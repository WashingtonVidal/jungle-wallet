package domain

import (
	"errors"
	"testing"
	"time"
)

func newTestWagerTransaction(t *testing.T) *WagerTransaction {
	t.Helper()

	money, err := Parse("25.00", "BRL")
	if err != nil {
		t.Fatalf("unexpected money error: %v", err)
	}

	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	transaction, err := NewWagerTransaction(NewWagerTransactionParams{
		ID:                    "transaction-id",
		ExternalTransactionID: "external-123",
		ProviderID:            "provider-a",
		IdempotencyKey:        "provider-a:external-123",
		PayloadHash:           "payload-hash",
		WalletID:              "wallet-id",
		PlayerID:              "player-id",
		RoundID:               "round-id",
		GameID:                "game-id",
		Kind:                  WagerTransactionKindBet,
		Money:                 money,
		CreatedAt:             now,
	})
	if err != nil {
		t.Fatalf("unexpected transaction error: %v", err)
	}

	return transaction
}

func TestNewWagerTransactionStartsPending(t *testing.T) {
	transaction := newTestWagerTransaction(t)

	if transaction.Status() != WagerTransactionStatusPending {
		t.Fatalf("expected PENDING, got %s", transaction.Status())
	}

	if transaction.Kind() != WagerTransactionKindBet {
		t.Fatalf("expected BET, got %s", transaction.Kind())
	}
}

func TestNewWagerTransactionRejectsOpening(t *testing.T) {
	money, err := Parse("100.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewWagerTransaction(NewWagerTransactionParams{
		ID:                    "transaction-id",
		ExternalTransactionID: "external-id",
		ProviderID:            "provider-a",
		IdempotencyKey:        "key",
		PayloadHash:           "hash",
		WalletID:              "wallet-id",
		PlayerID:              "player-id",
		RoundID:               "round-id",
		GameID:                "game-id",
		Kind:                  WagerTransactionKindOpening,
		Money:                 money,
		CreatedAt:             time.Now().UTC(),
	})

	if !errors.Is(err, ErrInvalidTransactionKind) {
		t.Fatalf("expected ErrInvalidTransactionKind, got %v", err)
	}
}

