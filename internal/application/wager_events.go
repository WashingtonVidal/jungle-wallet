package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/postgres"
)

type balanceChangedEvent struct {
	WalletID      string                 `json:"walletId"`
	TransactionID string                 `json:"transactionId"`
	Direction     domain.LedgerDirection `json:"direction"`
	Money         domain.Money           `json:"money"`
	BalanceBefore domain.Money           `json:"balanceBefore"`
	BalanceAfter  domain.Money           `json:"balanceAfter"`
	WalletVersion int64                  `json:"walletVersion"`
}
type wagerStatusEvent struct {
	TransactionID                  string                        `json:"transactionId"`
	WalletID                       string                        `json:"walletId"`
	ProviderID                     string                        `json:"providerId,omitempty"`
	Status                         domain.WagerTransactionStatus `json:"status"`
	FailureCode                    string                        `json:"failureCode,omitempty"`
	ReferenceExternalTransactionID string                        `json:"referenceExternalTransactionId,omitempty"`
}

func insertEvent(ctx context.Context, q postgres.Querier, outbox *postgres.OutboxRepository, aggregateType, aggregateID, eventType string, data any, now time.Time) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return outbox.Insert(ctx, q, postgres.OutboxMessage{ID: uuid.NewString(), AggregateType: aggregateType, AggregateID: aggregateID, EventType: eventType, Payload: payload, AvailableAt: now.UTC(), CreatedAt: now.UTC()})
}
func (s *ProcessWagerService) statusEvent(ctx context.Context, tx pgx.Tx, t *domain.WagerTransaction, eventType string, now time.Time) error {
	return insertEvent(ctx, tx, s.outbox, "WAGER_TRANSACTION", t.ID(), eventType, wagerStatusEvent{TransactionID: t.ID(), WalletID: t.WalletID(), ProviderID: t.ProviderID(), Status: t.Status(), FailureCode: t.FailureCode(), ReferenceExternalTransactionID: t.ReferenceExternalTransactionID()}, now)
}
func (s *ProcessWagerService) reject(ctx context.Context, tx pgx.Tx, t *domain.WagerTransaction, code string, now time.Time, persisted bool) error {
	if err := t.Reject(code, now); err != nil {
		return err
	}
	var err error
	if persisted {
		err = s.transactions.Update(ctx, tx, t)
	} else {
		err = s.transactions.Insert(ctx, tx, t)
	}
	if err != nil {
		return err
	}
	return s.statusEvent(ctx, tx, t, "WAGER_TRANSACTION_REJECTED", now)
}
func (s *ProcessWagerService) validateReference(ctx context.Context, tx pgx.Tx, t, ref *domain.WagerTransaction) (string, error) {
	if ref.ProviderID() != t.ProviderID() || ref.PlayerID() != t.PlayerID() || ref.WalletID() != t.WalletID() || ref.RoundID() != t.RoundID() || ref.Money().Currency() != t.Money().Currency() {
		return "REFERENCE_MISMATCH", nil
	}
	if ref.Status() != domain.WagerTransactionStatusProcessed {
		if ref.Status().IsTerminal() {
			return "REFERENCE_REJECTED", nil
		}
		return "REFERENCE_PENDING", nil
	}
	if t.Kind() == domain.WagerTransactionKindWin {
		if ref.Kind() != domain.WagerTransactionKindBet {
			return "INVALID_REFERENCE_KIND", nil
		}
		return "", nil
	}
	if ref.Money().Units() != t.Money().Units() {
		return "REVERSAL_AMOUNT_MISMATCH", nil
	}
	if t.Kind() == domain.WagerTransactionKindRefund && ref.Kind() != domain.WagerTransactionKindBet {
		return "INVALID_REFERENCE_KIND", nil
	}
	if t.Kind() == domain.WagerTransactionKindRollback && ref.Kind() != domain.WagerTransactionKindBet && ref.Kind() != domain.WagerTransactionKindWin && ref.Kind() != domain.WagerTransactionKindRefund {
		return "INVALID_REFERENCE_KIND", nil
	}
	var exists bool
	// The wallet lock serializes all reversals. Both REFUND and ROLLBACK
	// against a BET compete for the same debit; refund rollback is explicit.
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wager_transactions WHERE reference_transaction_id=$1 AND status='PROCESSED' AND (kind=$2 OR $3='BET'))`, ref.ID(), t.Kind(), ref.Kind()).Scan(&exists)
	if err != nil {
		return "", fmt.Errorf("check prior reversal: %w", err)
	}
	if exists {
		return "ALREADY_REVERSED", nil
	}
	return "", nil
}
func (s *ProcessWagerService) balanceEvent(ctx context.Context, tx pgx.Tx, t *domain.WagerTransaction, movement *domain.WagerMovement, version int64, now time.Time) error {
	return insertEvent(ctx, tx, s.outbox, "WALLET", t.WalletID(), "WALLET_BALANCE_CHANGED", balanceChangedEvent{WalletID: t.WalletID(), TransactionID: t.ID(), Direction: movement.Direction, Money: t.Money(), BalanceBefore: movement.BalanceBefore, BalanceAfter: movement.BalanceAfter, WalletVersion: version}, now)
}
