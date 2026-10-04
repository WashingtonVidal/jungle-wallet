package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

const inboxTestDatabaseURL = "postgres://jungle:jungle@localhost:5432/jungle_wallet?sslmode=disable"

func TestInboxRepositoryLifecycleIntegration(t *testing.T) {
	ctx := context.Background()

	db, err := New(ctx, inboxTestDatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	_, err = db.Pool.Exec(ctx, `TRUNCATE TABLE inbox`)
	if err != nil {
		t.Fatalf("clean inbox: %v", err)
	}

	repository := NewInboxRepository()
	now := time.Now().UTC()

	message := InboxMessage{
		ID:           "inbox-1",
		ConsumerName: "wager-transactions-consumer",
		MessageID:    "message-123",
		PayloadHash:  "payload-hash-123",
		Payload:      []byte(`{"messageId":"message-123"}`),
		Status:       "RECEIVED",
		Attempts:     0,
		ReceivedAt:   now,
	}

	if err := repository.Insert(ctx, db.Pool, message); err != nil {
		t.Fatalf("insert inbox message: %v", err)
	}

	stored, err := repository.FindByConsumerAndMessageID(
		ctx,
		db.Pool,
		message.ConsumerName,
		message.MessageID,
	)
	if err != nil {
		t.Fatalf("find inbox message: %v", err)
	}

	if stored.Status != "RECEIVED" {
		t.Fatalf(
			"expected status RECEIVED, got %s",
			stored.Status,
		)
	}

	if stored.Attempts != 0 {
		t.Fatalf(
			"expected 0 attempts, got %d",
			stored.Attempts,
		)
	}

	if err := repository.MarkProcessing(
		ctx,
		db.Pool,
		message.ConsumerName,
		message.MessageID,
	); err != nil {
		t.Fatalf("mark processing: %v", err)
	}

	stored, err = repository.FindByConsumerAndMessageID(
		ctx,
		db.Pool,
		message.ConsumerName,
		message.MessageID,
	)
	if err != nil {
		t.Fatalf("find processing inbox message: %v", err)
	}

	if stored.Status != "PROCESSING" {
		t.Fatalf(
			"expected status PROCESSING, got %s",
			stored.Status,
		)
	}

	if stored.Attempts != 1 {
		t.Fatalf(
			"expected 1 attempt, got %d",
			stored.Attempts,
		)
	}

	processedAt := now.Add(time.Second).Truncate(time.Microsecond)

	if err := repository.MarkProcessed(
		ctx,
		db.Pool,
		message.ConsumerName,
		message.MessageID,
		processedAt,
	); err != nil {
		t.Fatalf("mark processed: %v", err)
	}

	stored, err = repository.FindByConsumerAndMessageID(
		ctx,
		db.Pool,
		message.ConsumerName,
		message.MessageID,
	)
	if err != nil {
		t.Fatalf("find processed inbox message: %v", err)
	}

	if stored.Status != "PROCESSED" {
		t.Fatalf(
			"expected status PROCESSED, got %s",
			stored.Status,
		)
	}

	if stored.ProcessedAt == nil {
		t.Fatal("expected processed_at to be set")
	}

	if !stored.ProcessedAt.Equal(processedAt) {
		t.Fatalf(
			"expected processed_at %s, got %s",
			processedAt,
			stored.ProcessedAt,
		)
	}

	_, err = repository.FindByConsumerAndMessageID(
		ctx,
		db.Pool,
		"unknown-consumer",
		"unknown-message",
	)

	if !errors.Is(err, ErrInboxMessageNotFound) {
		t.Fatalf(
			"expected ErrInboxMessageNotFound, got %v",
			err,
		)
	}
}

func TestInboxRepositoryRegisterReplayIntegration(t *testing.T) {
	ctx := context.Background()

	db, err := New(ctx, inboxTestDatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	_, err = db.Pool.Exec(ctx, `TRUNCATE TABLE inbox`)
	if err != nil {
		t.Fatalf("clean inbox: %v", err)
	}

	repository := NewInboxRepository()
	now := time.Now().UTC()

	message := InboxMessage{
		ID:           "inbox-replay-1",
		ConsumerName: "wager-transactions-consumer",
		MessageID:    "message-replay-1",
		PayloadHash:  "same-payload-hash",
		Payload:      []byte(`{"messageId":"message-replay-1"}`),
		Status:       "RECEIVED",
		Attempts:     0,
		ReceivedAt:   now,
	}

	first, replay, err := repository.Register(
		ctx,
		db.Pool,
		message,
	)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	if replay {
		t.Fatal("first registration must not be replay")
	}

	if first.MessageID != message.MessageID {
		t.Fatalf(
			"expected message ID %s, got %s",
			message.MessageID,
			first.MessageID,
		)
	}

	second, replay, err := repository.Register(
		ctx,
		db.Pool,
		message,
	)
	if err != nil {
		t.Fatalf("second register: %v", err)
	}

	if !replay {
		t.Fatal("second identical registration must be replay")
	}

	if second.ID != first.ID {
		t.Fatalf(
			"expected replay to return inbox ID %s, got %s",
			first.ID,
			second.ID,
		)
	}

	var count int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM inbox
		WHERE consumer_name = $1
		  AND message_id = $2
		`,
		message.ConsumerName,
		message.MessageID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count inbox messages: %v", err)
	}

	if count != 1 {
		t.Fatalf(
			"expected exactly 1 inbox message, got %d",
			count,
		)
	}
}

func TestInboxRepositoryRegisterPayloadConflictIntegration(t *testing.T) {
	ctx := context.Background()

	db, err := New(ctx, inboxTestDatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	_, err = db.Pool.Exec(ctx, `TRUNCATE TABLE inbox`)
	if err != nil {
		t.Fatalf("clean inbox: %v", err)
	}

	repository := NewInboxRepository()
	now := time.Now().UTC()

	original := InboxMessage{
		ID:           "inbox-conflict-1",
		ConsumerName: "wager-transactions-consumer",
		MessageID:    "message-conflict-1",
		PayloadHash:  "payload-hash-A",
		Payload:      []byte(`{"amount":"25.00"}`),
		Status:       "RECEIVED",
		ReceivedAt:   now,
	}

	_, replay, err := repository.Register(
		ctx,
		db.Pool,
		original,
	)
	if err != nil {
		t.Fatalf("register original message: %v", err)
	}

	if replay {
		t.Fatal("original message must not be replay")
	}

	conflicting := original
	conflicting.ID = "inbox-conflict-2"
	conflicting.PayloadHash = "payload-hash-B"
	conflicting.Payload = []byte(`{"amount":"50.00"}`)

	result, replay, err := repository.Register(
		ctx,
		db.Pool,
		conflicting,
	)

	if !errors.Is(err, ErrInboxPayloadConflict) {
		t.Fatalf(
			"expected ErrInboxPayloadConflict, got %v",
			err,
		)
	}

	if result != nil {
		t.Fatal("conflicting registration must not return a message")
	}

	if replay {
		t.Fatal("payload conflict must not be reported as replay")
	}

	var count int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM inbox
		WHERE consumer_name = $1
		  AND message_id = $2
		`,
		original.ConsumerName,
		original.MessageID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count inbox messages: %v", err)
	}

	if count != 1 {
		t.Fatalf(
			"expected exactly 1 inbox message after conflict, got %d",
			count,
		)
	}
}

func TestInboxRepositoryRegisterSameMessageConcurrentlyIntegration(t *testing.T) {
	ctx := context.Background()

	db, err := New(ctx, inboxTestDatabaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer db.Close()

	_, err = db.Pool.Exec(ctx, `TRUNCATE TABLE inbox`)
	if err != nil {
		t.Fatalf("clean inbox: %v", err)
	}

	repository := NewInboxRepository()
	now := time.Now().UTC()

	const requestCount = 50

	start := make(chan struct{})
	resultsCh := make(chan *InboxMessage, requestCount)
	replaysCh := make(chan bool, requestCount)
	errorsCh := make(chan error, requestCount)

	run := func(index int) {
		<-start

		message := InboxMessage{
			ID:           fmt.Sprintf("inbox-concurrent-%d", index),
			ConsumerName: "wager-transactions-consumer",
			MessageID:    "message-concurrent-1",
			PayloadHash:  "same-concurrent-payload-hash",
			Payload:      []byte(`{"messageId":"message-concurrent-1"}`),
			Status:       "RECEIVED",
			ReceivedAt:   now,
		}

		result, replay, err := repository.Register(
			ctx,
			db.Pool,
			message,
		)

		resultsCh <- result
		replaysCh <- replay
		errorsCh <- err
	}

	for i := 0; i < requestCount; i++ {
		go run(i)
	}

	close(start)

	successes := 0
	replays := 0

	for i := 0; i < requestCount; i++ {
		result := <-resultsCh
		replay := <-replaysCh
		registerErr := <-errorsCh

		if registerErr != nil {
			t.Fatalf(
				"concurrent registration failed: %v",
				registerErr,
			)
		}

		if result == nil {
			t.Fatal("concurrent registration returned nil message")
		}

		successes++

		if replay {
			replays++
		}
	}

	if successes != requestCount {
		t.Fatalf(
			"expected %d successful registrations, got %d",
			requestCount,
			successes,
		)
	}

	if replays != requestCount-1 {
		t.Fatalf(
			"expected %d replays, got %d",
			requestCount-1,
			replays,
		)
	}

	var count int

	err = db.Pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM inbox
		WHERE consumer_name = $1
		  AND message_id = $2
		`,
		"wager-transactions-consumer",
		"message-concurrent-1",
	).Scan(&count)
	if err != nil {
		t.Fatalf("count inbox messages: %v", err)
	}

	if count != 1 {
		t.Fatalf(
			"expected exactly 1 inbox message, got %d",
			count,
		)
	}
}
