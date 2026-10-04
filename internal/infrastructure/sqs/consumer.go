package sqs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"jungle-wallet/internal/application"
	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/postgres"
)

const ConsumerName = "wager-transactions"

type WagerTransactionEnvelope struct {
	MessageID  string               `json:"messageId"`
	Type       string               `json:"type"`
	OccurredAt time.Time            `json:"occurredAt"`
	Data       WagerTransactionData `json:"data"`
}

type WagerTransactionData struct {
	ProviderID                     string            `json:"providerId"`
	ExternalTransactionID          string            `json:"externalTransactionId"`
	IdempotencyKey                 string            `json:"idempotencyKey"`
	PlayerID                       string            `json:"playerId"`
	WalletID                       string            `json:"walletId"`
	RoundID                        string            `json:"roundId"`
	GameID                         string            `json:"gameId"`
	Kind                           string            `json:"kind"`
	Money                          domain.MoneyInput `json:"money"`
	ReferenceExternalTransactionID string            `json:"referenceExternalTransactionId,omitempty"`
}

type Consumer struct {
	db      *postgres.DB
	inbox   *postgres.InboxRepository
	service *application.ProcessWagerService
}

func NewConsumer(
	db *postgres.DB,
	inbox *postgres.InboxRepository,
	service *application.ProcessWagerService,
) *Consumer {
	return &Consumer{
		db:      db,
		inbox:   inbox,
		service: service,
	}
}

func (c *Consumer) ProcessMessage(
	ctx context.Context,
	message types.Message,
) error {
	if c == nil ||
		c.db == nil ||
		c.inbox == nil ||
		c.service == nil {
		return fmt.Errorf("SQS consumer is not initialized")
	}

	if message.Body == nil || strings.TrimSpace(*message.Body) == "" {
		return fmt.Errorf("%w: empty SQS message body", domain.ErrInvalid)
	}

	rawPayload := []byte(*message.Body)

	var envelope WagerTransactionEnvelope

	decoder := json.NewDecoder(strings.NewReader(*message.Body))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("%w: invalid SQS message: %v", domain.ErrInvalid, err)
	}

	if err := validateEnvelope(envelope); err != nil {
		return err
	}

	money, err := envelope.Data.Money.Parse()
	if err != nil {
		return fmt.Errorf("%w: invalid money", domain.ErrInvalid)
	}

	kind, err := parseWagerKind(envelope.Data.Kind)
	if err != nil {
		return err
	}

	payloadHash := hashBytes(rawPayload)

	businessHash, err := wagerPayloadHash(envelope.Data)
	if err != nil {
		return fmt.Errorf("calculate wager payload hash: %w", err)
	}

	now := time.Now().UTC()

	err = c.db.WithTx(ctx, func(tx pgx.Tx) error {
		inboxMessage := postgres.InboxMessage{
			ID:           uuid.NewString(),
			ConsumerName: ConsumerName,
			MessageID:    envelope.MessageID,
			PayloadHash:  payloadHash,
			Payload:      rawPayload,
			Status:       "RECEIVED",
			Attempts:     0,
			ReceivedAt:   now,
		}

		registered, replay, err := c.inbox.Register(
			ctx,
			tx,
			inboxMessage,
		)
		if err != nil {
			return err
		}

		if replay && registered.Status == "PROCESSED" {
			return nil
		}

		if err := c.inbox.MarkProcessing(
			ctx,
			tx,
			ConsumerName,
			envelope.MessageID,
		); err != nil {
			return err
		}

		_, err = c.service.ExecuteTx(
			ctx,
			tx,
			application.ProcessWagerInput{
				ID:                             uuid.NewString(),
				LedgerID:                       uuid.NewString(),
				WalletID:                       envelope.Data.WalletID,
				ExternalTransactionID:          envelope.Data.ExternalTransactionID,
				ProviderID:                     envelope.Data.ProviderID,
				IdempotencyKey:                 envelope.Data.IdempotencyKey,
				PayloadHash:                    businessHash,
				ReferenceExternalTransactionID: envelope.Data.ReferenceExternalTransactionID,
				PlayerID:                       envelope.Data.PlayerID,
				RoundID:                        envelope.Data.RoundID,
				GameID:                         envelope.Data.GameID,
				Kind:                           kind,
				Money:                          money,
				CreatedAt:                      envelope.OccurredAt.UTC(),
			},
		)
		if err != nil {
			return err
		}

		if err := c.inbox.MarkProcessed(
			ctx,
			tx,
			ConsumerName,
			envelope.MessageID,
			time.Now().UTC(),
		); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func validateEnvelope(envelope WagerTransactionEnvelope) error {
	if strings.TrimSpace(envelope.MessageID) == "" {
		return fmt.Errorf("%w: messageId is required", domain.ErrInvalid)
	}

	if envelope.Type != "WagerTransactionRequested" {
		return fmt.Errorf("%w: invalid message type", domain.ErrInvalid)
	}

	if envelope.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurredAt is required", domain.ErrInvalid)
	}

	data := envelope.Data

	if strings.TrimSpace(data.ProviderID) == "" ||
		strings.TrimSpace(data.ExternalTransactionID) == "" ||
		strings.TrimSpace(data.IdempotencyKey) == "" ||
		strings.TrimSpace(data.PlayerID) == "" ||
		strings.TrimSpace(data.WalletID) == "" ||
		strings.TrimSpace(data.RoundID) == "" ||
		strings.TrimSpace(data.GameID) == "" ||
		strings.TrimSpace(data.Kind) == "" {
		return fmt.Errorf("%w: incomplete wager data", domain.ErrInvalid)
	}

	return nil
}

func parseWagerKind(value string) (domain.WagerTransactionKind, error) {
	switch value {
	case "BET":
		return domain.WagerTransactionKindBet, nil
	case "WIN":
		return domain.WagerTransactionKindWin, nil
	case "LOSS":
		return domain.WagerTransactionKindLoss, nil
	case "REFUND":
		return domain.WagerTransactionKindRefund, nil
	case "ROLLBACK":
		return domain.WagerTransactionKindRollback, nil
	default:
		return "", domain.ErrInvalidTransactionKind
	}
}

func wagerPayloadHash(data WagerTransactionData) (string, error) {
	canonical := struct {
		ProviderID                     string            `json:"providerId"`
		ExternalTransactionID          string            `json:"externalTransactionId"`
		PlayerID                       string            `json:"playerId"`
		WalletID                       string            `json:"walletId"`
		RoundID                        string            `json:"roundId"`
		GameID                         string            `json:"gameId"`
		Kind                           string            `json:"kind"`
		Money                          domain.MoneyInput `json:"money"`
		ReferenceExternalTransactionID string            `json:"referenceExternalTransactionId,omitempty"`
	}{
		ProviderID:                     data.ProviderID,
		ExternalTransactionID:          data.ExternalTransactionID,
		PlayerID:                       data.PlayerID,
		WalletID:                       data.WalletID,
		RoundID:                        data.RoundID,
		GameID:                         data.GameID,
		Kind:                           data.Kind,
		Money:                          data.Money,
		ReferenceExternalTransactionID: data.ReferenceExternalTransactionID,
	}

	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}

	return hashBytes(raw), nil
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
