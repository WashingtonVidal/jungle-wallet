package main

import (
	"go.uber.org/fx"

	"jungle-wallet/internal/application"
	"jungle-wallet/internal/config"
	"jungle-wallet/internal/infrastructure/auth"
	"jungle-wallet/internal/infrastructure/httpserver"
	"jungle-wallet/internal/infrastructure/postgres"
)

func main() {
	fx.New(
		fx.Provide(
			config.New,
			postgres.NewFromConfig,
			auth.NewFromConfig,

			postgres.NewWalletRepository,
			postgres.NewWagerTransactionRepository,
			postgres.NewLedgerRepository,
			postgres.NewOutboxRepository,

			application.NewProcessWagerService,

			httpserver.NewWagerHandler,
			httpserver.NewMux,
			httpserver.NewServer,
		),
		fx.Invoke(
			postgres.RegisterDB,
			httpserver.RegisterLifecycle,
		),
	).Run()
}
