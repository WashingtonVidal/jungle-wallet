package postgres

import (
	"context"
	"fmt"

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
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	committed = true
	return nil
}
