package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestOutboxRepositoryClaimNextConcurrentlyIntegration(t *testing.T) {
	ctx := context.Background()

	db, err := New(ctx, inboxTestDatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	_, err = db.Pool.Exec(ctx, `TRUNCATE TABLE outbox`)
	if err != nil {
		t.Fatalf("clean outbox: %v", err)
	}

	repository := NewOutboxRepository()
	now := time.Now().UTC()

	message := OutboxMessage{
		ID:            "outbox-concurrent-1",
		AggregateType: "WAGER_TRANSACTION",
		AggregateID:   "transaction-concurrent-1",
		EventType:     "WAGER_TRANSACTION_PROCESSED",
		Payload:       []byte(`{"transactionId":"transaction-concurrent-1"}`),
		AvailableAt:   now,
		CreatedAt:     now,
	}

	if err := repository.Insert(
		ctx,
		db.Pool,
		message,
	); err != nil {
		t.Fatalf("insert outbox message: %v", err)
	}

	const workerCount = 20

	start := make(chan struct{})
	resultsCh := make(chan *PendingOutboxMessage, workerCount)
	errorsCh := make(chan error, workerCount)

	var wg sync.WaitGroup
	wg.Add(workerCount)

	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()

			<-start

			result, err := repository.ClaimNext(
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
		if result != nil {
			claimed++

			if result.ID != message.ID {
				t.Fatalf(
					"expected claimed message %s, got %s",
					message.ID,
					result.ID,
				)
			}
		}
	}

	for claimErr := range errorsCh {
		switch {
		case claimErr == nil:
			// Successful claim.

		case errors.Is(claimErr, ErrOutboxMessageNotFound):
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
			"expected %d workers without a message, got %d",
			workerCount-1,
			notFound,
		)
	}

	var attempts int
	var publishedAt *time.Time

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT attempts, published_at
		FROM outbox
		WHERE id = $1
		`,
		message.ID,
	).Scan(
		&attempts,
		&publishedAt,
	)
	if err != nil {
		t.Fatalf("find outbox message: %v", err)
	}

	if attempts != 1 {
		t.Fatalf(
			"expected exactly 1 attempt, got %d",
			attempts,
		)
	}

	if publishedAt != nil {
		t.Fatalf(
			"expected unpublished message, got published_at %s",
			publishedAt,
		)
	}
}

func TestOutboxRepositoryClaimNextBecomesAvailableAfterLeaseIntegration(
	t *testing.T,
) {
	ctx := context.Background()

	db, err := New(ctx, inboxTestDatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	_, err = db.Pool.Exec(ctx, `TRUNCATE TABLE outbox`)
	if err != nil {
		t.Fatalf("clean outbox: %v", err)
	}

	repository := NewOutboxRepository()
	now := time.Now().UTC()

	message := OutboxMessage{
		ID:            "outbox-lease-1",
		AggregateType: "WAGER_TRANSACTION",
		AggregateID:   "transaction-lease-1",
		EventType:     "WAGER_TRANSACTION_PROCESSED",
		Payload:       []byte(`{"transactionId":"transaction-lease-1"}`),
		AvailableAt:   now,
		CreatedAt:     now,
	}

	if err := repository.Insert(
		ctx,
		db.Pool,
		message,
	); err != nil {
		t.Fatalf("insert outbox message: %v", err)
	}

	firstClaim, err := repository.ClaimNext(
		ctx,
		db.Pool,
		1,
	)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}

	if firstClaim.ID != message.ID {
		t.Fatalf(
			"expected first claim %s, got %s",
			message.ID,
			firstClaim.ID,
		)
	}

	_, err = repository.ClaimNext(
		ctx,
		db.Pool,
		1,
	)
	if !errors.Is(err, ErrOutboxMessageNotFound) {
		t.Fatalf(
			"expected message to be unavailable during lease, got %v",
			err,
		)
	}

	time.Sleep(1100 * time.Millisecond)

	secondClaim, err := repository.ClaimNext(
		ctx,
		db.Pool,
		1,
	)
	if err != nil {
		t.Fatalf("second claim after lease: %v", err)
	}

	if secondClaim.ID != message.ID {
		t.Fatalf(
			"expected reclaimed message %s, got %s",
			message.ID,
			secondClaim.ID,
		)
	}

	if secondClaim.Attempts != 2 {
		t.Fatalf(
			"expected 2 attempts after reclaim, got %d",
			secondClaim.Attempts,
		)
	}
}