func TestWagerTransactionCanBecomePendingReference(t *testing.T) {
	transaction := newTestWagerTransaction(t)

	err := transaction.MarkPendingReference(time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transaction.Status() != WagerTransactionStatusPendingReference {
		t.Fatalf(
			"expected PENDING_REFERENCE, got %s",
			transaction.Status(),
		)
	}
}

func TestWagerTransactionCanResolveReference(t *testing.T) {
	transaction := newTestWagerTransaction(t)

	now := time.Now().UTC()

	if err := transaction.MarkPendingReference(now); err != nil {
		t.Fatal(err)
	}

	if err := transaction.ResolveReference("referenced-transaction-id", now.Add(time.Second)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transaction.ReferenceTransactionID() != "referenced-transaction-id" {
		t.Fatalf(
			"unexpected reference: %s",
			transaction.ReferenceTransactionID(),
		)
	}

	if transaction.Status() != WagerTransactionStatusPending {
		t.Fatalf("expected PENDING, got %s", transaction.Status())
	}
}

func TestWagerTransactionCanBeProcessed(t *testing.T) {
	transaction := newTestWagerTransaction(t)

	balance, err := Parse("75.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	err = transaction.MarkProcessed(balance, time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transaction.Status() != WagerTransactionStatusProcessed {
		t.Fatalf("expected PROCESSED, got %s", transaction.Status())
	}

	if transaction.ResultBalance() == nil {
		t.Fatal("expected persisted result balance")
	}

	if transaction.ResultBalance().Amount() != "75.00" {
		t.Fatalf(
			"expected result balance 75.00, got %s",
			transaction.ResultBalance().Amount(),
		)
	}
}

func TestWagerTransactionCanBeRejected(t *testing.T) {
	transaction := newTestWagerTransaction(t)

	err := transaction.Reject("INSUFFICIENT_FUNDS", time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transaction.Status() != WagerTransactionStatusRejected {
		t.Fatalf("expected REJECTED, got %s", transaction.Status())
	}

	if transaction.FailureCode() != "INSUFFICIENT_FUNDS" {
		t.Fatalf(
			"unexpected failure code: %s",
			transaction.FailureCode(),
		)
	}
}

func TestRejectRequiresFailureCode(t *testing.T) {
	transaction := newTestWagerTransaction(t)

	err := transaction.Reject("", time.Now().UTC())

	if !errors.Is(err, ErrFailureCodeRequired) {
		t.Fatalf("expected ErrFailureCodeRequired, got %v", err)
	}
}

func TestTerminalTransactionCannotTransitionAgain(t *testing.T) {
	transaction := newTestWagerTransaction(t)

	balance, err := Parse("75.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	if err := transaction.MarkProcessed(balance, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	err = transaction.Reject(
		"SOME_FAILURE",
		time.Now().UTC().Add(time.Second),
	)

	if !errors.Is(err, ErrTerminalTransaction) {
		t.Fatalf("expected ErrTerminalTransaction, got %v", err)
	}
}

func TestRehydrateDoesNotReapplyTransition(t *testing.T) {
	money, err := Parse("25.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	resultBalance, err := Parse("75.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()

	transaction, err := RehydrateWagerTransaction(
		RehydrateWagerTransactionParams{
			ID:                    "transaction-id",
			ExternalTransactionID: "external-id",
			ProviderID:            "provider-a",
			IdempotencyKey:        "key",
			PayloadHash:           "hash",
			WalletID:              "wallet-id",
			PlayerID:              "player-id",
			RoundID:               "round-id",
			GameID:                "game-id",
			Kind:                  WagerTransactionKindBet,
			Money:                 money,
			Status:                WagerTransactionStatusProcessed,
			ResultBalance:         &resultBalance,
			CreatedAt:             now,
			UpdatedAt:             now,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transaction.Status() != WagerTransactionStatusProcessed {
		t.Fatalf(
			"expected PROCESSED, got %s",
			transaction.Status(),
		)
	}

	if transaction.ResultBalance() == nil {
		t.Fatal("expected result balance")
	}
}

func TestNewWagerTransactionValidatesMoneyByKind(t *testing.T) {
	tests := []struct {
		name      string
		kind      WagerTransactionKind
		amount    string
		reference string
		wantError bool
	}{
		{
			name:      "BET requires positive amount",
			kind:      WagerTransactionKindBet,
			amount:    "0.00",
			wantError: true,
		},
		{
			name:      "WIN requires positive amount",
			kind:      WagerTransactionKindWin,
			amount:    "0.00",
			wantError: true,
		},
		{
			name:      "LOSS accepts zero",
			kind:      WagerTransactionKindLoss,
			amount:    "0.00",
			wantError: false,
		},
		{
			name:      "LOSS rejects positive amount",
			kind:      WagerTransactionKindLoss,
			amount:    "1.00",
			wantError: true,
		},
		{
			name:      "REFUND requires reference",
			kind:      WagerTransactionKindRefund,
			amount:    "25.00",
			wantError: true,
		},
		{
			name:      "REFUND accepts reference",
			kind:      WagerTransactionKindRefund,
			amount:    "25.00",
			reference: "original-bet",
			wantError: false,
		},
		{
			name:      "ROLLBACK requires reference",
			kind:      WagerTransactionKindRollback,
			amount:    "25.00",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := Parse(tt.amount, "BRL")
			if err != nil {
				t.Fatal(err)
			}

			_, err = NewWagerTransaction(NewWagerTransactionParams{
				ID:                             "transaction-id",
				ExternalTransactionID:          "external-id",
				ProviderID:                     "provider-a",
				IdempotencyKey:                 "provider-a:external-id",
				PayloadHash:                    "hash",
				WalletID:                       "wallet-id",
				PlayerID:                       "player-id",
				RoundID:                        "round-id",
				GameID:                         "game-id",
				Kind:                           tt.kind,
				Money:                          money,
				ReferenceExternalTransactionID: tt.reference,
				CreatedAt:                      time.Now().UTC(),
			})

			if tt.wantError && err == nil {
				t.Fatal("expected error, got nil")
			}

			if !tt.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
