package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"jungle-wallet/internal/domain"
)

var ErrWagerTransactionNotFound = errors.New("wager transaction not found")

type rowScanner interface {
	Scan(dest ...any) error
}

type WagerTransactionRepository struct{}

func NewWagerTransactionRepository() *WagerTransactionRepository {
	return &WagerTransactionRepository{}
}

func (r *WagerTransactionRepository) Insert(
	ctx context.Context,
	q Querier,
	transaction *domain.WagerTransaction,
) error {
	if transaction == nil {
		return fmt.Errorf("insert wager transaction: nil transaction")
	}

	var resultBalanceUnits any
	if transaction.ResultBalance() != nil {
		resultBalanceUnits = transaction.ResultBalance().Units()
	}

	_, err := q.Exec(
		ctx,
		`
		INSERT INTO wager_transactions (
			id,
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			wallet_id,
			player_id,
			round_id,
			game_id,
			kind,
			amount_units,
			currency,
			reference_external_transaction_id,
			reference_transaction_id,
			status,
			result_balance_units,
			failure_code,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15,
			$16, $17, $18, $19
		)
		`,
		transaction.ID(),
		nullableString(transaction.ExternalTransactionID()),
		nullableString(transaction.ProviderID()),
		nullableString(transaction.IdempotencyKey()),
		nullableString(transaction.PayloadHash()),
		transaction.WalletID(),
		transaction.PlayerID(),
		nullableString(transaction.RoundID()),
		nullableString(transaction.GameID()),
		transaction.Kind(),
		transaction.Money().Units(),
		transaction.Money().Currency(),
		nullableString(transaction.ReferenceExternalTransactionID()),
		nullableString(transaction.ReferenceTransactionID()),
		transaction.Status(),
		resultBalanceUnits,
		nullableString(transaction.FailureCode()),
		transaction.CreatedAt(),
		transaction.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("insert wager transaction: %w", err)
	}

	return nil
}

func (r *WagerTransactionRepository) FindByProviderAndExternalID(
	ctx context.Context,
	q Querier,
	providerID string,
	externalTransactionID string,
) (*domain.WagerTransaction, error) {
	return r.findOne(
		ctx,
		q,
		`
		SELECT
			id,
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			wallet_id,
			player_id,
			round_id,
			game_id,
			kind,
			amount_units,
			currency,
			COALESCE(reference_external_transaction_id, ''),
			COALESCE(reference_transaction_id, ''),
			status,
			result_balance_units,
			COALESCE(failure_code, ''),
			created_at,
			updated_at
		FROM wager_transactions
		WHERE provider_id = $1
		  AND external_transaction_id = $2
		`,
		providerID,
		externalTransactionID,
	)
}

func (r *WagerTransactionRepository) FindByProviderAndIdempotencyKey(
	ctx context.Context,
	q Querier,
	providerID string,
	idempotencyKey string,
) (*domain.WagerTransaction, error) {
	return r.findOne(
		ctx,
		q,
		`
		SELECT
			id,
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			wallet_id,
			player_id,
			round_id,
			game_id,
			kind,
			amount_units,
			currency,
			COALESCE(reference_external_transaction_id, ''),
			COALESCE(reference_transaction_id, ''),
			status,
			result_balance_units,
			COALESCE(failure_code, ''),
			created_at,
			updated_at
		FROM wager_transactions
		WHERE provider_id = $1
		  AND idempotency_key = $2
		`,
		providerID,
		idempotencyKey,
	)
}

func (r *WagerTransactionRepository) FindPendingByReference(
	ctx context.Context,
	q Querier,
	providerID string,
	referenceExternalTransactionID string,
) ([]*domain.WagerTransaction, error) {
	rows, err := q.Query(
		ctx,
		`
		SELECT
			id,
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			wallet_id,
			player_id,
			round_id,
			game_id,
			kind,
			amount_units,
			currency,
			COALESCE(reference_external_transaction_id, ''),
			COALESCE(reference_transaction_id, ''),
			status,
			result_balance_units,
			COALESCE(failure_code, ''),
			created_at,
			updated_at
		FROM wager_transactions
		WHERE provider_id = $1
		  AND reference_external_transaction_id = $2
		  AND status = $3
		ORDER BY created_at, id
		FOR UPDATE
		`,
		providerID,
		referenceExternalTransactionID,
		domain.WagerTransactionStatusPendingReference,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"find pending wager transactions by reference: %w",
			err,
		)
	}
	defer rows.Close()

	var transactions []*domain.WagerTransaction

	for rows.Next() {
		transaction, err := scanWagerTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf(
				"scan pending wager transaction: %w",
				err,
			)
		}

		transactions = append(transactions, transaction)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate pending wager transactions: %w",
			err,
		)
	}

	return transactions, nil
}

