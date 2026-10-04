package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"jungle-wallet/internal/domain"
)

func TestWagerTransactionRepositoryClaimNextPendingReferenceConcurrentlyIntegration(
	t *testing.T,
) {
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
			wallet_ledger_entries,
			wager_transactions,
			wallets
		CASCADE
		`,
	)
	if err != nil {
		t.Fatalf("clean database: %v", err)
	}

	now := time.Now().UTC()

	_, err = db.Pool.Exec(
		ctx,
		`
		INSERT INTO wallets (
			id,
			player_id,
			balance_units,
			currency,
			version,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		`,
		"wallet-pending-claim",
		"player-pending-claim",
		10000,
		"BRL",
		1,
		now,
	)
	if err != nil {
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
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15, $15
		)
		`,
		"transaction-pending-claim",
		"refund-pending-claim",
		"provider-pending-claim",
		"provider-pending-claim:refund-pending-claim",
		"payload-pending-claim",
		"wallet-pending-claim",
		"player-pending-claim",
		"round-pending-claim",
		"game-pending-claim",
		domain.WagerTransactionKindRefund,
		2500,
		"BRL",
		"bet-not-arrived-yet",
		domain.WagerTransactionStatusPendingReference,
		now,
	)
	if err != nil {
		t.Fatalf("insert pending transaction: %v", err)
	}

	repository := NewWagerTransactionRepository()

	const workerCount = 20

	start := make(chan struct{})
	resultsCh := make(
		chan *PendingReferenceClaim,
		workerCount,
	)
	errorsCh := make(chan error, workerCount)

	var wg sync.WaitGroup
	wg.Add(workerCount)

	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()

			<-start

			result, err := repository.ClaimNextPendingReference(
				ctx,
				db.Pool,
				30,
			)

			resultsCh <- result
			errorsCh <- err
		}()
	}

	close(start)

	wg.Wait()
	close(resultsCh)
	close(errorsCh)

	claimed := 0
	notFound := 0

	for result := range resultsCh {
		if result == nil {
			continue
		}

		claimed++

		if result.Transaction.ID() != "transaction-pending-claim" {
			t.Fatalf(
				"expected transaction-pending-claim, got %s",
				result.Transaction.ID(),
			)
		}

		if result.Attempts != 1 {
			t.Fatalf(
				"expected first claim attempt to be 1, got %d",
				result.Attempts,
			)
		}
	}

	for claimErr := range errorsCh {
		switch {
		case claimErr == nil:
			// Successful claim.

		case errors.Is(
			claimErr,
			ErrWagerTransactionNotFound,
		):
			notFound++

		default:
			t.Fatalf(
				"unexpected claim error: %v",
				claimErr,
			)
		}
	}

	if claimed != 1 {
		t.Fatalf(
			"expected exactly 1 successful claim, got %d",
			claimed,
		)
	}

	if notFound != workerCount-1 {
		t.Fatalf(
			"expected %d workers without a transaction, got %d",
			workerCount-1,
			notFound,
		)
	}

	var (
		attempts      int
		nextAttemptAt *time.Time
	)

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT
			reference_attempts,
			reference_next_attempt_at
		FROM wager_transactions
		WHERE id = $1
		`,
		"transaction-pending-claim",
	).Scan(
		&attempts,
		&nextAttemptAt,
	)
	if err != nil {
		t.Fatalf(
			"find pending transaction: %v",
			err,
		)
	}

	if attempts != 1 {
		t.Fatalf(
			"expected exactly 1 reference attempt, got %d",
			attempts,
		)
	}

	if nextAttemptAt == nil {
		t.Fatal(
			"expected reference_next_attempt_at to be set",
		)
	}

	if !nextAttemptAt.After(now) {
		t.Fatalf(
			"expected next attempt after %s, got %s",
			now,
			*nextAttemptAt,
		)
	}
}

func TestWagerTransactionRepositoryClaimNextPendingReferenceBecomesAvailableAfterLeaseIntegration(
	t *testing.T,
) {
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
			wallet_ledger_entries,
			wager_transactions,
			wallets
		CASCADE
		`,
	)
	if err != nil {
		t.Fatalf("clean database: %v", err)
	}

	now := time.Now().UTC()

	_, err = db.Pool.Exec(
		ctx,
		`
		INSERT INTO wallets (
			id,
			player_id,
			balance_units,
			currency,
			version,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		`,
		"wallet-pending-lease",
		"player-pending-lease",
		10000,
		"BRL",
		1,
		now,
	)
	if err != nil {
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
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15, $15
		)
		`,
		"transaction-pending-lease",
		"refund-pending-lease",
		"provider-pending-lease",
		"provider-pending-lease:refund-pending-lease",
		"payload-pending-lease",
		"wallet-pending-lease",
		"player-pending-lease",
		"round-pending-lease",
		"game-pending-lease",
		domain.WagerTransactionKindRefund,
		2500,
		"BRL",
		"bet-pending-lease",
		domain.WagerTransactionStatusPendingReference,
		now,
	)
	if err != nil {
		t.Fatalf("insert pending transaction: %v", err)
	}

	repository := NewWagerTransactionRepository()

	firstClaim, err := repository.ClaimNextPendingReference(
		ctx,
		db.Pool,
		1,
	)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}

	if firstClaim.Transaction.ID() != "transaction-pending-lease" {
		t.Fatalf(
			"expected first claim transaction-pending-lease, got %s",
			firstClaim.Transaction.ID(),
		)
	}

	if firstClaim.Attempts != 1 {
		t.Fatalf(
			"expected first claim attempts 1, got %d",
			firstClaim.Attempts,
		)
	}

	_, err = repository.ClaimNextPendingReference(
		ctx,
		db.Pool,
		1,
	)
	if !errors.Is(err, ErrWagerTransactionNotFound) {
		t.Fatalf(
			"expected transaction unavailable during lease, got %v",
			err,
		)
	}

	time.Sleep(1100 * time.Millisecond)

	secondClaim, err := repository.ClaimNextPendingReference(
		ctx,
		db.Pool,
		1,
	)
	if err != nil {
		t.Fatalf("second claim after lease: %v", err)
	}

	if secondClaim.Transaction.ID() != "transaction-pending-lease" {
		t.Fatalf(
			"expected reclaimed transaction-pending-lease, got %s",
			secondClaim.Transaction.ID(),
		)
	}

	if secondClaim.Attempts != 2 {
		t.Fatalf(
			"expected 2 attempts after reclaim, got %d",
			secondClaim.Attempts,
		)
	}
}
