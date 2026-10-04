package sqs

import (
	"context"
	"errors"
	"fmt"
	"log"
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

	log.Printf("SQS consumer started queue=%s", w.queueURL)

	for {
		if err := ctx.Err(); err != nil {
			log.Printf("SQS consumer stopped")
			return nil
		}

		output, err := w.client.ReceiveMessage(
			ctx,
			&awssqs.ReceiveMessageInput{
				QueueUrl:            &w.queueURL,
				MaxNumberOfMessages: 1,
				WaitTimeSeconds:     20,
				VisibilityTimeout:   30,
			},
		)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			log.Printf("receive SQS message: %v", err)

			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
				continue
			}
		}

		for _, message := range output.Messages {
			if err := w.consumer.ProcessMessage(ctx, message); err != nil {
				log.Printf(
					"process SQS message message_id=%s error=%v",
					stringValue(message.MessageId),
					err,
				)

				// Do not delete failed messages.
				// SQS will make them visible again after the
				// visibility timeout and eventually move them
				// to the DLQ according to the redrive policy.
				continue
			}

			if message.ReceiptHandle == nil {
				log.Printf(
					"SQS message has no receipt handle message_id=%s",
					stringValue(message.MessageId),
				)
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

				log.Printf(
					"delete SQS message message_id=%s error=%v",
					stringValue(message.MessageId),
					err,
				)

				continue
			}

			log.Printf(
				"SQS message processed message_id=%s",
				stringValue(message.MessageId),
			)
		}
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
