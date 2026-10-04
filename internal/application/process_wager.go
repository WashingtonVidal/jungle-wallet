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
		// Persistent idempotency check.
		// Persistent idempotency check by provider + external transaction ID.
		existing, err := s.transactions.FindByProviderAndExternalID(
			ctx,
			tx,
			input.ProviderID,
			input.ExternalTransactionID,
		)

		if err == nil {
			if existing.PayloadHash() != input.PayloadHash {
				return fmt.Errorf(
					"%w: same external transaction with different payload",
					domain.ErrInvalid,
				)
			}

			result = &ProcessWagerResult{
				Transaction: existing,
				Replay:      true,
			}

			return nil
		}

		if !errors.Is(err, postgres.ErrWagerTransactionNotFound) {
			return err
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
				return fmt.Errorf(
					"%w: same idempotency key with different payload",
					domain.ErrInvalid,
				)
			}

			result = &ProcessWagerResult{
				Transaction: existing,
				Replay:      true,
			}

			return nil
		}

		if !errors.Is(err, postgres.ErrWagerTransactionNotFound) {
			return err
		}

		// The wallet row is locked until COMMIT or ROLLBACK.
		wallet, err := s.wallets.FindByPlayerAndCurrencyForUpdate(
			ctx,
			tx,
			input.PlayerID,
			input.Money.Currency(),
		)
		if err != nil {
			return err
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
				return fmt.Errorf(
					"%w: same external transaction with different payload",
					domain.ErrInvalid,
				)
			}

			result = &ProcessWagerResult{
				Transaction: existing,
				Replay:      true,
			}

			return nil
		}

		if !errors.Is(err, postgres.ErrWagerTransactionNotFound) {
			return err
		}

		existing, err = s.transactions.FindByProviderAndIdempotencyKey(
			ctx,
			tx,
			input.ProviderID,
			input.IdempotencyKey,
		)
		if err == nil {
			if existing.PayloadHash() != input.PayloadHash {
				return fmt.Errorf(
					"%w: same idempotency key with different payload",
					domain.ErrInvalid,
				)
			}

			result = &ProcessWagerResult{
				Transaction: existing,
				Replay:      true,
			}

			return nil
		}

		if !errors.Is(err, postgres.ErrWagerTransactionNotFound) {
			return err
		}

		walletBefore, err := wallet.State()
		if err != nil {
			return err
		}

		if walletBefore.ID != input.WalletID {
			return fmt.Errorf(
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
			return err
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
				return err
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
						return err
					}

					if err := s.transactions.Insert(
						ctx,
						tx,
						transaction,
					); err != nil {
						return err
					}

					result = &ProcessWagerResult{
						Transaction: transaction,
						Replay:      false,
					}

					return nil
				}

				return findErr
			}

			if err := transaction.ResolveReference(
				referenced.ID(),
				input.CreatedAt,
			); err != nil {
				return err
			}

			movement, err = domain.ApplyReversalMovement(
				wallet,
				input.Kind,
				referenced.Kind(),
				input.Money,
				input.CreatedAt,
			)
			if err != nil {
				return err
			}

		default:
			return fmt.Errorf(
				"%w: unsupported wager kind",
				domain.ErrInvalidTransactionKind,
			)
		}

		walletAfter, err := wallet.State()
		if err != nil {
			return err
		}

		if err := transaction.MarkProcessed(
			walletAfter.Balance,
			input.CreatedAt,
		); err != nil {
			return err
		}

		// Persist the operation before related ledger rows reference it.
		if err := s.transactions.Insert(ctx, tx, transaction); err != nil {
			return err
		}

		// LOSS has no financial movement, therefore no ledger entry.
		if movement != nil {
			if err := s.wallets.Update(
				ctx,
				tx,
				wallet,
				walletBefore.Version,
			); err != nil {
				return err
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
				return err
			}

			if err := s.ledger.Insert(ctx, tx, entry); err != nil {
				return err
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
			return fmt.Errorf("marshal wager processed event: %w", err)
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
			return err
		}

		result = &ProcessWagerResult{
			Transaction: transaction,
			Replay:      false,
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}
