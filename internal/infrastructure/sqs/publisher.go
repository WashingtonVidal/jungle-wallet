package sqs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"jungle-wallet/internal/observability"
	"log/slog"
	"time"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	"jungle-wallet/internal/infrastructure/postgres"
)

const (
	outboxLeaseSeconds = 30
	outboxPollInterval = 500 * time.Millisecond
)

type Publisher struct {
	client   *awssqs.Client
	db       *postgres.DB
	outbox   *postgres.OutboxRepository
	queueURL string
}

type outboxEnvelope struct {
	Version       int             `json:"version"`
	CorrelationID string          `json:"correlationId"`
	CausationID   string          `json:"causationId,omitempty"`
	EventID       string          `json:"eventId"`
	EventType     string          `json:"eventType"`
	AggregateType string          `json:"aggregateType"`
	AggregateID   string          `json:"aggregateId"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Data          json.RawMessage `json:"data"`
}

func NewPublisher(
	client *awssqs.Client,
	db *postgres.DB,
	outbox *postgres.OutboxRepository,
	queueURL string,
) *Publisher {
	return &Publisher{
		client:   client,
		db:       db,
		outbox:   outbox,
		queueURL: queueURL,
	}
}

func (p *Publisher) Run(ctx context.Context) error {
	if p == nil ||
		p.client == nil ||
		p.db == nil ||
		p.db.Pool == nil ||
		p.outbox == nil ||
		p.queueURL == "" {
		return fmt.Errorf("outbox publisher is not initialized")
	}

	slog.Info("outbox publisher started", "queue", p.queueURL)

	for {
		err := p.publishNext(ctx)

		switch {
		case err == nil:
			continue

		case errors.Is(err, postgres.ErrOutboxMessageNotFound):
			timer := time.NewTimer(outboxPollInterval)

			select {
			case <-ctx.Done():
				timer.Stop()
				return nil

			case <-timer.C:
			}

		case errors.Is(err, context.Canceled):
			return nil

		default:
			slog.Error("outbox publisher failed", "error", err)

			timer := time.NewTimer(outboxPollInterval)

			select {
			case <-ctx.Done():
				timer.Stop()
				return nil

			case <-timer.C:
			}
		}
	}
}

func (p *Publisher) publishNext(ctx context.Context) error {
	message, err := p.outbox.ClaimNext(
		ctx,
		p.db.Pool,
		outboxLeaseSeconds,
	)
	if err != nil {
		return err
	}

	var snapshot struct {
		TransactionID string `json:"transactionId"`
	}
	if err := json.Unmarshal(message.Payload, &snapshot); err != nil {
		return p.failMessage(ctx, message, fmt.Errorf("invalid outbox snapshot: %w", err))
	}
	correlation := snapshot.TransactionID
	if correlation == "" {
		correlation = message.AggregateID
	}
	envelope := outboxEnvelope{
		Version:       1,
		CorrelationID: correlation,
		EventID:       message.ID,
		EventType:     externalEventType(message.EventType),
		AggregateType: message.AggregateType,
		AggregateID:   message.AggregateID,
		OccurredAt:    message.CreatedAt.UTC(),
		Data:          json.RawMessage(message.Payload),
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		observability.Inc("outbox_retries")
		return p.failMessage(
			ctx,
			message,
			fmt.Errorf("marshal outbox envelope: %w", err),
		)
	}

	_, err = p.client.SendMessage(
		ctx,
		&awssqs.SendMessageInput{
			QueueUrl:               &p.queueURL,
			MessageBody:            stringPointer(string(body)),
			MessageGroupId:         stringPointer(message.AggregateID),
			MessageDeduplicationId: stringPointer(message.ID),
		},
	)
	if err != nil {
		observability.Inc("outbox_retries")
		return p.failMessage(
			ctx,
			message,
			fmt.Errorf("send outbox message: %w", err),
		)
	}

	if err := p.outbox.MarkPublished(
		ctx,
		p.db.Pool,
		message.ID,
		time.Now().UTC(),
	); err != nil {
		return fmt.Errorf("mark published: %w", err)
	}

	slog.Info("outbox published", "eventId", message.ID, "transactionId", message.AggregateID, "eventType", message.EventType)

	return nil
}

func (p *Publisher) failMessage(
	ctx context.Context,
	message *postgres.PendingOutboxMessage,
	publishErr error,
) error {
	backoff := retryBackoff(message.Attempts)

	markErr := p.outbox.MarkFailed(
		ctx,
		p.db.Pool,
		message.ID,
		publishErr.Error(),
		time.Now().UTC().Add(backoff),
	)
	if markErr != nil {
		return fmt.Errorf(
			"publish failed: %v; mark failed: %w",
			publishErr,
			markErr,
		)
	}

	return publishErr
}

func retryBackoff(attempts int) time.Duration {
	switch {
	case attempts <= 1:
		return time.Second
	case attempts == 2:
		return 2 * time.Second
	case attempts == 3:
		return 5 * time.Second
	case attempts == 4:
		return 10 * time.Second
	default:
		return 30 * time.Second
	}
}

func stringPointer(value string) *string {
	return &value
}

func externalEventType(value string) string {
	switch value {
	case "WAGER_TRANSACTION_PROCESSED":
		return "WagerTransactionProcessed"
	case "WAGER_TRANSACTION_REJECTED":
		return "WagerTransactionRejected"
	case "WAGER_TRANSACTION_PENDING_REFERENCE":
		return "WagerTransactionPendingReference"
	case "WALLET_BALANCE_CHANGED":
		return "WalletBalanceChanged"
	default:
		return value
	}
}
