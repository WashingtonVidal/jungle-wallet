package postgres

import (
	"context"
	"fmt"

	"jungle-wallet/internal/domain"
)

type LedgerRepository struct{}

func NewLedgerRepository() *LedgerRepository {
	return &LedgerRepository{}
}

func (r *LedgerRepository) Insert(
	ctx context.Context,
	q Querier,
	entry domain.WalletLedgerEntry,
) error {
	state, err := entry.State()
	if err != nil {
		return err
	}

	_, err = q.Exec(
		ctx,
		`
		INSERT INTO wallet_ledger_entries (
			id,
			wallet_id,
			transaction_id,
			direction,
			amount_units,
			currency,
			balance_before_units,
			balance_after_units,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`,
		state.ID,
		state.WalletID,
		state.TransactionID,
		state.Direction,
		state.Money.Units(),
		state.Money.Currency(),
		state.BalanceBefore.Units(),
		state.BalanceAfter.Units(),
		state.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert ledger entry: %w", err)
	}

	return nil
}
