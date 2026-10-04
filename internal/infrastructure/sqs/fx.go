package sqs

import (
	"context"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"go.uber.org/fx"

	"jungle-wallet/internal/config"
	"jungle-wallet/internal/infrastructure/postgres"
)

type ClientDependencies struct {
	Client         *awssqs.Client
	QueueURL       string
	OutboxQueueURL string
}

func NewClientFromConfig(
	cfg *config.Config,
) (*ClientDependencies, error) {
	client, err := NewClient(context.Background(), cfg)
	if err != nil {
		return nil, err
	}

	return &ClientDependencies{
		Client:         client,
		QueueURL:       cfg.SQSQueueURL,
		OutboxQueueURL: cfg.SQSOutboxQueueURL,
	}, nil
}

func NewWorkerFromDependencies(
	deps *ClientDependencies,
	consumer *Consumer,
) *Worker {
	return NewWorker(
		deps.Client,
		consumer,
		deps.QueueURL,
	)
}

func NewPublisherFromDependencies(
	deps *ClientDependencies,
	db *postgres.DB,
	outbox *postgres.OutboxRepository,
) *Publisher {
	return NewPublisher(
		deps.Client,
		db,
		outbox,
		deps.OutboxQueueURL,
	)
}

func RegisterLifecycle(
	lifecycle fx.Lifecycle,
	worker *Worker,
	publisher *Publisher,
) {
	var cancel context.CancelFunc

	workerDone := make(chan struct{})
	publisherDone := make(chan struct{})

	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, cancelWorkers := context.WithCancel(context.Background())
			cancel = cancelWorkers

			go func() {
				defer close(workerDone)
				_ = worker.Run(ctx)
			}()

			go func() {
				defer close(publisherDone)
				_ = publisher.Run(ctx)
			}()

			return nil
		},

		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}

			for workerDone != nil || publisherDone != nil {
				select {
				case <-workerDone:
					workerDone = nil

				case <-publisherDone:
					publisherDone = nil

				case <-ctx.Done():
					return ctx.Err()
				}
			}

			return nil
		},
	})
}
