package application

import (
	"context"

	"go.uber.org/fx"
)

func RegisterLifecycle(
	lifecycle fx.Lifecycle,
	worker *PendingReferenceWorker,
) {
	var (
		cancel context.CancelFunc
		done   chan struct{}
	)

	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, cancelWorker := context.WithCancel(
				context.Background(),
			)
			cancel = cancelWorker

			done = make(chan struct{})

			go func() {
				defer close(done)
				_ = worker.Run(ctx)
			}()

			return nil
		},

		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}

			if done == nil {
				return nil
			}

			select {
			case <-done:
				return nil

			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
