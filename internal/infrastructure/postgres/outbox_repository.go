package postgres

import (
	"context"
	"fmt"
	"time"
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
