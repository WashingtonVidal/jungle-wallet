package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/postgres"
)

type ProcessWagerInput struct {
	ID                             string
	LedgerID                       string
	WalletID                       string
	ExternalTransactionID          string
	ProviderID                     string
	IdempotencyKey                 string
	PayloadHash                    string
	ReferenceExternalTransactionID string

	PlayerID string
	RoundID  string
	GameID   string

	Kind  domain.WagerTransactionKind
	Money domain.Money

	CreatedAt time.Time
}

type ProcessWagerResult struct {
	Transaction *domain.WagerTransaction
	Replay      bool
}

type wagerProcessedEvent struct {
	TransactionID         string                        `json:"transactionId"`
	ExternalTransactionID string                        `json:"externalTransactionId"`
	ProviderID            string                        `json:"providerId"`
	PlayerID              string                        `json:"playerId"`
	Kind                  domain.WagerTransactionKind   `json:"kind"`
	Status                domain.WagerTransactionStatus `json:"status"`
	OccurredAt            time.Time                     `json:"occurredAt"`
}

type ProcessWagerService struct {
	db           *postgres.DB
	wallets      *postgres.WalletRepository
	transactions *postgres.WagerTransactionRepository
	ledger       *postgres.LedgerRepository
	outbox       *postgres.OutboxRepository
}

func NewProcessWagerService(
	db *postgres.DB,
	wallets *postgres.WalletRepository,
	transactions *postgres.WagerTransactionRepository,
	ledger *postgres.LedgerRepository,
	outbox *postgres.OutboxRepository,
) *ProcessWagerService {
	return &ProcessWagerService{
		db:           db,
		wallets:      wallets,
		transactions: transactions,
		ledger:       ledger,
		outbox:       outbox,
	}
}

func (s *ProcessWagerService) Execute(
	ctx context.Context,
	input ProcessWagerInput,
) (*ProcessWagerResult, error) {
	if s == nil ||
		s.db == nil ||
		s.wallets == nil ||
		s.transactions == nil ||
		s.ledger == nil ||
		s.outbox == nil {
		return nil, fmt.Errorf("process wager service is not initialized")
	}

	var result *ProcessWagerResult

	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		var err error

		result, err = s.ExecuteTx(ctx, tx, input)
		return err
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (s *ProcessWagerService) RetryPendingReference(
	ctx context.Context,
	leaseSeconds int,
) (bool, error) {
	if s == nil ||
		s.db == nil ||
		s.wallets == nil ||
		s.transactions == nil ||
		s.ledger == nil ||
		s.outbox == nil {
		return false, fmt.Errorf(
			"process wager service is not initialized",
		)
	}

	if leaseSeconds <= 0 {
		return false, fmt.Errorf(
			"lease seconds must be positive",
		)
	}

	processed := false

	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		claim, err := s.transactions.ClaimNextPendingReference(
			ctx,
			tx,
			leaseSeconds,
		)
		if errors.Is(
			err,
			postgres.ErrWagerTransactionNotFound,
		) {
			return nil
		}
		if err != nil {
			return err
		}

		pending := claim.Transaction

		referenced, err :=
			s.transactions.FindByProviderAndExternalID(
				ctx,
				tx,
				pending.ProviderID(),
				pending.ReferenceExternalTransactionID(),
			)

		if errors.Is(
			err,
			postgres.ErrWagerTransactionNotFound,
		) {
			const maxReferenceAttempts = 6

			if claim.Attempts < maxReferenceAttempts {
				backoff := time.Second * time.Duration(
					1<<(claim.Attempts-1),
				)

				nextAttemptAt := time.Now().UTC().Add(backoff)

				if err := s.transactions.SchedulePendingReferenceRetry(
					ctx,
					tx,
					pending.ID(),
					nextAttemptAt,
				); err != nil {
					return err
				}

				return nil
			}

			now := time.Now().UTC()

			if err := pending.Reject(
				"REFERENCE_NOT_FOUND",
				now,
			); err != nil {
				return err
			}

			if err := s.transactions.Update(
				ctx,
				tx,
				pending,
			); err != nil {
				return err
			}

			return nil
		}
		if err != nil {
			return err
		}

		if err := s.processPendingReference(
			ctx,
			tx,
			pending,
			referenced,
			time.Now().UTC(),
		); err != nil {
			return err
		}

		processed = true
		return nil
	})
	if err != nil {
		return false, err
	}

	return processed, nil
}

