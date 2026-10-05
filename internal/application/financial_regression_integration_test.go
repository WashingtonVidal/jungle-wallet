package application

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/postgres"
	"testing"
	"time"
)

func financialFixture(t *testing.T) (context.Context, *postgres.DB, *ProcessWagerService, string, string, time.Time) {
	t.Helper()
	ctx := context.Background()
	db, err := postgres.New(ctx, testDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	wallets := postgres.NewWalletRepository()
	transactions := postgres.NewWagerTransactionRepository()
	ledger := postgres.NewLedgerRepository()
	outbox := postgres.NewOutboxRepository()
	now := time.Now().UTC()
	id, player := uuid.NewString(), uuid.NewString()
	balance, _ := domain.Parse("100.00", "BRL")
	create := NewCreateWalletService(db, wallets, transactions, ledger, outbox)
	if _, err := create.Execute(ctx, CreateWalletInput{ID: id, PlayerID: player, InitialBalance: balance, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return ctx, db, NewProcessWagerService(db, wallets, transactions, ledger, outbox), id, player, now
}
func regressionInput(wallet, player string, now time.Time, kind domain.WagerTransactionKind, amount string) ProcessWagerInput {
	id := uuid.NewString()
	money, _ := domain.Parse(amount, "BRL")
	return ProcessWagerInput{ID: id, LedgerID: uuid.NewString(), WalletID: wallet, PlayerID: player, ProviderID: "provider-regression", ExternalTransactionID: id, IdempotencyKey: id, PayloadHash: id, RoundID: "round", GameID: "game", Kind: kind, Money: money, CreatedAt: now}
}
func TestReferenceFinancialGuardsIntegration(t *testing.T) {
	for _, name := range []string{"amount", "round", "wallet", "status", "duplicate", "cross-kind", "win-round"} {
		t.Run(name, func(t *testing.T) {
			ctx, db, s, wallet, player, now := financialFixture(t)
			bet := regressionInput(wallet, player, now, domain.WagerTransactionKindBet, "25.00")
			if name == "status" {
				bet.Money, _ = domain.Parse("101.00", "BRL")
			}
			if _, err := s.Execute(ctx, bet); err != nil {
				t.Fatal(err)
			}
			refund := regressionInput(wallet, player, now.Add(time.Second), domain.WagerTransactionKindRefund, "25.00")
			refund.ReferenceExternalTransactionID = bet.ExternalTransactionID
			expected := ""
			balance := int64(7500)
			switch name {
			case "amount":
				refund.Money, _ = domain.Parse("24.00", "BRL")
				expected = "REVERSAL_AMOUNT_MISMATCH"
			case "round":
				refund.RoundID = "other-round"
				expected = "REFERENCE_MISMATCH"
			case "wallet":
				money, _ := domain.Parse("100.00", "BRL")
				other, err := domain.NewWallet(uuid.NewString(), uuid.NewString(), money, now)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.wallets.Insert(ctx, db.Pool, other); err != nil {
					t.Fatal(err)
				}
				state, _ := other.State()
				refund.WalletID = state.ID
				refund.PlayerID = state.PlayerID
				expected = "REFERENCE_MISMATCH"
			case "status":
				expected = "REFERENCE_REJECTED"
				balance = 10000
			case "duplicate", "cross-kind":
				result, err := s.Execute(ctx, refund)
				if err != nil || result.Transaction.Status() != domain.WagerTransactionStatusProcessed {
					t.Fatalf("first reversal: %v", err)
				}
				refund = regressionInput(wallet, player, now.Add(2*time.Second), domain.WagerTransactionKindRefund, "25.00")
				refund.ReferenceExternalTransactionID = bet.ExternalTransactionID
				if name == "cross-kind" {
					refund.Kind = domain.WagerTransactionKindRollback
				}
				expected = "ALREADY_REVERSED"
				balance = 10000
			case "win-round":
				refund.Kind = domain.WagerTransactionKindWin
				refund.RoundID = "different"
				expected = "REFERENCE_MISMATCH"
			}
			result, err := s.Execute(ctx, refund)
			if err != nil {
				t.Fatal(err)
			}
			if result.Transaction.Status() != domain.WagerTransactionStatusRejected || result.Transaction.FailureCode() != expected {
				t.Fatalf("status/code %s/%s expected %s", result.Transaction.Status(), result.Transaction.FailureCode(), expected)
			}
			var units int64
			if err := db.Pool.QueryRow(ctx, "SELECT balance_units FROM wallets WHERE id=$1", wallet).Scan(&units); err != nil {
				t.Fatal(err)
			}
			if units != balance {
				t.Fatalf("balance %d want %d", units, balance)
			}
			var ledgerCount, eventCount int
			if err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM wallet_ledger_entries WHERE transaction_id=$1", refund.ID).Scan(&ledgerCount); err != nil {
				t.Fatal(err)
			}
			if ledgerCount != 0 {
				t.Fatal("rejection wrote ledger")
			}
			if err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox WHERE aggregate_id=$1 AND event_type='WAGER_TRANSACTION_REJECTED'", refund.ID).Scan(&eventCount); err != nil {
				t.Fatal(err)
			}
			if eventCount != 1 {
				t.Fatal("missing rejection event")
			}
			replay, err := s.Execute(ctx, refund)
			if err != nil || !replay.Replay || replay.Transaction.FailureCode() != expected {
				t.Fatalf("rejection replay: %v", err)
			}
		})
	}
}
func TestInvalidPendingDoesNotPoisonOriginalIntegration(t *testing.T) {
	ctx, db, s, wallet, player, now := financialFixture(t)
	bet := regressionInput(wallet, player, now, domain.WagerTransactionKindBet, "25.00")
	refund := regressionInput(wallet, player, now, domain.WagerTransactionKindRefund, "30.00")
	refund.ReferenceExternalTransactionID = bet.ExternalTransactionID
	first, err := s.Execute(ctx, refund)
	if err != nil || first.Transaction.Status() != domain.WagerTransactionStatusPendingReference {
		t.Fatalf("pending: %v", err)
	}
	original, err := s.Execute(ctx, bet)
	if err != nil || original.Transaction.Status() != domain.WagerTransactionStatusProcessed {
		t.Fatalf("original poisoned: %v", err)
	}
	resolved, err := s.transactions.FindByProviderAndExternalID(ctx, db.Pool, refund.ProviderID, refund.ExternalTransactionID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.FailureCode() != "REVERSAL_AMOUNT_MISMATCH" {
		t.Fatal(resolved.FailureCode())
	}
}
func TestReversalInsufficientIsDurableIntegration(t *testing.T) {
	ctx, _, s, wallet, player, now := financialFixture(t)
	win := regressionInput(wallet, player, now, domain.WagerTransactionKindWin, "25.00")
	if _, err := s.Execute(ctx, win); err != nil {
		t.Fatal(err)
	}
	spend := regressionInput(wallet, player, now.Add(time.Second), domain.WagerTransactionKindBet, "125.00")
	if _, err := s.Execute(ctx, spend); err != nil {
		t.Fatal(err)
	}
	rollback := regressionInput(wallet, player, now.Add(2*time.Second), domain.WagerTransactionKindRollback, "25.00")
	rollback.ReferenceExternalTransactionID = win.ExternalTransactionID
	result, err := s.Execute(ctx, rollback)
	if err != nil {
		t.Fatal(err)
	}
	if result.Transaction.FailureCode() != "REVERSAL_INSUFFICIENT_BALANCE" {
		t.Fatal(result.Transaction.FailureCode())
	}
}
func TestPendingExpiryPublishesRejectionIntegration(t *testing.T) {
	ctx, db, s, wallet, player, now := financialFixture(t)
	refund := regressionInput(wallet, player, now, domain.WagerTransactionKindRefund, "25.00")
	refund.ReferenceExternalTransactionID = uuid.NewString()
	if _, err := s.Execute(ctx, refund); err != nil {
		t.Fatal(err)
	}
	// Force only this test's persisted retry clock/attempt count, without sleeping.
	if _, err := db.Pool.Exec(ctx, "UPDATE wager_transactions SET reference_attempts=5,reference_next_attempt_at=NOW() WHERE id=$1", refund.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryPendingReference(ctx, 30); err != nil {
		t.Fatal(err)
	}
	result, err := s.transactions.FindByProviderAndExternalID(ctx, db.Pool, refund.ProviderID, refund.ExternalTransactionID)
	if err != nil {
		t.Fatal(err)
	}
	if result.FailureCode() != "REFERENCE_NOT_FOUND" {
		t.Fatal(result.FailureCode())
	}
	var count int
	if err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM outbox WHERE aggregate_id=$1 AND event_type='WAGER_TRANSACTION_REJECTED'", refund.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("expiry event absent")
	}
}
func TestOpeningRollbackOnOutboxFailureIntegration(t *testing.T) {
	ctx, db, s, wallet, _, now := financialFixture(t)
	// The temporary trigger only rejects this test's new wallet; existing rows are untouched.
	id := uuid.NewString()
	sql := fmt.Sprintf(`CREATE FUNCTION test_reject_opening_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.payload->>'walletId' = '%s' THEN RAISE EXCEPTION 'test injected outbox failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER test_reject_opening_outbox BEFORE INSERT ON outbox FOR EACH ROW EXECUTE FUNCTION test_reject_opening_outbox();`, id)
	if _, err := db.Pool.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(ctx, "DROP TRIGGER test_reject_opening_outbox ON outbox; DROP FUNCTION test_reject_opening_outbox();"); err != nil {
			t.Error(err)
		}
	})
	money, _ := domain.Parse("50.00", "BRL")
	create := NewCreateWalletService(db, s.wallets, s.transactions, s.ledger, s.outbox)
	if _, err := create.Execute(ctx, CreateWalletInput{ID: id, PlayerID: uuid.NewString(), InitialBalance: money, CreatedAt: now}); err == nil {
		t.Fatal("expected injected failure")
	}
	for _, table := range []string{"wallets", "wager_transactions", "wallet_ledger_entries"} {
		column := "wallet_id"
		if table == "wallets" {
			column = "id"
		}
		var count int
		if err := db.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+column+"=$1", id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("partial opening in %s", table)
		}
	}
	_ = wallet
}
