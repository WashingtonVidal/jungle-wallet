package domain_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"jungle-wallet/internal/domain"
)

func ledgerMoney(t *testing.T, amount, currency string) domain.Money {
	t.Helper()
	m, err := domain.Parse(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func ledgerFixture(t *testing.T) domain.LedgerState {
	t.Helper()
	return domain.LedgerState{
		ID: "entry-1", WalletID: "wallet-1", TransactionID: "transaction-1",
		Direction:     domain.LedgerDebit,
		Money:         ledgerMoney(t, "80.00", "BRL"),
		BalanceBefore: ledgerMoney(t, "100.00", "BRL"),
		BalanceAfter:  ledgerMoney(t, "20.00", "BRL"),
		CreatedAt:     time.Date(2026, 10, 3, 13, 0, 0, 0, time.UTC),
	}
}

func TestLedgerAcceptsExactMovements(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		direction             domain.LedgerDirection
		before, amount, after string
	}{
		{"debit", domain.LedgerDebit, "100.00", "80.00", "20.00"},
		{"credit", domain.LedgerCredit, "20.00", "80.00", "100.00"},
		{"debit entire balance", domain.LedgerDebit, "100.00", "100.00", "0.00"},
		{"positive opening", domain.LedgerCredit, "0.00", "100.00", "100.00"},
		{"one cent", domain.LedgerCredit, "0.10", "0.01", "0.11"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ledgerFixture(t)
			s.Direction = tc.direction
			s.BalanceBefore = ledgerMoney(t, tc.before, "BRL")
			s.Money = ledgerMoney(t, tc.amount, "BRL")
			s.BalanceAfter = ledgerMoney(t, tc.after, "BRL")
			entry, err := domain.NewLedger(s)
			if err != nil {
				t.Fatal(err)
			}
			got, err := entry.State()
			if err != nil || got != s {
				t.Fatal("entry differs from validated input", got, err)
			}
		})
	}
}

func TestLedgerRejectsInvalidRecordsOnCreateAndRestore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*domain.LedgerState)
		want   error
	}{
		{"missing entry ID", func(s *domain.LedgerState) { s.ID = "" }, domain.ErrInvalid},
		{"missing wallet ID", func(s *domain.LedgerState) { s.WalletID = "" }, domain.ErrInvalid},
		{"missing transaction ID", func(s *domain.LedgerState) { s.TransactionID = "" }, domain.ErrInvalid},
		{"whitespace ID", func(s *domain.LedgerState) { s.ID = " entry " }, domain.ErrInvalid},
		{"missing timestamp", func(s *domain.LedgerState) { s.CreatedAt = time.Time{} }, domain.ErrInvalid},
		{"unknown direction", func(s *domain.LedgerState) { s.Direction = "LOSS" }, domain.ErrInvalid},
		{"missing direction", func(s *domain.LedgerState) { s.Direction = "" }, domain.ErrInvalid},
		{"zero amount", func(s *domain.LedgerState) { s.Money = ledgerMoney(t, "0.00", "BRL") }, domain.ErrInvalid},
		{"negative amount", func(s *domain.LedgerState) { s.Money = ledgerMoney(t, "-80.00", "BRL") }, domain.ErrInvalid},
		{"negative before", func(s *domain.LedgerState) { s.BalanceBefore = ledgerMoney(t, "-100.00", "BRL") }, domain.ErrInvalid},
		{"negative after", func(s *domain.LedgerState) { s.BalanceAfter = ledgerMoney(t, "-20.00", "BRL") }, domain.ErrInvalid},
		{"uninitialized amount", func(s *domain.LedgerState) { s.Money = domain.Money{} }, domain.ErrInvalid},
		{"uninitialized before", func(s *domain.LedgerState) { s.BalanceBefore = domain.Money{} }, domain.ErrInvalid},
		{"uninitialized after", func(s *domain.LedgerState) { s.BalanceAfter = domain.Money{} }, domain.ErrInvalid},
		{"amount currency mismatch", func(s *domain.LedgerState) { s.Money = ledgerMoney(t, "80.00", "USD") }, domain.ErrCurrency},
		{"before currency mismatch", func(s *domain.LedgerState) { s.BalanceBefore = ledgerMoney(t, "100.00", "USD") }, domain.ErrCurrency},
		{"after currency mismatch", func(s *domain.LedgerState) { s.BalanceAfter = ledgerMoney(t, "20.00", "EUR") }, domain.ErrCurrency},
		{"incorrect debit result", func(s *domain.LedgerState) { s.BalanceAfter = ledgerMoney(t, "30.00", "BRL") }, domain.ErrInvalid},
		{"incorrect credit result", func(s *domain.LedgerState) { s.Direction = domain.LedgerCredit }, domain.ErrInvalid},
		{"overspending disguised as zero", func(s *domain.LedgerState) {
			s.Money = ledgerMoney(t, "101.00", "BRL")
			s.BalanceAfter = ledgerMoney(t, "0.00", "BRL")
		}, domain.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ledgerFixture(t)
			tc.mutate(&s)
			for _, construct := range []func(domain.LedgerState) (domain.WalletLedgerEntry, error){domain.NewLedger, domain.RestoreLedger} {
				entry, err := construct(s)
				if !errors.Is(err, tc.want) {
					t.Fatalf("wanted %v, got %v", tc.want, err)
				}
				if _, err := entry.State(); !errors.Is(err, domain.ErrInvalid) {
					t.Fatal("invalid construction returned usable entry")
				}
			}
		})
	}
}

