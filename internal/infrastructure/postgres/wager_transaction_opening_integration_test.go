package postgres

import (
	"context"
	"testing"
	"time"

	"jungle-wallet/internal/domain"
)

func TestWagerTransactionRepositoryOpeningIntegration(t *testing.T) {
	ctx := context.Background()

	db, err := New(ctx, inboxTestDatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	_, err = db.Pool.Exec(
		ctx,
		`
		TRUNCATE TABLE
			outbox,
			inbox,
			wallet_ledger_entries,
			wager_transactions,
			wallets
		RESTART IDENTITY CASCADE
		`,
	)
	if err != nil {
		t.Fatalf("clean database: %v", err)
	}

	now := time.Now().UTC()

	initialBalance, err := domain.Parse("100.00", "BRL")
	if err != nil {
		t.Fatalf("create money: %v", err)
	}

	wallet, err := domain.NewWallet(
		"wallet-opening",
		"player-opening",
		initialBalance,
		now,
	)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	wallets := NewWalletRepository()

	if err := wallets.Insert(ctx, db.Pool, wallet); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	transaction, err := domain.NewOpeningTransaction(
		domain.NewOpeningTransactionParams{
			ID:        "opening-transaction-1",
			WalletID:  "wallet-opening",
			PlayerID:  "player-opening",
			Money:     initialBalance,
			CreatedAt: now,
		},
	)
	if err != nil {
		t.Fatalf("create opening transaction: %v", err)
	}

	repository := NewWagerTransactionRepository()

	if err := repository.Insert(
		ctx,
		db.Pool,
		transaction,
	); err != nil {
		t.Fatalf("insert opening transaction: %v", err)
	}

	var (
		externalTransactionID *string
		providerID            *string
		idempotencyKey        *string
		payloadHash           *string
		roundID               *string
		gameID                *string
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT
			external_transaction_id,
			provider_id,
			idempotency_key,
			payload_hash,
			round_id,
			game_id
		FROM wager_transactions
		WHERE id = $1
		`,
		transaction.ID(),
	).Scan(
		&externalTransactionID,
		&providerID,
		&idempotencyKey,
		&payloadHash,
		&roundID,
		&gameID,
	)
	if err != nil {
		t.Fatalf("query opening external fields: %v", err)
	}

	if externalTransactionID != nil ||
		providerID != nil ||
		idempotencyKey != nil ||
		payloadHash != nil ||
		roundID != nil ||
		gameID != nil {
		t.Fatal("expected OPENING external fields to be NULL")
	}

	var (
		kind               string
		status             string
		amountUnits        int64
		resultBalanceUnits int64
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT
			kind,
			status,
			amount_units,
			result_balance_units
		FROM wager_transactions
		WHERE id = $1
		`,
		transaction.ID(),
	).Scan(
		&kind,
		&status,
		&amountUnits,
		&resultBalanceUnits,
	)
	if err != nil {
		t.Fatalf("query opening transaction: %v", err)
	}

	if kind != string(domain.WagerTransactionKindOpening) {
		t.Fatalf("expected OPENING kind, got %s", kind)
	}

	if status != string(domain.WagerTransactionStatusProcessed) {
		t.Fatalf("expected PROCESSED status, got %s", status)
	}

	if amountUnits != 10000 {
		t.Fatalf("expected amount 10000, got %d", amountUnits)
	}

	if resultBalanceUnits != 10000 {
		t.Fatalf(
			"expected result balance 10000, got %d",
			resultBalanceUnits,
		)
	}
}
