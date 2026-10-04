package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type OutboxMessage struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	AvailableAt   time.Time
	CreatedAt     time.Time
}

type OutboxRepository struct{}

func NewOutboxRepository() *OutboxRepository {
	return &OutboxRepository{}
}

func (r *OutboxRepository) Insert(
	ctx context.Context,
	q Querier,
	message OutboxMessage,
) error {
	if message.ID == "" ||
		message.AggregateType == "" ||
		message.AggregateID == "" ||
		message.EventType == "" ||
		len(message.Payload) == 0 ||
		message.AvailableAt.IsZero() ||
		message.CreatedAt.IsZero() {
		return fmt.Errorf("invalid outbox message")
	}

	_, err := q.Exec(
		ctx,
		`
		INSERT INTO outbox (
			id,
			aggregate_type,
			aggregate_id,
			event_type,
			payload,
			attempts,
			available_at,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, 0, $6, $7)
		`,
		message.ID,
		message.AggregateType,
		message.AggregateID,
		message.EventType,
		message.Payload,
		message.AvailableAt,
		message.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert outbox message: %w", err)
	}

	return nil
}

var ErrOutboxMessageNotFound = errors.New("outbox message not found")

type PendingOutboxMessage struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	Attempts      int
	AvailableAt   time.Time
	CreatedAt     time.Time
}

func (r *OutboxRepository) ClaimNext(
	ctx context.Context,
	q Querier,
	leaseSeconds int,
) (*PendingOutboxMessage, error) {
	if leaseSeconds <= 0 {
		return nil, fmt.Errorf("lease seconds must be positive")
	}

	var message PendingOutboxMessage

	err := q.QueryRow(
		ctx,
		`
		WITH candidate AS (
			SELECT id
			FROM outbox
			WHERE published_at IS NULL
			  AND available_at <= NOW()
			ORDER BY available_at, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE outbox AS o
		SET
			attempts = o.attempts + 1,
			available_at = NOW() + ($1 * INTERVAL '1 second'),
			last_error = NULL
		FROM candidate
		WHERE o.id = candidate.id
		RETURNING
			o.id,
			o.aggregate_type,
			o.aggregate_id,
			o.event_type,
			o.payload,
			o.attempts,
			o.available_at,
			o.created_at
		`,
		leaseSeconds,
	).Scan(
		&message.ID,
		&message.AggregateType,
		&message.AggregateID,
		&message.EventType,
		&message.Payload,
		&message.Attempts,
		&message.AvailableAt,
		&message.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOutboxMessageNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("claim outbox message: %w", err)
	}

	return &message, nil
}

func (r *OutboxRepository) MarkPublished(
	ctx context.Context,
	q Querier,
	id string,
	publishedAt time.Time,
) error {
	if id == "" || publishedAt.IsZero() {
		return fmt.Errorf("invalid published outbox message")
	}

	commandTag, err := q.Exec(
		ctx,
		`
		UPDATE outbox
		SET
			published_at = $2,
			last_error = NULL
		WHERE id = $1
		  AND published_at IS NULL
		`,
		id,
		publishedAt,
	)
	if err != nil {
		return fmt.Errorf("mark outbox message published: %w", err)
	}

	if commandTag.RowsAffected() == 0 {
		return ErrOutboxMessageNotFound
	}

	return nil
}

func (r *OutboxRepository) MarkFailed(
	ctx context.Context,
	q Querier,
	id string,
	lastError string,
	availableAt time.Time,
) error {
	if id == "" || lastError == "" || availableAt.IsZero() {
		return fmt.Errorf("invalid failed outbox message")
	}

	commandTag, err := q.Exec(
		ctx,
		`
		UPDATE outbox
		SET
			last_error = $2,
			available_at = $3
		WHERE id = $1
		  AND published_at IS NULL
		`,
		id,
		lastError,
		availableAt,
	)
	if err != nil {
		return fmt.Errorf("mark outbox message failed: %w", err)
	}

	if commandTag.RowsAffected() == 0 {
		return ErrOutboxMessageNotFound
	}

	return nil
}