func (s *ProcessWagerService) ExecuteTx(
	ctx context.Context,
	tx pgx.Tx,
	input ProcessWagerInput,
) (*ProcessWagerResult, error) {
	if s == nil ||
		s.wallets == nil ||
		s.transactions == nil ||
		s.ledger == nil ||
		s.outbox == nil {
		return nil, fmt.Errorf("process wager service is not initialized")
	}

	if tx == nil {
		return nil, fmt.Errorf("transaction is required")
	}

	// Persistent idempotency check by provider + external transaction ID.
	existing, err := s.transactions.FindByProviderAndExternalID(
		ctx,
		tx,
		input.ProviderID,
		input.ExternalTransactionID,
	)

	if err == nil {
		if existing.PayloadHash() != input.PayloadHash {
			return nil, fmt.Errorf(
				"%w: same external transaction with different payload",
				domain.ErrInvalid,
			)
		}

		return &ProcessWagerResult{
			Transaction: existing,
			Replay:      true,
		}, nil
	}

	if !errors.Is(err, postgres.ErrWagerTransactionNotFound) {
		return nil, err
	}

	// The idempotency key is an independent persistent identity.
	// Reusing it with another payload must also be rejected.
	existing, err = s.transactions.FindByProviderAndIdempotencyKey(
		ctx,
		tx,
		input.ProviderID,
		input.IdempotencyKey,
	)

	if err == nil {
		if existing.PayloadHash() != input.PayloadHash {
			return nil, fmt.Errorf(
				"%w: same idempotency key with different payload",
				domain.ErrInvalid,
			)
		}

		return &ProcessWagerResult{
			Transaction: existing,
			Replay:      true,
		}, nil
	}

	if !errors.Is(err, postgres.ErrWagerTransactionNotFound) {
		return nil, err
	}

	// The wallet row is locked until COMMIT or ROLLBACK.
	wallet, err := s.wallets.FindByPlayerAndCurrencyForUpdate(
		ctx,
		tx,
		input.PlayerID,
		input.Money.Currency(),
	)
	if err != nil {
		return nil, err
	}

	// Re-check persistent idempotency after acquiring the wallet lock.
	// Another process may have completed the same transaction while
	// this transaction was waiting for FOR UPDATE.
	existing, err = s.transactions.FindByProviderAndExternalID(
		ctx,
		tx,
		input.ProviderID,
		input.ExternalTransactionID,
	)
	if err == nil {
		if existing.PayloadHash() != input.PayloadHash {
			return nil, fmt.Errorf(
				"%w: same external transaction with different payload",
				domain.ErrInvalid,
			)
		}

		return &ProcessWagerResult{
			Transaction: existing,
			Replay:      true,
		}, nil
	}

	if !errors.Is(err, postgres.ErrWagerTransactionNotFound) {
		return nil, err
	}

	existing, err = s.transactions.FindByProviderAndIdempotencyKey(
		ctx,
		tx,
		input.ProviderID,
		input.IdempotencyKey,
	)
	if err == nil {
		if existing.PayloadHash() != input.PayloadHash {
			return nil, fmt.Errorf(
				"%w: same idempotency key with different payload",
				domain.ErrInvalid,
			)
		}

		return &ProcessWagerResult{
			Transaction: existing,
			Replay:      true,
		}, nil
	}

	if !errors.Is(err, postgres.ErrWagerTransactionNotFound) {
		return nil, err
	}

	walletBefore, err := wallet.State()
	if err != nil {
		return nil, err
	}

	if walletBefore.ID != input.WalletID {
		return nil, fmt.Errorf(
			"%w: wallet does not belong to player and currency",
			domain.ErrInvalid,
		)
	}

	transaction, err := domain.NewWagerTransaction(
		domain.NewWagerTransactionParams{
			ID:                             input.ID,
			ExternalTransactionID:          input.ExternalTransactionID,
			ProviderID:                     input.ProviderID,
			IdempotencyKey:                 input.IdempotencyKey,
			PayloadHash:                    input.PayloadHash,
			WalletID:                       walletBefore.ID,
			PlayerID:                       input.PlayerID,
			RoundID:                        input.RoundID,
			GameID:                         input.GameID,
			Kind:                           input.Kind,
			Money:                          input.Money,
			ReferenceExternalTransactionID: input.ReferenceExternalTransactionID,
			CreatedAt:                      input.CreatedAt,
		},
	)
	if err != nil {
		return nil, err
	}

	var movement *domain.WagerMovement

	switch input.Kind {
	case domain.WagerTransactionKindBet,
		domain.WagerTransactionKindWin,
		domain.WagerTransactionKindLoss:

		movement, err = domain.ApplyWagerMovement(
			wallet,
			input.Kind,
			input.Money,
			input.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

	case domain.WagerTransactionKindRefund,
		domain.WagerTransactionKindRollback:

		referenced, findErr := s.transactions.FindByProviderAndExternalID(
			ctx,
			tx,
			input.ProviderID,
			input.ReferenceExternalTransactionID,
		)
		if findErr != nil {
			if errors.Is(
				findErr,
				postgres.ErrWagerTransactionNotFound,
			) {
				if err := transaction.MarkPendingReference(
					input.CreatedAt,
				); err != nil {
					return nil, err
				}

				if err := s.transactions.Insert(
					ctx,
					tx,
					transaction,
				); err != nil {
					return nil, err
				}

				return &ProcessWagerResult{
					Transaction: transaction,
					Replay:      false,
				}, nil
			}

			return nil, findErr
		}

		if err := transaction.ResolveReference(
			referenced.ID(),
			input.CreatedAt,
		); err != nil {
			return nil, err
		}

		movement, err = domain.ApplyReversalMovement(
			wallet,
			input.Kind,
			referenced.Kind(),
			input.Money,
			input.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf(
			"%w: unsupported wager kind",
			domain.ErrInvalidTransactionKind,
		)
	}

	walletAfter, err := wallet.State()
	if err != nil {
		return nil, err
	}

	if err := transaction.MarkProcessed(
		walletAfter.Balance,
		input.CreatedAt,
	); err != nil {
		return nil, err
	}

	// Persist the operation before related ledger rows reference it.
	if err := s.transactions.Insert(ctx, tx, transaction); err != nil {
		return nil, err
	}

	// A newly processed transaction may satisfy REFUND/ROLLBACK
	// transactions that arrived before their reference.

	// LOSS has no financial movement, therefore no ledger entry.
	if movement != nil {
		if err := s.wallets.Update(
			ctx,
			tx,
			wallet,
			walletBefore.Version,
		); err != nil {
			return nil, err
		}

		entry, err := domain.NewLedger(domain.LedgerState{
			ID:            input.LedgerID,
			WalletID:      walletBefore.ID,
			TransactionID: transaction.ID(),
			Direction:     movement.Direction,
			Money:         input.Money,
			BalanceBefore: movement.BalanceBefore,
			BalanceAfter:  movement.BalanceAfter,
			CreatedAt:     input.CreatedAt,
		})
		if err != nil {
			return nil, err
		}

		if err := s.ledger.Insert(ctx, tx, entry); err != nil {
			return nil, err
		}
	}

	pendingTransactions, err := s.transactions.FindPendingByReference(
		ctx,
		tx,
		transaction.ProviderID(),
		transaction.ExternalTransactionID(),
	)
	if err != nil {
		return nil, err
	}

	for _, pending := range pendingTransactions {
		if err := s.processPendingReference(
			ctx,
			tx,
			pending,
			transaction,
			input.CreatedAt,
		); err != nil {
			return nil, err
		}
	}

	eventPayload, err := json.Marshal(wagerProcessedEvent{
		TransactionID:         transaction.ID(),
		ExternalTransactionID: transaction.ExternalTransactionID(),
		ProviderID:            transaction.ProviderID(),
		PlayerID:              transaction.PlayerID(),
		Kind:                  transaction.Kind(),
		Status:                transaction.Status(),
		OccurredAt:            input.CreatedAt.UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf(
			"marshal wager processed event: %w",
			err,
		)
	}

	if err := s.outbox.Insert(
		ctx,
		tx,
		postgres.OutboxMessage{
			ID:            uuid.NewString(),
			AggregateType: "WAGER_TRANSACTION",
			AggregateID:   transaction.ID(),
			EventType:     "WAGER_TRANSACTION_PROCESSED",
			Payload:       eventPayload,
			AvailableAt:   input.CreatedAt.UTC(),
			CreatedAt:     input.CreatedAt.UTC(),
		},
	); err != nil {
		return nil, err
	}

	return &ProcessWagerResult{
		Transaction: transaction,
		Replay:      false,
	}, nil
}

func (s *ProcessWagerService) processPendingReference(
	ctx context.Context,
	tx pgx.Tx,
	pending *domain.WagerTransaction,
	referenced *domain.WagerTransaction,
	now time.Time,
) error {
	if pending == nil {
		return fmt.Errorf("pending wager transaction is required")
	}

	if referenced == nil {
		return fmt.Errorf("referenced wager transaction is required")
	}

	if err := pending.ResolveReference(
		referenced.ID(),
		now,
	); err != nil {
		return err
	}

	wallet, err := s.wallets.FindByPlayerAndCurrencyForUpdate(
		ctx,
		tx,
		pending.PlayerID(),
		pending.Money().Currency(),
	)
	if err != nil {
		return err
	}

	walletBefore, err := wallet.State()
	if err != nil {
		return err
	}

	movement, err := domain.ApplyReversalMovement(
		wallet,
		pending.Kind(),
		referenced.Kind(),
		pending.Money(),
		now,
	)
	if err != nil {
		return err
	}

	walletAfter, err := wallet.State()
	if err != nil {
		return err
	}

	if err := pending.MarkProcessed(
		walletAfter.Balance,
		now,
	); err != nil {
		return err
	}

	if err := s.wallets.Update(
		ctx,
		tx,
		wallet,
		walletBefore.Version,
	); err != nil {
		return err
	}

	entry, err := domain.NewLedger(domain.LedgerState{
		ID:            uuid.NewString(),
		WalletID:      walletBefore.ID,
		TransactionID: pending.ID(),
		Direction:     movement.Direction,
		Money:         pending.Money(),
		BalanceBefore: movement.BalanceBefore,
		BalanceAfter:  movement.BalanceAfter,
		CreatedAt:     now,
	})
	if err != nil {
		return err
	}

	if err := s.ledger.Insert(
		ctx,
		tx,
		entry,
	); err != nil {
		return err
	}

	if err := s.transactions.Update(
		ctx,
		tx,
		pending,
	); err != nil {
		return err
	}

	eventPayload, err := json.Marshal(wagerProcessedEvent{
		TransactionID:         pending.ID(),
		ExternalTransactionID: pending.ExternalTransactionID(),
		ProviderID:            pending.ProviderID(),
		PlayerID:              pending.PlayerID(),
		Kind:                  pending.Kind(),
		Status:                pending.Status(),
		OccurredAt:            now.UTC(),
	})
	if err != nil {
		return fmt.Errorf(
			"marshal pending wager processed event: %w",
			err,
		)
	}

	if err := s.outbox.Insert(
		ctx,
		tx,
		postgres.OutboxMessage{
			ID:            uuid.NewString(),
			AggregateType: "WAGER_TRANSACTION",
			AggregateID:   pending.ID(),
			EventType:     "WAGER_TRANSACTION_PROCESSED",
			Payload:       eventPayload,
			AvailableAt:   now.UTC(),
			CreatedAt:     now.UTC(),
		},
	); err != nil {
		return err
	}

	return nil
}
