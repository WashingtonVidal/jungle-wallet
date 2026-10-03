package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/postgres"
)

const testDatabaseURL = "postgres://jungle:jungle@localhost:5432/jungle_wallet?sslmode=disable"

func TestProcessWagerBETIntegration(t *testing.T) {
	ctx := context.Background()

	db, err := postgres.New(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	// Keep this test deterministic even when it is executed repeatedly.
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
		t.Fatalf("create initial money: %v", err)
	}

	wallet, err := domain.NewWallet(
		"wallet-1",
		"player-1",
		initialBalance,
		now,
	)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	wallets := postgres.NewWalletRepository()
	transactions := postgres.NewWagerTransactionRepository()
	ledger := postgres.NewLedgerRepository()
	outbox := postgres.NewOutboxRepository()

	if err := wallets.Insert(ctx, db.Pool, wallet); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	service := NewProcessWagerService(
		db,
		wallets,
		transactions,
		ledger,
		outbox,
	)

	betAmount, err := domain.Parse("25.00", "BRL")
	if err != nil {
		t.Fatalf("create bet money: %v", err)
	}

	result, err := service.Execute(ctx, ProcessWagerInput{
		ID:                    "transaction-1",
		LedgerID:              "ledger-1",
		ExternalTransactionID: "external-1",
		ProviderID:            "provider-1",
		IdempotencyKey:        "idempotency-1",
		PayloadHash:           "payload-hash-1",
		PlayerID:              "player-1",
		RoundID:               "round-1",
		GameID:                "game-1",
		Kind:                  domain.WagerTransactionKindBet,
		Money:                 betAmount,
		CreatedAt:             now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("process BET: %v", err)
	}

	if result.Replay {
		t.Fatal("first request must not be a replay")
	}

	var (
		balanceUnits int64
		version      int64
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT balance_units, version
		FROM wallets
		WHERE id = $1
		`,
		"wallet-1",
	).Scan(&balanceUnits, &version)
	if err != nil {
		t.Fatalf("query wallet: %v", err)
	}

	if balanceUnits != 7500 {
		t.Fatalf(
			"expected wallet balance 7500, got %d",
			balanceUnits,
		)
	}

	if version != 2 {
		t.Fatalf(
			"expected wallet version 2, got %d",
			version,
		)
	}

	var (
		status        string
		resultBalance int64
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT status, result_balance_units
		FROM wager_transactions
		WHERE id = $1
		`,
		"transaction-1",
	).Scan(&status, &resultBalance)
	if err != nil {
		t.Fatalf("query wager transaction: %v", err)
	}

	if status != "PROCESSED" {
		t.Fatalf(
			"expected transaction status PROCESSED, got %s",
			status,
		)
	}

	if resultBalance != 7500 {
		t.Fatalf(
			"expected result balance 7500, got %d",
			resultBalance,
		)
	}

	var (
		direction     string
		amountUnits   int64
		balanceBefore int64
		balanceAfter  int64
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT
			direction,
			amount_units,
			balance_before_units,
			balance_after_units
		FROM wallet_ledger_entries
		WHERE id = $1
		`,
		"ledger-1",
	).Scan(
		&direction,
		&amountUnits,
		&balanceBefore,
		&balanceAfter,
	)
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}

	if direction != "DEBIT" {
		t.Fatalf("expected DEBIT, got %s", direction)
	}

	if amountUnits != 2500 {
		t.Fatalf("expected ledger amount 2500, got %d", amountUnits)
	}

	if balanceBefore != 10000 || balanceAfter != 7500 {
		t.Fatalf(
			"expected ledger 10000 -> 7500, got %d -> %d",
			balanceBefore,
			balanceAfter,
		)
	}

	var outboxCount int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM outbox
		WHERE aggregate_id = $1
		  AND event_type = $2
		`,
		"transaction-1",
		"WAGER_TRANSACTION_PROCESSED",
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}

	if outboxCount != 1 {
		t.Fatalf(
			"expected exactly 1 outbox event, got %d",
			outboxCount,
		)
	}
}

func TestProcessWagerBETIdempotentReplay(t *testing.T) {
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
		t.Fatalf("create initial money: %v", err)
	}

	wallet, err := domain.NewWallet(
		"wallet-idempotency",
		"player-idempotency",
		initialBalance,
		now,
	)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	wallets := postgres.NewWalletRepository()
	transactions := postgres.NewWagerTransactionRepository()
	ledger := postgres.NewLedgerRepository()
	outbox := postgres.NewOutboxRepository()

	if err := wallets.Insert(ctx, db.Pool, wallet); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	service := NewProcessWagerService(
		db,
		wallets,
		transactions,
		ledger,
		outbox,
	)

	betAmount, err := domain.Parse("25.00", "BRL")
	if err != nil {
		t.Fatalf("create bet money: %v", err)
	}

	input := ProcessWagerInput{
		ID:                    "transaction-idempotency",
		LedgerID:              "ledger-idempotency",
		ExternalTransactionID: "external-idempotency",
		ProviderID:            "provider-idempotency",
		IdempotencyKey:        "idempotency-key-1",
		PayloadHash:           "same-payload",
		PlayerID:              "player-idempotency",
		RoundID:               "round-idempotency",
		GameID:                "game-idempotency",
		Kind:                  domain.WagerTransactionKindBet,
		Money:                 betAmount,
		CreatedAt:             now.Add(time.Second),
	}

	first, err := service.Execute(ctx, input)
	if err != nil {
		t.Fatalf("first BET: %v", err)
	}

	if first.Replay {
		t.Fatal("first request must not be replay")
	}

	second, err := service.Execute(ctx, input)
	if err != nil {
		t.Fatalf("replayed BET: %v", err)
	}

	if !second.Replay {
		t.Fatal("second identical request must be replay")
	}

	var balanceUnits int64

	err = db.Pool.QueryRow(
		ctx,
		`SELECT balance_units FROM wallets WHERE id = $1`,
		"wallet-idempotency",
	).Scan(&balanceUnits)
	if err != nil {
		t.Fatalf("query wallet: %v", err)
	}

	if balanceUnits != 7500 {
		t.Fatalf(
			"expected balance to remain 7500 after replay, got %d",
			balanceUnits,
		)
	}

	var transactionCount int
	err = db.Pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM wager_transactions`,
	).Scan(&transactionCount)
	if err != nil {
		t.Fatalf("count transactions: %v", err)
	}

	if transactionCount != 1 {
		t.Fatalf(
			"expected 1 transaction after replay, got %d",
			transactionCount,
		)
	}

	var ledgerCount int
	err = db.Pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM wallet_ledger_entries`,
	).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("count ledger entries: %v", err)
	}

	if ledgerCount != 1 {
		t.Fatalf(
			"expected 1 ledger entry after replay, got %d",
			ledgerCount,
		)
	}

	var outboxCount int
	err = db.Pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM outbox`,
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("count outbox messages: %v", err)
	}

	if outboxCount != 1 {
		t.Fatalf(
			"expected 1 outbox message after replay, got %d",
			outboxCount,
		)
	}
}

func TestProcessWagerConcurrentBetsDoNotOverspend(t *testing.T) {
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
		t.Fatalf("create initial money: %v", err)
	}

	wallet, err := domain.NewWallet(
		"wallet-concurrent",
		"player-concurrent",
		initialBalance,
		now,
	)
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	wallets := postgres.NewWalletRepository()
	transactions := postgres.NewWagerTransactionRepository()
	ledger := postgres.NewLedgerRepository()
	outbox := postgres.NewOutboxRepository()

	if err := wallets.Insert(ctx, db.Pool, wallet); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	service := NewProcessWagerService(
		db,
		wallets,
		transactions,
		ledger,
		outbox,
	)

	betAmount, err := domain.Parse("80.00", "BRL")
	if err != nil {
		t.Fatalf("create bet money: %v", err)
	}

	inputA := ProcessWagerInput{
		ID:                    "transaction-concurrent-a",
		LedgerID:              "ledger-concurrent-a",
		ExternalTransactionID: "external-concurrent-a",
		ProviderID:            "provider-concurrent",
		IdempotencyKey:        "idempotency-concurrent-a",
		PayloadHash:           "payload-concurrent-a",
		PlayerID:              "player-concurrent",
		RoundID:               "round-concurrent",
		GameID:                "game-concurrent",
		Kind:                  domain.WagerTransactionKindBet,
		Money:                 betAmount,
		CreatedAt:             now.Add(time.Second),
	}

	inputB := ProcessWagerInput{
		ID:                    "transaction-concurrent-b",
		LedgerID:              "ledger-concurrent-b",
		ExternalTransactionID: "external-concurrent-b",
		ProviderID:            "provider-concurrent",
		IdempotencyKey:        "idempotency-concurrent-b",
		PayloadHash:           "payload-concurrent-b",
		PlayerID:              "player-concurrent",
		RoundID:               "round-concurrent",
		GameID:                "game-concurrent",
		Kind:                  domain.WagerTransactionKindBet,
		Money:                 betAmount,
		CreatedAt:             now.Add(time.Second),
	}

	start := make(chan struct{})
	errorsCh := make(chan error, 2)

	run := func(input ProcessWagerInput) {
		<-start

		_, err := service.Execute(ctx, input)
		errorsCh <- err
	}

	go run(inputA)
	go run(inputB)

	close(start)

	errA := <-errorsCh
	errB := <-errorsCh

	successes := 0
	insufficient := 0

	for _, processErr := range []error{errA, errB} {
		switch {
		case processErr == nil:
			successes++

		case errors.Is(processErr, domain.ErrInsufficient):
			insufficient++

		default:
			t.Fatalf(
				"unexpected concurrent processing error: %v",
				processErr,
			)
		}
	}

	if successes != 1 {
		t.Fatalf(
			"expected exactly 1 successful BET, got %d",
			successes,
		)
	}

	if insufficient != 1 {
		t.Fatalf(
			"expected exactly 1 insufficient balance error, got %d",
			insufficient,
		)
	}

	var balanceUnits int64

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT balance_units
		FROM wallets
		WHERE id = $1
		`,
		"wallet-concurrent",
	).Scan(&balanceUnits)
	if err != nil {
		t.Fatalf("query wallet: %v", err)
	}

	if balanceUnits != 2000 {
		t.Fatalf(
			"expected final balance 2000, got %d",
			balanceUnits,
		)
	}

	var transactionCount int

	err = db.Pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM wager_transactions`,
	).Scan(&transactionCount)
	if err != nil {
		t.Fatalf("count transactions: %v", err)
	}

	if transactionCount != 1 {
		t.Fatalf(
			"expected exactly 1 persisted transaction, got %d",
			transactionCount,
		)
	}

	var ledgerCount int

	err = db.Pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM wallet_ledger_entries`,
	).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("count ledger entries: %v", err)
	}

	if ledgerCount != 1 {
		t.Fatalf(
			"expected exactly 1 ledger entry, got %d",
			ledgerCount,
		)
	}

	var outboxCount int

	err = db.Pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM outbox`,
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("count outbox messages: %v", err)
	}

	if outboxCount != 1 {
		t.Fatalf(
			"expected exactly 1 outbox message, got %d",
			outboxCount,
		)
	}
}
