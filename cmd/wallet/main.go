package main

import (
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"log/slog"
	"os"

	"jungle-wallet/internal/application"
	"jungle-wallet/internal/config"
	"jungle-wallet/internal/infrastructure/auth"
	"jungle-wallet/internal/infrastructure/httpserver"
	"jungle-wallet/internal/infrastructure/postgres"
	"jungle-wallet/internal/infrastructure/sqs"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	fx.New(walletModule(), fx.WithLogger(func() fxevent.Logger { return &fxevent.SlogLogger{Logger: slog.Default()} })).Run()
}

func walletModule() fx.Option {
	return fx.Module("wallet",
		fx.Provide(
			config.New,
			postgres.NewFromConfig,
			auth.NewFromConfig,

			postgres.NewWalletRepository,
			postgres.NewWagerTransactionRepository,
			postgres.NewLedgerRepository,
			postgres.NewOutboxRepository,
			postgres.NewInboxRepository,

			sqs.NewClientFromConfig,
			sqs.NewConsumer,
			sqs.NewWorkerFromDependencies,
			sqs.NewPublisherFromDependencies,

			application.NewProcessWagerService,
			application.NewCreateWalletService,
			application.NewPendingReferenceWorker,

			httpserver.NewWagerHandler,
			httpserver.NewWalletHandler,
			httpserver.NewQueryHandler,
			httpserver.NewMux,
			httpserver.NewServer,
		),
		fx.Invoke(
			postgres.RegisterDB,
			sqs.RegisterLifecycle,
			application.RegisterLifecycle,
			httpserver.RegisterLifecycle,
		),
	)
}