type PendingReferenceClaim struct {
	Transaction *domain.WagerTransaction
	Attempts    int
}

func (r *WagerTransactionRepository) ClaimNextPendingReference(
	ctx context.Context,
	q Querier,
	leaseSeconds int,
) (*PendingReferenceClaim, error) {
	if leaseSeconds <= 0 {
		return nil, fmt.Errorf("lease seconds must be positive")
	}

	var attempts int

	row := q.QueryRow(
		ctx,
		`
		WITH candidate AS (
			SELECT id
			FROM wager_transactions
			WHERE status = $1
			  AND (
				reference_next_attempt_at IS NULL
				OR reference_next_attempt_at <= NOW()
			  )
			ORDER BY
				reference_next_attempt_at NULLS FIRST,
				created_at,
				id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE wager_transactions AS wt
		SET
			reference_attempts = wt.reference_attempts + 1,
			reference_next_attempt_at =
				NOW() + ($2 * INTERVAL '1 second'),
			updated_at = GREATEST(wt.updated_at, NOW())
		FROM candidate
		WHERE wt.id = candidate.id
		RETURNING
			wt.id,
			wt.external_transaction_id,
			wt.provider_id,
			wt.idempotency_key,
			wt.payload_hash,
			wt.wallet_id,
			wt.player_id,
			wt.round_id,
			wt.game_id,
			wt.kind,
			wt.amount_units,
			wt.currency,
			COALESCE(wt.reference_external_transaction_id, ''),
			COALESCE(wt.reference_transaction_id, ''),
			wt.status,
			wt.result_balance_units,
			COALESCE(wt.failure_code, ''),
			wt.created_at,
			wt.updated_at,
			wt.reference_attempts
		`,
		domain.WagerTransactionStatusPendingReference,
		leaseSeconds,
	)

	transaction, err := scanPendingReferenceClaim(row, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrWagerTransactionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf(
			"claim pending reference transaction: %w",
			err,
		)
	}

	return &PendingReferenceClaim{
		Transaction: transaction,
		Attempts:    attempts,
	}, nil
}

func (r *WagerTransactionRepository) SchedulePendingReferenceRetry(
	ctx context.Context,
	q Querier,
	id string,
	nextAttemptAt time.Time,
) error {
	if id == "" {
		return fmt.Errorf(
			"schedule pending reference retry: empty transaction id",
		)
	}

	if nextAttemptAt.IsZero() {
		return fmt.Errorf(
			"schedule pending reference retry: zero next attempt time",
		)
	}

	tag, err := q.Exec(
		ctx,
		`
		UPDATE wager_transactions
		SET
			reference_next_attempt_at = $1,
			updated_at = GREATEST(updated_at, NOW())
		WHERE id = $2
		  AND status = $3
		`,
		nextAttemptAt.UTC(),
		id,
		domain.WagerTransactionStatusPendingReference,
	)
	if err != nil {
		return fmt.Errorf(
			"schedule pending reference retry: %w",
			err,
		)
	}

	if tag.RowsAffected() != 1 {
		return ErrWagerTransactionNotFound
	}

	return nil
}

func scanPendingReferenceClaim(
	row rowScanner,
	attempts *int,
) (*domain.WagerTransaction, error) {
	var (
		id                             string
		externalTransactionID          string
		providerID                     string
		idempotencyKey                 string
		payloadHash                    string
		walletID                       string
		playerID                       string
		roundID                        string
		gameID                         string
		kind                           domain.WagerTransactionKind
		amountUnits                    int64
		currency                       string
		referenceExternalTransactionID string
		referenceTransactionID         string
		status                         domain.WagerTransactionStatus
		resultBalanceUnits             *int64
		failureCode                    string
		createdAt                      time.Time
		updatedAt                      time.Time
	)

	err := row.Scan(
		&id,
		&externalTransactionID,
		&providerID,
		&idempotencyKey,
		&payloadHash,
		&walletID,
		&playerID,
		&roundID,
		&gameID,
		&kind,
		&amountUnits,
		&currency,
		&referenceExternalTransactionID,
		&referenceTransactionID,
		&status,
		&resultBalanceUnits,
		&failureCode,
		&createdAt,
		&updatedAt,
		attempts,
	)
	if err != nil {
		return nil, err
	}

	money, err := domain.FromUnits(amountUnits, currency)
	if err != nil {
		return nil, fmt.Errorf("restore wager money: %w", err)
	}

	var resultBalance *domain.Money
	if resultBalanceUnits != nil {
		balance, err := domain.FromUnits(
			*resultBalanceUnits,
			currency,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"restore result balance: %w",
				err,
			)
		}

		resultBalance = &balance
	}

	transaction, err := domain.RehydrateWagerTransaction(
		domain.RehydrateWagerTransactionParams{
			ID:                             id,
			ExternalTransactionID:          externalTransactionID,
			ProviderID:                     providerID,
			IdempotencyKey:                 idempotencyKey,
			PayloadHash:                    payloadHash,
			WalletID:                       walletID,
			PlayerID:                       playerID,
			RoundID:                        roundID,
			GameID:                         gameID,
			Kind:                           kind,
			Money:                          money,
			ReferenceExternalTransactionID: referenceExternalTransactionID,
			ReferenceTransactionID:         referenceTransactionID,
			Status:                         status,
			FailureCode:                    failureCode,
			ResultBalance:                  resultBalance,
			CreatedAt:                      createdAt,
			UpdatedAt:                      updatedAt,
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"restore wager transaction: %w",
			err,
		)
	}

	return transaction, nil
}

