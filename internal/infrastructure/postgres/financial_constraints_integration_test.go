package postgres

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/testsupport"
	"testing"
	"time"
)

func TestLedgerAppendOnlyAndOpeningUniquenessIntegration(t *testing.T) {
	ctx := context.Background()
	db, err := New(ctx, testsupport.DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	amount, _ := domain.Parse("10.00", "BRL")
	zero, _ := domain.Zero("BRL")
	wallet, err := domain.NewWallet(uuid.NewString(), uuid.NewString(), amount, now)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := wallet.State()
	wallets := NewWalletRepository()
	transactions := NewWagerTransactionRepository()
	ledger := NewLedgerRepository()
	if err := wallets.Insert(ctx, db.Pool, wallet); err != nil {
		t.Fatal(err)
	}
	opening, err := domain.NewOpeningTransaction(domain.NewOpeningTransactionParams{ID: uuid.NewString(), WalletID: state.ID, PlayerID: state.PlayerID, Money: amount, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := transactions.Insert(ctx, db.Pool, opening); err != nil {
		t.Fatal(err)
	}
	entry, err := domain.NewLedger(domain.LedgerState{ID: uuid.NewString(), WalletID: state.ID, TransactionID: opening.ID(), Direction: domain.LedgerCredit, Money: amount, BalanceBefore: zero, BalanceAfter: amount, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Insert(ctx, db.Pool, entry); err != nil {
		t.Fatal(err)
	}
	e, _ := entry.State()
	for _, statement := range []string{"UPDATE wallet_ledger_entries SET amount_units=amount_units WHERE id=$1", "DELETE FROM wallet_ledger_entries WHERE id=$1"} {
		if _, err := db.Pool.Exec(ctx, statement, e.ID); err == nil {
			t.Fatal("ledger mutation accepted")
		}
	}
	if err := ledger.Insert(ctx, db.Pool, entry); err == nil {
		t.Fatal("duplicate ledger accepted")
	}
	duplicate, _ := domain.NewOpeningTransaction(domain.NewOpeningTransactionParams{ID: uuid.NewString(), WalletID: state.ID, PlayerID: state.PlayerID, Money: amount, CreatedAt: now})
	err = transactions.Insert(ctx, db.Pool, duplicate)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "wager_transactions_opening_wallet_unique" {
		t.Fatalf("opening uniqueness: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, "UPDATE wallets SET balance_units=-1 WHERE id=$1", state.ID); err == nil {
		t.Fatal("negative persisted balance accepted")
	}
}