func TestLedgerOverflowRejected(t *testing.T) {
	s := ledgerFixture(t)
	s.Direction = domain.LedgerCredit
	s.BalanceBefore, _ = domain.FromUnits(math.MaxInt64, "BRL")
	s.Money = ledgerMoney(t, "0.01", "BRL")
	s.BalanceAfter = s.BalanceBefore
	if _, err := domain.NewLedger(s); !errors.Is(err, domain.ErrOverflow) {
		t.Fatal("overflow not rejected", err)
	}
	if _, err := domain.RestoreLedger(s); !errors.Is(err, domain.ErrOverflow) {
		t.Fatal("corrupt overflowing record restored", err)
	}
	// Exactly reaching the upper limit remains valid.
	s.BalanceBefore, _ = domain.FromUnits(math.MaxInt64-1, "BRL")
	if _, err := domain.NewLedger(s); err != nil {
		t.Fatal("valid boundary rejected", err)
	}
}

func TestLedgerIsImmutableAndRestorePreservesState(t *testing.T) {
	original := ledgerFixture(t)
	entry, err := domain.NewLedger(original)
	if err != nil {
		t.Fatal(err)
	}
	copyOfState, err := entry.State()
	if err != nil {
		t.Fatal(err)
	}
	copyOfState.ID = "changed"
	copyOfState.BalanceAfter = ledgerMoney(t, "999.00", "BRL")
	stillOriginal, err := entry.State()
	if err != nil || stillOriginal != original {
		t.Fatal("changing snapshot changed immutable entry", err)
	}
	// Restoring keeps even the persisted timezone representation; no operation is replayed.
	saved := original
	saved.CreatedAt = saved.CreatedAt.In(time.FixedZone("UTC-3", -10800))
	restored, err := domain.RestoreLedger(saved)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.State()
	if err != nil || got != saved {
		t.Fatal("restore changed persisted record", err)
	}
	newEntry, err := domain.NewLedger(saved)
	if err != nil {
		t.Fatal(err)
	}
	created, err := newEntry.State()
	if err != nil || created.CreatedAt.Location() != time.UTC || !created.CreatedAt.Equal(saved.CreatedAt) {
		t.Fatal("new entry UTC normalization failed", err)
	}
}

func TestLedgerZeroValueRejected(t *testing.T) {
	var entry domain.WalletLedgerEntry
	if _, err := entry.State(); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestLedgerJSONKeepsMoneyAsStrings(t *testing.T) {
	entry, err := domain.NewLedger(ledgerFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	s, err := entry.State()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Direction string            `json:"direction"`
		Money     domain.MoneyInput `json:"money"`
		Before    domain.MoneyInput `json:"balanceBefore"`
		After     domain.MoneyInput `json:"balanceAfter"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Direction != "DEBIT" || decoded.Money.Amount != "80.00" || decoded.Before.Amount != "100.00" || decoded.After.Amount != "20.00" || decoded.Money.Currency != "BRL" {
		t.Fatal(string(b))
	}
}

func TestLedgerRecordingDoesNotDebitWalletAgain(t *testing.T) {
	s := ledgerFixture(t)
	w, err := domain.NewWallet(s.WalletID, "player", s.BalanceBefore, s.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Debit(s.Money, s.CreatedAt); err != nil {
		t.Fatal(err)
	}
	afterDebit, err := w.State()
	if err != nil {
		t.Fatal(err)
	}
	if afterDebit.Balance != s.BalanceAfter {
		t.Fatal("unexpected wallet balance")
	}
	entry, err := domain.NewLedger(s)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := entry.State()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.RestoreLedger(saved); err != nil {
		t.Fatal(err)
	}
	got, err := w.State()
	if err != nil || got != afterDebit {
		t.Fatal("recording or restoring changed wallet", err)
	}
}
