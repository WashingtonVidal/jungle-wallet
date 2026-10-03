package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"jungle-wallet/internal/domain"
)

var ErrWalletNotFound = errors.New("wallet not found")

type WalletRepository struct{}

func NewWalletRepository() *WalletRepository {
	return &WalletRepository{}
}

// Insert persists a newly created wallet.
func (r *WalletRepository) Insert(
	ctx context.Context,
	q Querier,
	wallet *domain.Wallet,
) error {
	state, err := wallet.State()
	if err != nil {
		return err
	}

	_, err = q.Exec(
		ctx,
		`
		INSERT INTO wallets (
			id,
			player_id,
			currency,
			balance_units,
			version,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		`,
		state.ID,
		state.PlayerID,
		state.Balance.Currency(),
		state.Balance.Units(),
		state.Version,
		state.CreatedAt,
		state.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert wallet: %w", err)
	}

	return nil
}

// FindByPlayerAndCurrency loads a wallet without locking it.
// Suitable for read-only operations such as balance queries.
func (r *WalletRepository) FindByPlayerAndCurrency(
	ctx context.Context,
	q Querier,
	playerID string,
	currency string,
) (*domain.Wallet, error) {
	return r.findByPlayerAndCurrency(
		ctx,
		q,
		playerID,
		currency,
		false,
	)
}

// FindByPlayerAndCurrencyForUpdate loads and locks the wallet row.
//
// The lock is held until the surrounding PostgreSQL transaction commits
// or rolls back. This coordinates financial writes across application instances.
func (r *WalletRepository) FindByPlayerAndCurrencyForUpdate(
	ctx context.Context,
	q Querier,
	playerID string,
	currency string,
) (*domain.Wallet, error) {
	return r.findByPlayerAndCurrency(
		ctx,
		q,
		playerID,
		currency,
		true,
	)
}

func (r *WalletRepository) findByPlayerAndCurrency(
	ctx context.Context,
	q Querier,
	playerID string,
	currency string,
	forUpdate bool,
) (*domain.Wallet, error) {
	query := `
		SELECT
			id,
			player_id,
			currency,
			balance_units,
			version,
			created_at,
			updated_at
		FROM wallets
		WHERE player_id = $1
		  AND currency = $2
	`

	if forUpdate {
		query += ` FOR UPDATE`
	}

	var (
		id           string
		storedPlayer string
		storedCurr   string
		balanceUnits int64
		version      int64
		createdAt    time.Time
		updatedAt    time.Time
	)

	err := q.QueryRow(
		ctx,
		query,
		playerID,
		currency,
	).Scan(
		&id,
		&storedPlayer,
		&storedCurr,
		&balanceUnits,
		&version,
		&createdAt,
		&updatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrWalletNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("find wallet: %w", err)
	}

	balance, err := domain.FromUnits(balanceUnits, storedCurr)
	if err != nil {
		return nil, fmt.Errorf("restore wallet balance: %w", err)
	}

	wallet, err := domain.RestoreWallet(domain.WalletState{
		ID:        id,
		PlayerID:  storedPlayer,
		Balance:   balance,
		Version:   version,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("restore wallet: %w", err)
	}

	return wallet, nil
}

// Update persists the wallet after a financial movement.
//
// The version in the WHERE clause provides an additional optimistic check.
// Financial processing will also use SELECT ... FOR UPDATE.
func (r *WalletRepository) Update(
	ctx context.Context,
	q Querier,
	wallet *domain.Wallet,
	expectedVersion int64,
) error {
	state, err := wallet.State()
	if err != nil {
		return err
	}

	tag, err := q.Exec(
		ctx,
		`
		UPDATE wallets
		SET
			balance_units = $1,
			version = $2,
			updated_at = $3
		WHERE id = $4
		  AND version = $5
		`,
		state.Balance.Units(),
		state.Version,
		state.UpdatedAt,
		state.ID,
		expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("update wallet: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf(
			"%w: wallet version changed",
			domain.ErrTransition,
		)
	}

	return nil
}
