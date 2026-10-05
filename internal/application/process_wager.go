package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"jungle-wallet/internal/observability"
	"log/slog"
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
	LegacyPayloadHash              string
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
	WalletID              string                        `json:"walletId"`
	ExternalTransactionID string                        `json:"externalTransactionId,omitempty"`
	ProviderID            string                        `json:"providerId,omitempty"`
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

		observability.Inc("reference_retries")
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
		) || (err == nil && !referenced.Status().IsTerminal()) {
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
			if now.Before(pending.UpdatedAt()) {
				now = pending.UpdatedAt()
			}

			return s.reject(ctx, tx, pending, "REFERENCE_NOT_FOUND", now, true)
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
) (result *ProcessWagerResult, err error) {
	start := time.Now()
	defer func() {
		observability.Observe(time.Since(start))
		if err != nil {
			if errors.Is(err, domain.ErrIdempotencyConflict) {
				observability.Inc("idempotency_conflicts")
			}
			return
		}
		if result != nil {
			if result.Replay {
				observability.Inc("duplicates")
			} else {
				observability.Inc("transactions_" + string(result.Transaction.Status()))
			}
			slog.Info("wager processed", "transactionId", result.Transaction.ID(), "correlationId", result.Transaction.ID(), "walletId", result.Transaction.WalletID(), "providerId", result.Transaction.ProviderID(), "status", result.Transaction.Status(), "replay", result.Replay)
		}
	}()
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
	if input.CreatedAt.IsZero() {
		return nil, domain.ErrInvalidTransaction
	}

	// Persistent idempotency check by provider + external transaction ID.
	existing, err := s.transactions.FindByProviderAndExternalID(
		ctx,
		tx,
		input.ProviderID,
		input.ExternalTransactionID,
	)

	if err == nil {
		if existing.PayloadHash() != input.PayloadHash && existing.PayloadHash() != input.LegacyPayloadHash {
			return nil, fmt.Errorf(
				"%w: same external transaction with different payload",
				domain.ErrIdempotencyConflict,
			)
		}

		keyed, keyErr := s.transactions.FindByProviderAndIdempotencyKey(ctx, tx, input.ProviderID, input.IdempotencyKey)
		if keyErr != nil && !errors.Is(keyErr, postgres.ErrWagerTransactionNotFound) {
			return nil, keyErr
		}
		if keyErr == nil && keyed.ID() != existing.ID() {
			return nil, domain.ErrIdempotencyConflict
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
		if existing.PayloadHash() != input.PayloadHash && existing.PayloadHash() != input.LegacyPayloadHash {
			return nil, fmt.Errorf(
				"%w: same idempotency key with different payload",
				domain.ErrIdempotencyConflict,
			)
		}

		keyed, keyErr := s.transactions.FindByProviderAndIdempotencyKey(ctx, tx, input.ProviderID, input.IdempotencyKey)
		if keyErr != nil && !errors.Is(keyErr, postgres.ErrWagerTransactionNotFound) {
			return nil, keyErr
		}
		if keyErr == nil && keyed.ID() != existing.ID() {
			return nil, domain.ErrIdempotencyConflict
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
		if existing.PayloadHash() != input.PayloadHash && existing.PayloadHash() != input.LegacyPayloadHash {
			return nil, fmt.Errorf(
				"%w: same external transaction with different payload",
				domain.ErrIdempotencyConflict,
			)
		}

		keyed, keyErr := s.transactions.FindByProviderAndIdempotencyKey(ctx, tx, input.ProviderID, input.IdempotencyKey)
		if keyErr != nil && !errors.Is(keyErr, postgres.ErrWagerTransactionNotFound) {
			return nil, keyErr
		}
		if keyErr == nil && keyed.ID() != existing.ID() {
			return nil, domain.ErrIdempotencyConflict
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
		if existing.PayloadHash() != input.PayloadHash && existing.PayloadHash() != input.LegacyPayloadHash {
			return nil, fmt.Errorf(
				"%w: same idempotency key with different payload",
				domain.ErrIdempotencyConflict,
			)
		}

		keyed, keyErr := s.transactions.FindByProviderAndIdempotencyKey(ctx, tx, input.ProviderID, input.IdempotencyKey)
		if keyErr != nil && !errors.Is(keyErr, postgres.ErrWagerTransactionNotFound) {
			return nil, keyErr
		}
		if keyErr == nil && keyed.ID() != existing.ID() {
			return nil, domain.ErrIdempotencyConflict
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

	if input.CreatedAt.Before(walletBefore.UpdatedAt) {
		input.CreatedAt = walletBefore.UpdatedAt
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
		if input.Kind == domain.WagerTransactionKindWin && input.ReferenceExternalTransactionID != "" {
			ref, err := s.transactions.FindByProviderAndExternalID(ctx, tx, input.ProviderID, input.ReferenceExternalTransactionID)
			code := ""
			if errors.Is(err, postgres.ErrWagerTransactionNotFound) {
				code = "REFERENCE_PENDING"
			} else if err != nil {
				return nil, err
			} else {
				code, err = s.validateReference(ctx, tx, transaction, ref)
				if err != nil {
					return nil, err
				}
			}
			if code == "REFERENCE_PENDING" {
				if err := transaction.MarkPendingReference(input.CreatedAt); err != nil {
					return nil, err
				}
				if err := s.transactions.Insert(ctx, tx, transaction); err != nil {
					return nil, err
				}
				if err := s.statusEvent(ctx, tx, transaction, "WAGER_TRANSACTION_PENDING_REFERENCE", input.CreatedAt); err != nil {
					return nil, err
				}
				return &ProcessWagerResult{Transaction: transaction}, nil
			}
			if code != "" {
				if err := s.reject(ctx, tx, transaction, code, input.CreatedAt, false); err != nil {
					return nil, err
				}
				return &ProcessWagerResult{Transaction: transaction}, nil
			}
			if err := transaction.ResolveReference(ref.ID(), input.CreatedAt); err != nil {
				return nil, err
			}
		}
		movement, err = domain.ApplyWagerMovement(
			wallet,
			input.Kind,
			input.Money,
			input.CreatedAt,
		)
		if err != nil {
			code := "INVALID_OPERATION"
			if errors.Is(err, domain.ErrInsufficient) {
				code = "INSUFFICIENT_BALANCE"
			}
			if errors.Is(err, domain.ErrOverflow) {
				code = "BALANCE_OVERFLOW"
			}
			if rejectErr := s.reject(ctx, tx, transaction, code, input.CreatedAt, false); rejectErr != nil {
				return nil, rejectErr
			}
			return &ProcessWagerResult{Transaction: transaction}, nil
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

				if err := s.statusEvent(ctx, tx, transaction, "WAGER_TRANSACTION_PENDING_REFERENCE", input.CreatedAt); err != nil {
					return nil, err
				}
				return &ProcessWagerResult{
					Transaction: transaction,
					Replay:      false,
				}, nil
			}

			return nil, findErr
		}

		code, err := s.validateReference(ctx, tx, transaction, referenced)
		if err != nil {
			return nil, err
		}
		if code == "REFERENCE_PENDING" {
			if err := transaction.MarkPendingReference(input.CreatedAt); err != nil {
				return nil, err
			}
			if err := s.transactions.Insert(ctx, tx, transaction); err != nil {
				return nil, err
			}
			if err := s.statusEvent(ctx, tx, transaction, "WAGER_TRANSACTION_PENDING_REFERENCE", input.CreatedAt); err != nil {
				return nil, err
			}
			return &ProcessWagerResult{Transaction: transaction}, nil
		}
		if code != "" {
			if err := s.reject(ctx, tx, transaction, code, input.CreatedAt, false); err != nil {
				return nil, err
			}
			return &ProcessWagerResult{Transaction: transaction}, nil
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
			code := "INVALID_REVERSAL"
			if errors.Is(err, domain.ErrInsufficient) {
				code = "REVERSAL_INSUFFICIENT_BALANCE"
			}
			if errors.Is(err, domain.ErrOverflow) {
				code = "BALANCE_OVERFLOW"
			}
			if err := s.reject(ctx, tx, transaction, code, input.CreatedAt, false); err != nil {
				return nil, err
			}
			return &ProcessWagerResult{Transaction: transaction}, nil
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
		if err := s.balanceEvent(ctx, tx, transaction, movement, walletAfter.Version, input.CreatedAt); err != nil {
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
		WalletID:              transaction.WalletID(),
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

	if now.Before(pending.UpdatedAt()) {
		now = pending.UpdatedAt()
	}
	code, err := s.validateReference(ctx, tx, pending, referenced)
	if err != nil {
		return err
	}
	if code == "REFERENCE_PENDING" {
		return s.transactions.SchedulePendingReferenceRetry(ctx, tx, pending.ID(), now.Add(time.Second))
	}
	if code != "" {
		return s.reject(ctx, tx, pending, code, now, true)
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

	if now.Before(walletBefore.UpdatedAt) {
		now = walletBefore.UpdatedAt
	}
	var movement *domain.WagerMovement
	if pending.Kind() == domain.WagerTransactionKindWin {
		movement, err = domain.ApplyWagerMovement(wallet, pending.Kind(), pending.Money(), now)
	} else {
		movement, err = domain.ApplyReversalMovement(wallet, pending.Kind(), referenced.Kind(), pending.Money(), now)
	}
	if err != nil {
		code := "INVALID_REVERSAL"
		if errors.Is(err, domain.ErrInsufficient) {
			code = "REVERSAL_INSUFFICIENT_BALANCE"
		}
		if errors.Is(err, domain.ErrOverflow) {
			code = "BALANCE_OVERFLOW"
		}
		return s.reject(ctx, tx, pending, code, now, true)
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

	if err := s.balanceEvent(ctx, tx, pending, movement, walletAfter.Version, now); err != nil {
		return err
	}
	eventPayload, err := json.Marshal(wagerProcessedEvent{
		TransactionID:         pending.ID(),
		WalletID:              pending.WalletID(),
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
