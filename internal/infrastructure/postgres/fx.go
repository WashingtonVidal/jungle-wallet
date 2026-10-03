package postgres

import (
	"context"

	"go.uber.org/fx"

	"jungle-wallet/internal/config"
)

func NewFromConfig(
	lifecycle fx.Lifecycle,
	cfg *config.Config,
) (*DB, error) {
	db, err := New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	lifecycle.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			db.Close()
			return nil
		},
	})

	return db, nil
}

func RegisterDB(db *DB) {
	// Intentionally empty.
	// Requiring *DB here forces Fx to construct the database dependency.
}