func (r *WagerTransactionRepository) findOne(
	ctx context.Context,
	q Querier,
	query string,
	args ...any,
) (*domain.WagerTransaction, error) {
	transaction, err := scanWagerTransaction(
		q.QueryRow(ctx, query, args...),
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrWagerTransactionNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("find wager transaction: %w", err)
	}

	return transaction, nil
}

func scanWagerTransaction(
	row rowScanner,
) (*domain.WagerTransaction, error) {
	var (
		id                             string
		externalTransactionID          pgtype.Text
		providerID                     pgtype.Text
		idempotencyKey                 pgtype.Text
		payloadHash                    pgtype.Text
		walletID                       string
		playerID                       string
		roundID                        pgtype.Text
		gameID                         pgtype.Text
		kind                           domain.WagerTransactionKind
		amountUnits                    int64
		currency                       string
		referenceExternalTransactionID string
		referenceTransactionID         string
		status                         domain.WagerTransactionStatus
		resultBalanceUnits             *int64
		failureCode                    string
		createdAt                      time.Time
		updatedAt                      time.Time
	)

	err := row.Scan(
		&id,
		&externalTransactionID,
		&providerID,
		&idempotencyKey,
		&payloadHash,
		&walletID,
		&playerID,
		&roundID,
		&gameID,
		&kind,
		&amountUnits,
		&currency,
		&referenceExternalTransactionID,
		&referenceTransactionID,
		&status,
		&resultBalanceUnits,
		&failureCode,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}

	money, err := domain.FromUnits(amountUnits, currency)
	if err != nil {
		return nil, fmt.Errorf("restore wager money: %w", err)
	}

	var resultBalance *domain.Money

	if resultBalanceUnits != nil {
		balance, err := domain.FromUnits(
			*resultBalanceUnits,
			currency,
		)
		if err != nil {
			return nil, fmt.Errorf("restore result balance: %w", err)
		}

		resultBalance = &balance
	}

	transaction, err := domain.RehydrateWagerTransaction(
		domain.RehydrateWagerTransactionParams{
			ID:                             id,
			ExternalTransactionID:          externalTransactionID.String,
			ProviderID:                     providerID.String,
			IdempotencyKey:                 idempotencyKey.String,
			PayloadHash:                    payloadHash.String,
			WalletID:                       walletID,
			PlayerID:                       playerID,
			RoundID:                        roundID.String,
			GameID:                         gameID.String,
			Kind:                           kind,
			Money:                          money,
			ReferenceExternalTransactionID: referenceExternalTransactionID,
			ReferenceTransactionID:         referenceTransactionID,
			Status:                         status,
			FailureCode:                    failureCode,
			ResultBalance:                  resultBalance,
			CreatedAt:                      createdAt,
			UpdatedAt:                      updatedAt,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("restore wager transaction: %w", err)
	}

	return transaction, nil
}

func (r *WagerTransactionRepository) Update(
	ctx context.Context,
	q Querier,
	transaction *domain.WagerTransaction,
) error {
	if transaction == nil {
		return fmt.Errorf("update wager transaction: nil transaction")
	}

	var resultBalanceUnits any

	if transaction.ResultBalance() != nil {
		resultBalanceUnits = transaction.ResultBalance().Units()
	}

	tag, err := q.Exec(
		ctx,
		`
		UPDATE wager_transactions
		SET
			reference_transaction_id = $1,
			status = $2,
			result_balance_units = $3,
			failure_code = $4,
			updated_at = $5
		WHERE id = $6
		`,
		nullableString(transaction.ReferenceTransactionID()),
		transaction.Status(),
		resultBalanceUnits,
		nullableString(transaction.FailureCode()),
		transaction.UpdatedAt(),
		transaction.ID(),
	)
	if err != nil {
		return fmt.Errorf("update wager transaction: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return ErrWagerTransactionNotFound
	}

	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}

	return value
}
