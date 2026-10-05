package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/observability"

	"github.com/jackc/pgx/v5"
)

// WithTx executes fn inside one PostgreSQL transaction.
// A successful fn commits; an error rolls everything back.
func (db *DB) WithTx(
	ctx context.Context,
	fn func(pgx.Tx) error,
) error {
	if db == nil || db.Pool == nil {
		return fmt.Errorf("database is not initialized")
	}

	tx, err := db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	committed := false

	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if err := fn(tx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "40P01" || pgErr.Code == "40001" {
				observability.Inc("concurrency_conflicts")
			}
			if pgErr.Code == "23505" && (pgErr.ConstraintName == "wager_transactions_provider_external_unique" || pgErr.ConstraintName == "wager_transactions_provider_idempotency_unique") {
				return fmt.Errorf("%w: concurrent identity collision", domain.ErrIdempotencyConflict)
			}
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	committed = true
	return nil
}
