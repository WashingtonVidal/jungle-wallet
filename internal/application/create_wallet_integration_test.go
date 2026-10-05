package application

import (
	"context"
	"testing"
	"time"

	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/postgres"
)

func TestCreateWalletWithInitialBalanceIntegration(t *testing.T) {
	ctx := context.Background()

	db, err := postgres.New(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	_, err = db.Pool.Exec(ctx, `
		TRUNCATE TABLE
			outbox,
			inbox,
			wallet_ledger_entries,
			wager_transactions,
			wallets
		RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatalf("clean database: %v", err)
	}

	now := time.Now().UTC()

	initialBalance, err := domain.Parse("100.00", "BRL")
	if err != nil {
		t.Fatalf("create initial balance: %v", err)
	}

	service := NewCreateWalletService(
		db,
		postgres.NewWalletRepository(),
		postgres.NewWagerTransactionRepository(),
		postgres.NewLedgerRepository(),
		postgres.NewOutboxRepository(),
	)

	result, err := service.Execute(ctx, CreateWalletInput{
		ID:             "wallet-create-1",
		PlayerID:       "player-create-1",
		InitialBalance: initialBalance,
		CreatedAt:      now,
	})
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	if result == nil || result.Wallet == nil {
		t.Fatal("expected created wallet")
	}

	var walletCount int

	err = db.Pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM wallets WHERE id = $1`,
		"wallet-create-1",
	).Scan(&walletCount)
	if err != nil {
		t.Fatalf("count wallets: %v", err)
	}

	if walletCount != 1 {
		t.Fatalf("expected 1 wallet, got %d", walletCount)
	}

	var (
		transactionCount int
		kind             string
		status           string
		amountUnits      int64
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT
			COUNT(*),
			MIN(kind),
			MIN(status),
			MIN(amount_units)
		FROM wager_transactions
		WHERE wallet_id = $1
		`,
		"wallet-create-1",
	).Scan(
		&transactionCount,
		&kind,
		&status,
		&amountUnits,
	)
	if err != nil {
		t.Fatalf("query opening transaction: %v", err)
	}

	if transactionCount != 1 {
		t.Fatalf(
			"expected 1 opening transaction, got %d",
			transactionCount,
		)
	}

	if kind != string(domain.WagerTransactionKindOpening) {
		t.Fatalf("expected OPENING, got %s", kind)
	}

	if status != string(domain.WagerTransactionStatusProcessed) {
		t.Fatalf("expected PROCESSED, got %s", status)
	}

	if amountUnits != 10000 {
		t.Fatalf("expected opening amount 10000, got %d", amountUnits)
	}

	var ledgerCount int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM wallet_ledger_entries
		WHERE wallet_id = $1
		`,
		"wallet-create-1",
	).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("count ledger entries: %v", err)
	}

	if ledgerCount != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", ledgerCount)
	}

	var outboxCount int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM outbox
		WHERE aggregate_id = $1
		   OR aggregate_id = (
			   SELECT id
			   FROM wager_transactions
			   WHERE wallet_id = $1
			   LIMIT 1
		   )
		`,
		"wallet-create-1",
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("count outbox messages: %v", err)
	}

	if outboxCount != 2 {
		t.Fatalf("expected 2 outbox messages, got %d", outboxCount)
	}
}

func TestCreateWalletWithZeroInitialBalanceIntegration(t *testing.T) {
	ctx := context.Background()

	db, err := postgres.New(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	_, err = db.Pool.Exec(ctx, `
		TRUNCATE TABLE
			outbox,
			inbox,
			wallet_ledger_entries,
			wager_transactions,
			wallets
		RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatalf("clean database: %v", err)
	}

	now := time.Now().UTC()

	initialBalance, err := domain.Parse("0.00", "BRL")
	if err != nil {
		t.Fatalf("create initial balance: %v", err)
	}

	service := NewCreateWalletService(
		db,
		postgres.NewWalletRepository(),
		postgres.NewWagerTransactionRepository(),
		postgres.NewLedgerRepository(),
		postgres.NewOutboxRepository(),
	)

	result, err := service.Execute(ctx, CreateWalletInput{
		ID:             "wallet-create-zero",
		PlayerID:       "player-create-zero",
		InitialBalance: initialBalance,
		CreatedAt:      now,
	})
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	if result == nil || result.Wallet == nil {
		t.Fatal("expected created wallet")
	}

	var walletCount int

	err = db.Pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM wallets WHERE id = $1`,
		"wallet-create-zero",
	).Scan(&walletCount)
	if err != nil {
		t.Fatalf("count wallets: %v", err)
	}

	if walletCount != 1 {
		t.Fatalf("expected 1 wallet, got %d", walletCount)
	}

	var transactionCount int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM wager_transactions
		WHERE wallet_id = $1
		`,
		"wallet-create-zero",
	).Scan(&transactionCount)
	if err != nil {
		t.Fatalf("count transactions: %v", err)
	}

	if transactionCount != 0 {
		t.Fatalf(
			"expected 0 opening transactions, got %d",
			transactionCount,
		)
	}

	var ledgerCount int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM wallet_ledger_entries
		WHERE wallet_id = $1
		`,
		"wallet-create-zero",
	).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("count ledger entries: %v", err)
	}

	if ledgerCount != 0 {
		t.Fatalf(
			"expected 0 ledger entries, got %d",
			ledgerCount,
		)
	}

	var outboxCount int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM outbox
		WHERE aggregate_id = $1
		`,
		"wallet-create-zero",
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("count outbox messages: %v", err)
	}

	if outboxCount != 0 {
		t.Fatalf(
			"expected 0 outbox messages, got %d",
			outboxCount,
		)
	}
}
