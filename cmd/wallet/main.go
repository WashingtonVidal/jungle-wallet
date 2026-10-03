package main

import (
	"go.uber.org/fx"

	"jungle-wallet/internal/config"
	"jungle-wallet/internal/infrastructure/httpserver"
	"jungle-wallet/internal/infrastructure/postgres"
)

func main() {
	fx.New(
		fx.Provide(
			config.New,
			postgres.NewFromConfig,
			httpserver.NewMux,
			httpserver.NewServer,
		),
		fx.Invoke(
			postgres.RegisterDB,
			httpserver.RegisterLifecycle,
		),
	).Run()
}
