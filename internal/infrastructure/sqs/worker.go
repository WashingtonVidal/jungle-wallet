package sqs

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"jungle-wallet/internal/observability"
	"log/slog"
	"strconv"
	"time"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

type Worker struct {
	client   *awssqs.Client
	consumer *Consumer
	queueURL string
}

func NewWorker(
	client *awssqs.Client,
	consumer *Consumer,
	queueURL string,
) *Worker {
	return &Worker{
		client:   client,
		consumer: consumer,
		queueURL: queueURL,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	if w == nil ||
		w.client == nil ||
		w.consumer == nil ||
		w.queueURL == "" {
		return fmt.Errorf("SQS worker is not initialized")
	}

	slog.Info("SQS consumer started", "queue", w.queueURL)

	for {
		if err := ctx.Err(); err != nil {
			slog.Info("SQS consumer stopped")
			return nil
		}

		output, err := w.client.ReceiveMessage(
			ctx,
			&awssqs.ReceiveMessageInput{
				QueueUrl:                    &w.queueURL,
				MaxNumberOfMessages:         1,
				WaitTimeSeconds:             20,
				VisibilityTimeout:           30,
				MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameApproximateReceiveCount},
			},
		)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			slog.Error("SQS receive failed", "error", err)

			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
				continue
			}
		}

		for _, message := range output.Messages {
			if err := w.consumer.ProcessMessage(ctx, message); err != nil {
				observability.Inc("sqs_retries")
				count, _ := strconv.Atoi(message.Attributes["ApproximateReceiveCount"])
				if count >= 5 {
					observability.Inc("dlq_candidates")
				}
				delay := int32(1)
				for i := 1; i < count && delay < 30; i++ {
					delay *= 2
				}
				if delay > 30 {
					delay = 30
				}
				// A canceled in-flight attempt releases work for another instance.
				retryCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if ctx.Err() != nil {
					delay = 0
				}
				if message.ReceiptHandle != nil {
					_, _ = w.client.ChangeMessageVisibility(retryCtx, &awssqs.ChangeMessageVisibilityInput{QueueUrl: &w.queueURL, ReceiptHandle: message.ReceiptHandle, VisibilityTimeout: delay})
				}
				cancel()
				slog.Error("SQS processing failed", "messageId", stringValue(message.MessageId), "error", err)

				// Do not delete failed messages.
				// SQS will make them visible again after the
				// visibility timeout and eventually move them
				// to the DLQ according to the redrive policy.
				continue
			}

			if message.ReceiptHandle == nil {
				slog.Error("SQS receipt missing", "messageId", stringValue(message.MessageId))

				continue
			}

			_, err := w.client.DeleteMessage(
				ctx,
				&awssqs.DeleteMessageInput{
					QueueUrl:      &w.queueURL,
					ReceiptHandle: message.ReceiptHandle,
				},
			)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}

				slog.Error("SQS delete failed", "messageId", stringValue(message.MessageId), "error", err)

				continue
			}

			slog.Info("SQS processed", "messageId", stringValue(message.MessageId))

		}
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
