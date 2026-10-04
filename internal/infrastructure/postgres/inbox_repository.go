package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrInboxMessageNotFound = errors.New("inbox message not found")
var ErrInboxPayloadConflict = errors.New("inbox message payload conflict")

type InboxMessage struct {
	ID           string
	ConsumerName string
	MessageID    string
	PayloadHash  string
	Payload      []byte
	Status       string
	Attempts     int
	LastError    string
	ReceivedAt   time.Time
	ProcessedAt  *time.Time
}

type InboxRepository struct{}

func NewInboxRepository() *InboxRepository {
	return &InboxRepository{}
}

func (r *InboxRepository) Register(
	ctx context.Context,
	q Querier,
	message InboxMessage,
) (*InboxMessage, bool, error) {
	existing, err := r.FindByConsumerAndMessageID(
		ctx,
		q,
		message.ConsumerName,
		message.MessageID,
	)

	if err == nil {
		if existing.PayloadHash != message.PayloadHash {
			return nil, false, ErrInboxPayloadConflict
		}

		return existing, true, nil
	}

	if !errors.Is(err, ErrInboxMessageNotFound) {
		return nil, false, err
	}

	if err := r.Insert(ctx, q, message); err != nil {
		var pgErr *pgconn.PgError

		if !errors.As(err, &pgErr) ||
			pgErr.Code != "23505" ||
			pgErr.ConstraintName != "inbox_consumer_message_unique" {
			return nil, false, err
		}

		existing, findErr := r.FindByConsumerAndMessageID(
			ctx,
			q,
			message.ConsumerName,
			message.MessageID,
		)
		if findErr != nil {
			return nil, false, findErr
		}

		if existing.PayloadHash != message.PayloadHash {
			return nil, false, ErrInboxPayloadConflict
		}

		return existing, true, nil
	}

	return &message, false, nil
}

func (r *InboxRepository) Insert(
	ctx context.Context,
	q Querier,
	message InboxMessage,
) error {
	if message.ID == "" ||
		message.ConsumerName == "" ||
		message.MessageID == "" ||
		message.PayloadHash == "" ||
		len(message.Payload) == 0 ||
		message.Status == "" ||
		message.ReceivedAt.IsZero() {
		return fmt.Errorf("invalid inbox message")
	}

	_, err := q.Exec(
		ctx,
		`
		INSERT INTO inbox (
			id,
			consumer_name,
			message_id,
			payload_hash,
			payload,
			status,
			attempts,
			last_error,
			received_at,
			processed_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10
		)
		`,
		message.ID,
		message.ConsumerName,
		message.MessageID,
		message.PayloadHash,
		message.Payload,
		message.Status,
		message.Attempts,
		nullableString(message.LastError),
		message.ReceivedAt,
		message.ProcessedAt,
	)
	if err != nil {
		return fmt.Errorf("insert inbox message: %w", err)
	}

	return nil
}

func (r *InboxRepository) FindByConsumerAndMessageID(
	ctx context.Context,
	q Querier,
	consumerName string,
	messageID string,
) (*InboxMessage, error) {
	var message InboxMessage

	err := q.QueryRow(
		ctx,
		`
		SELECT
			id,
			consumer_name,
			message_id,
			payload_hash,
			payload,
			status,
			attempts,
			COALESCE(last_error, ''),
			received_at,
			processed_at
		FROM inbox
		WHERE consumer_name = $1
		  AND message_id = $2
		`,
		consumerName,
		messageID,
	).Scan(
		&message.ID,
		&message.ConsumerName,
		&message.MessageID,
		&message.PayloadHash,
		&message.Payload,
		&message.Status,
		&message.Attempts,
		&message.LastError,
		&message.ReceivedAt,
		&message.ProcessedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInboxMessageNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("find inbox message: %w", err)
	}

	return &message, nil
}
func (r *InboxRepository) MarkProcessing(
	ctx context.Context,
	q Querier,
	consumerName string,
	messageID string,
) error {
	tag, err := q.Exec(
		ctx,
		`
		UPDATE inbox
		SET
			status = 'PROCESSING',
			attempts = attempts + 1,
			last_error = NULL
		WHERE consumer_name = $1
		  AND message_id = $2
		`,
		consumerName,
		messageID,
	)
	if err != nil {
		return fmt.Errorf("mark inbox message processing: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrInboxMessageNotFound
	}

	return nil
}

func (r *InboxRepository) MarkProcessed(
	ctx context.Context,
	q Querier,
	consumerName string,
	messageID string,
	processedAt time.Time,
) error {
	if processedAt.IsZero() {
		return fmt.Errorf("processed at is required")
	}

	tag, err := q.Exec(
		ctx,
		`
		UPDATE inbox
		SET
			status = 'PROCESSED',
			processed_at = $3,
			last_error = NULL
		WHERE consumer_name = $1
		  AND message_id = $2
		`,
		consumerName,
		messageID,
		processedAt,
	)
	if err != nil {
		return fmt.Errorf("mark inbox message processed: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrInboxMessageNotFound
	}

	return nil
}

func (r *InboxRepository) MarkFailed(
	ctx context.Context,
	q Querier,
	consumerName string,
	messageID string,
	lastError string,
) error {
	if lastError == "" {
		return fmt.Errorf("last error is required")
	}

	tag, err := q.Exec(
		ctx,
		`
		UPDATE inbox
		SET
			status = 'FAILED',
			last_error = $3
		WHERE consumer_name = $1
		  AND message_id = $2
		`,
		consumerName,
		messageID,
		lastError,
	)
	if err != nil {
		return fmt.Errorf("mark inbox message failed: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ErrInboxMessageNotFound
	}

	return nil
}
