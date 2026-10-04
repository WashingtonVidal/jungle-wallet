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
		WalletID:              "wallet-1",
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
		WalletID:              "wallet-idempotency",
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
		WalletID:              "wallet-concurrent",
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
		WalletID:              "wallet-concurrent",
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

func TestProcessWagerSameBetFiftyTimesConcurrently(t *testing.T) {
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
		"wallet-concurrent-idempotency",
		"player-concurrent-idempotency",
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
		ID:                    "transaction-concurrent-idempotency",
		LedgerID:              "ledger-concurrent-idempotency",
		WalletID:              "wallet-concurrent-idempotency",
		ExternalTransactionID: "external-concurrent-idempotency",
		ProviderID:            "provider-concurrent-idempotency",
		IdempotencyKey:        "idempotency-concurrent-idempotency",
		PayloadHash:           "payload-concurrent-idempotency",
		PlayerID:              "player-concurrent-idempotency",
		RoundID:               "round-concurrent-idempotency",
		GameID:                "game-concurrent-idempotency",
		Kind:                  domain.WagerTransactionKindBet,
		Money:                 betAmount,
		CreatedAt:             now.Add(time.Second),
	}

	const requestCount = 50

	start := make(chan struct{})
	resultsCh := make(chan *ProcessWagerResult, requestCount)
	errorsCh := make(chan error, requestCount)

	run := func() {
		<-start

		result, err := service.Execute(ctx, input)

		resultsCh <- result
		errorsCh <- err
	}

	for i := 0; i < requestCount; i++ {
		go run()
	}

	close(start)

	successes := 0
	replays := 0

	for i := 0; i < requestCount; i++ {
		result := <-resultsCh
		processErr := <-errorsCh

		if processErr != nil {
			t.Fatalf("concurrent request failed: %v", processErr)
		}

		successes++

		if result.Replay {
			replays++
		}
	}

	if successes != requestCount {
		t.Fatalf(
			"expected %d successful requests, got %d",
			requestCount,
			successes,
		)
	}

	if replays != requestCount-1 {
		t.Fatalf(
			"expected %d idempotent replays, got %d",
			requestCount-1,
			replays,
		)
	}

	var balanceUnits int64

	err = db.Pool.QueryRow(
		ctx,
		`SELECT balance_units FROM wallets WHERE id = $1`,
		"wallet-concurrent-idempotency",
	).Scan(&balanceUnits)
	if err != nil {
		t.Fatalf("query wallet: %v", err)
	}

	if balanceUnits != 7500 {
		t.Fatalf(
			"expected final balance 7500, got %d",
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
			"expected exactly 1 transaction, got %d",
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

func TestProcessWagerDifferentWalletsConcurrently(t *testing.T) {
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

	walletA, err := domain.NewWallet(
		"wallet-parallel-a",
		"player-parallel-a",
		initialBalance,
		now,
	)
	if err != nil {
		t.Fatalf("create wallet A: %v", err)
	}

	walletB, err := domain.NewWallet(
		"wallet-parallel-b",
		"player-parallel-b",
		initialBalance,
		now,
	)
	if err != nil {
		t.Fatalf("create wallet B: %v", err)
	}

	wallets := postgres.NewWalletRepository()
	transactions := postgres.NewWagerTransactionRepository()
	ledger := postgres.NewLedgerRepository()
	outbox := postgres.NewOutboxRepository()

	if err := wallets.Insert(ctx, db.Pool, walletA); err != nil {
		t.Fatalf("insert wallet A: %v", err)
	}

	if err := wallets.Insert(ctx, db.Pool, walletB); err != nil {
		t.Fatalf("insert wallet B: %v", err)
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

	inputA := ProcessWagerInput{
		ID:                    "transaction-parallel-a",
		LedgerID:              "ledger-parallel-a",
		WalletID:              "wallet-parallel-a",
		ExternalTransactionID: "external-parallel-a",
		ProviderID:            "provider-parallel",
		IdempotencyKey:        "idempotency-parallel-a",
		PayloadHash:           "payload-parallel-a",
		PlayerID:              "player-parallel-a",
		RoundID:               "round-parallel-a",
		GameID:                "game-parallel-a",
		Kind:                  domain.WagerTransactionKindBet,
		Money:                 betAmount,
		CreatedAt:             now.Add(time.Second),
	}

	inputB := ProcessWagerInput{
		ID:                    "transaction-parallel-b",
		LedgerID:              "ledger-parallel-b",
		WalletID:              "wallet-parallel-b",
		ExternalTransactionID: "external-parallel-b",
		ProviderID:            "provider-parallel",
		IdempotencyKey:        "idempotency-parallel-b",
		PayloadHash:           "payload-parallel-b",
		PlayerID:              "player-parallel-b",
		RoundID:               "round-parallel-b",
		GameID:                "game-parallel-b",
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

	for i := 0; i < 2; i++ {
		if processErr := <-errorsCh; processErr != nil {
			t.Fatalf("parallel processing failed: %v", processErr)
		}
	}

	var balanceA int64

	err = db.Pool.QueryRow(
		ctx,
		`SELECT balance_units FROM wallets WHERE id = $1`,
		"wallet-parallel-a",
	).Scan(&balanceA)
	if err != nil {
		t.Fatalf("query wallet A: %v", err)
	}

	var balanceB int64

	err = db.Pool.QueryRow(
		ctx,
		`SELECT balance_units FROM wallets WHERE id = $1`,
		"wallet-parallel-b",
	).Scan(&balanceB)
	if err != nil {
		t.Fatalf("query wallet B: %v", err)
	}

	if balanceA != 7500 {
		t.Fatalf(
			"expected wallet A balance 7500, got %d",
			balanceA,
		)
	}

	if balanceB != 7500 {
		t.Fatalf(
			"expected wallet B balance 7500, got %d",
			balanceB,
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

	if transactionCount != 2 {
		t.Fatalf(
			"expected exactly 2 transactions, got %d",
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

	if ledgerCount != 2 {
		t.Fatalf(
			"expected exactly 2 ledger entries, got %d",
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

	if outboxCount != 2 {
		t.Fatalf(
			"expected exactly 2 outbox messages, got %d",
			outboxCount,
		)
	}
}
func TestProcessWagerRefundBeforeBetIsEventuallyProcessed(t *testing.T) {
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
		"wallet-refund-before-bet",
		"player-refund-before-bet",
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

	amount, err := domain.Parse("25.00", "BRL")
	if err != nil {
		t.Fatalf("create money: %v", err)
	}

	// Primeiro chega o REFUND, mas o BET referenciado ainda não existe.
	refundResult, err := service.Execute(ctx, ProcessWagerInput{
		ID:                             "refund-before-bet",
		LedgerID:                       "ledger-refund-before-bet",
		WalletID:                       "wallet-refund-before-bet",
		ExternalTransactionID:          "external-refund-before-bet",
		ProviderID:                     "provider-refund-before-bet",
		IdempotencyKey:                 "idempotency-refund-before-bet",
		PayloadHash:                    "payload-refund-before-bet",
		ReferenceExternalTransactionID: "external-original-bet",
		PlayerID:                       "player-refund-before-bet",
		RoundID:                        "round-refund-before-bet",
		GameID:                         "game-refund-before-bet",
		Kind:                           domain.WagerTransactionKindRefund,
		Money:                          amount,
		CreatedAt:                      now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("process REFUND before BET: %v", err)
	}

	if refundResult.Transaction.Status() != domain.WagerTransactionStatusPendingReference {
		t.Fatalf(
			"expected refund PENDING_REFERENCE, got %s",
			refundResult.Transaction.Status(),
		)
	}

	// Agora chega o BET original.
	_, err = service.Execute(ctx, ProcessWagerInput{
		ID:                    "original-bet",
		LedgerID:              "ledger-original-bet",
		WalletID:              "wallet-refund-before-bet",
		ExternalTransactionID: "external-original-bet",
		ProviderID:            "provider-refund-before-bet",
		IdempotencyKey:        "idempotency-original-bet",
		PayloadHash:           "payload-original-bet",
		PlayerID:              "player-refund-before-bet",
		RoundID:               "round-refund-before-bet",
		GameID:                "game-refund-before-bet",
		Kind:                  domain.WagerTransactionKindBet,
		Money:                 amount,
		CreatedAt:             now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("process original BET: %v", err)
	}

	var (
		refundStatus string
		referenceID  *string
		balanceUnits int64
		ledgerCount  int
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT status, reference_transaction_id
		FROM wager_transactions
		WHERE id = $1
		`,
		"refund-before-bet",
	).Scan(&refundStatus, &referenceID)
	if err != nil {
		t.Fatalf("query refund: %v", err)
	}

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT balance_units
		FROM wallets
		WHERE id = $1
		`,
		"wallet-refund-before-bet",
	).Scan(&balanceUnits)
	if err != nil {
		t.Fatalf("query wallet: %v", err)
	}

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM wallet_ledger_entries
		WHERE wallet_id = $1
		`,
		"wallet-refund-before-bet",
	).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("query ledger count: %v", err)
	}

	if refundStatus != "PROCESSED" {
		t.Fatalf(
			"expected refund to become PROCESSED after referenced BET arrives, got %s",
			refundStatus,
		)
	}

	if referenceID == nil || *referenceID != "original-bet" {
		t.Fatalf(
			"expected refund reference_transaction_id original-bet, got %v",
			referenceID,
		)
	}

	// 100.00 - 25.00 BET + 25.00 REFUND = 100.00
	if balanceUnits != 10000 {
		t.Fatalf(
			"expected final balance 10000, got %d",
			balanceUnits,
		)
	}

	// Um lançamento do BET + um lançamento do REFUND.
	if ledgerCount != 2 {
		t.Fatalf(
			"expected 2 ledger entries, got %d",
			ledgerCount,
		)
	}
}

func TestRetryPendingReferenceProcessesExistingReference(t *testing.T) {
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

	initialBalance, err := domain.Parse("75.00", "BRL")
	if err != nil {
		t.Fatalf("create initial balance: %v", err)
	}

	wallet, err := domain.NewWallet(
		"wallet-retry-pending",
		"player-retry-pending",
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

	if err := wallets.Insert(
		ctx,
		db.Pool,
		wallet,
	); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	// Simula uma BET que já foi processada anteriormente.
	_, err = db.Pool.Exec(
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
			status,
			result_balance_units,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15, $15
		)
		`,
		"transaction-referenced-bet",
		"external-referenced-bet",
		"provider-retry-pending",
		"provider-retry-pending:external-referenced-bet",
		"payload-referenced-bet",
		"wallet-retry-pending",
		"player-retry-pending",
		"round-retry-pending",
		"game-retry-pending",
		domain.WagerTransactionKindBet,
		2500,
		"BRL",
		domain.WagerTransactionStatusProcessed,
		7500,
		now,
	)
	if err != nil {
		t.Fatalf("insert referenced BET: %v", err)
	}

	// Simula um REFUND que chegou antes da referência e ficou pendente.
	_, err = db.Pool.Exec(
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
			status,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15, $15
		)
		`,
		"transaction-pending-refund",
		"external-pending-refund",
		"provider-retry-pending",
		"provider-retry-pending:external-pending-refund",
		"payload-pending-refund",
		"wallet-retry-pending",
		"player-retry-pending",
		"round-retry-pending",
		"game-retry-pending",
		domain.WagerTransactionKindRefund,
		2500,
		"BRL",
		"external-referenced-bet",
		domain.WagerTransactionStatusPendingReference,
		now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("insert pending REFUND: %v", err)
	}

	service := NewProcessWagerService(
		db,
		wallets,
		transactions,
		ledger,
		outbox,
	)

	processed, err := service.RetryPendingReference(
		ctx,
		30,
	)
	if err != nil {
		t.Fatalf("retry pending reference: %v", err)
	}

	if !processed {
		t.Fatal("expected pending reference to be processed")
	}

	var (
		status                 string
		referenceTransactionID string
		resultBalanceUnits     int64
		attempts               int
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT
			status,
			COALESCE(reference_transaction_id, ''),
			result_balance_units,
			reference_attempts
		FROM wager_transactions
		WHERE id = $1
		`,
		"transaction-pending-refund",
	).Scan(
		&status,
		&referenceTransactionID,
		&resultBalanceUnits,
		&attempts,
	)
	if err != nil {
		t.Fatalf("query processed REFUND: %v", err)
	}

	if status != string(domain.WagerTransactionStatusProcessed) {
		t.Fatalf(
			"expected REFUND status PROCESSED, got %s",
			status,
		)
	}

	if referenceTransactionID != "transaction-referenced-bet" {
		t.Fatalf(
			"expected reference transaction %s, got %s",
			"transaction-referenced-bet",
			referenceTransactionID,
		)
	}

	if resultBalanceUnits != 10000 {
		t.Fatalf(
			"expected result balance 10000, got %d",
			resultBalanceUnits,
		)
	}

	if attempts != 1 {
		t.Fatalf(
			"expected exactly 1 reference attempt, got %d",
			attempts,
		)
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
		"wallet-retry-pending",
	).Scan(
		&balanceUnits,
		&version,
	)
	if err != nil {
		t.Fatalf("query wallet: %v", err)
	}

	if balanceUnits != 10000 {
		t.Fatalf(
			"expected wallet balance 10000, got %d",
			balanceUnits,
		)
	}

	if version != 2 {
		t.Fatalf(
			"expected wallet version 2, got %d",
			version,
		)
	}

	var ledgerCount int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM wallet_ledger_entries
		WHERE transaction_id = $1
		`,
		"transaction-pending-refund",
	).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("count ledger entries: %v", err)
	}

	if ledgerCount != 1 {
		t.Fatalf(
			"expected exactly 1 REFUND ledger entry, got %d",
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
		  AND event_type = 'WAGER_TRANSACTION_PROCESSED'
		`,
		"transaction-pending-refund",
	).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("count outbox messages: %v", err)
	}

	if outboxCount != 1 {
		t.Fatalf(
			"expected exactly 1 processed outbox message, got %d",
			outboxCount,
		)
	}
}

func TestRetryPendingReferenceRejectsAfterMaxAttempts(t *testing.T) {
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

	wallet, err := domain.NewWallet(
		"wallet-max-attempts",
		"player-max-attempts",
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

	if err := wallets.Insert(
		ctx,
		db.Pool,
		wallet,
	); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	_, err = db.Pool.Exec(
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
			status,
			reference_attempts,
			reference_next_attempt_at,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15,
			NULL, $16, $16
		)
		`,
		"transaction-max-attempts",
		"refund-max-attempts",
		"provider-max-attempts",
		"provider-max-attempts:refund-max-attempts",
		"payload-max-attempts",
		"wallet-max-attempts",
		"player-max-attempts",
		"round-max-attempts",
		"game-max-attempts",
		domain.WagerTransactionKindRefund,
		2500,
		"BRL",
		"bet-that-never-arrived",
		domain.WagerTransactionStatusPendingReference,
		5,
		now,
	)
	if err != nil {
		t.Fatalf("insert pending REFUND: %v", err)
	}

	service := NewProcessWagerService(
		db,
		wallets,
		transactions,
		ledger,
		outbox,
	)

	processed, err := service.RetryPendingReference(
		ctx,
		30,
	)
	if err != nil {
		t.Fatalf("retry pending reference: %v", err)
	}

	if processed {
		t.Fatal(
			"expected missing reference not to be processed",
		)
	}

	var (
		status      string
		failureCode string
		attempts    int
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT
			status,
			COALESCE(failure_code, ''),
			reference_attempts
		FROM wager_transactions
		WHERE id = $1
		`,
		"transaction-max-attempts",
	).Scan(
		&status,
		&failureCode,
		&attempts,
	)
	if err != nil {
		t.Fatalf("query rejected REFUND: %v", err)
	}

	if attempts != 6 {
		t.Fatalf(
			"expected 6 reference attempts, got %d",
			attempts,
		)
	}

	if status != string(domain.WagerTransactionStatusRejected) {
		t.Fatalf(
			"expected status REJECTED, got %s",
			status,
		)
	}

	if failureCode != "REFERENCE_NOT_FOUND" {
		t.Fatalf(
			"expected failure code REFERENCE_NOT_FOUND, got %s",
			failureCode,
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
		"wallet-max-attempts",
	).Scan(&balanceUnits)
	if err != nil {
		t.Fatalf("query wallet: %v", err)
	}

	if balanceUnits != 10000 {
		t.Fatalf(
			"expected wallet balance to remain 10000, got %d",
			balanceUnits,
		)
	}

	var ledgerCount int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM wallet_ledger_entries
		WHERE transaction_id = $1
		`,
		"transaction-max-attempts",
	).Scan(&ledgerCount)
	if err != nil {
		t.Fatalf("count ledger entries: %v", err)
	}

	if ledgerCount != 0 {
		t.Fatalf(
			"expected no ledger entry, got %d",
			ledgerCount,
		)
	}
}

func TestRetryPendingReferenceSchedulesBackoffWhenReferenceIsMissing(
	t *testing.T,
) {
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

	wallet, err := domain.NewWallet(
		"wallet-backoff",
		"player-backoff",
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

	if err := wallets.Insert(
		ctx,
		db.Pool,
		wallet,
	); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	_, err = db.Pool.Exec(
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
			status,
			reference_attempts,
			reference_next_attempt_at,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, 0,
			NULL, $15, $15
		)
		`,
		"transaction-backoff",
		"refund-backoff",
		"provider-backoff",
		"provider-backoff:refund-backoff",
		"payload-backoff",
		"wallet-backoff",
		"player-backoff",
		"round-backoff",
		"game-backoff",
		domain.WagerTransactionKindRefund,
		2500,
		"BRL",
		"bet-that-does-not-exist",
		domain.WagerTransactionStatusPendingReference,
		now,
	)
	if err != nil {
		t.Fatalf("insert pending REFUND: %v", err)
	}

	service := NewProcessWagerService(
		db,
		wallets,
		transactions,
		ledger,
		outbox,
	)

	beforeRetry := time.Now().UTC()

	processed, err := service.RetryPendingReference(
		ctx,
		30,
	)
	if err != nil {
		t.Fatalf("retry pending reference: %v", err)
	}

	afterRetry := time.Now().UTC()

	if processed {
		t.Fatal(
			"expected missing reference not to be processed",
		)
	}

	var (
		status        string
		attempts      int
		nextAttemptAt time.Time
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT
			status,
			reference_attempts,
			reference_next_attempt_at
		FROM wager_transactions
		WHERE id = $1
		`,
		"transaction-backoff",
	).Scan(
		&status,
		&attempts,
		&nextAttemptAt,
	)
	if err != nil {
		t.Fatalf("query pending REFUND: %v", err)
	}

	if status != string(
		domain.WagerTransactionStatusPendingReference,
	) {
		t.Fatalf(
			"expected status PENDING_REFERENCE, got %s",
			status,
		)
	}

	if attempts != 1 {
		t.Fatalf(
			"expected 1 reference attempt, got %d",
			attempts,
		)
	}

	earliestExpected := beforeRetry.Add(time.Second)
	latestExpected := afterRetry.Add(time.Second)

	if nextAttemptAt.Before(earliestExpected) {
		t.Fatalf(
			"expected next attempt at or after %s, got %s",
			earliestExpected,
			nextAttemptAt,
		)
	}

	if nextAttemptAt.After(latestExpected) {
		t.Fatalf(
			"expected next attempt at or before %s, got %s",
			latestExpected,
			nextAttemptAt,
		)
	}
}
