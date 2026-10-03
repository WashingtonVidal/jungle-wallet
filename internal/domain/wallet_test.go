package domain_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"jungle-wallet/internal/domain"
)

func walletMoney(t *testing.T, amount, currency string) domain.Money {
	t.Helper()
	m, err := domain.Parse(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func walletSnapshot(t *testing.T, w *domain.Wallet) domain.WalletState {
	t.Helper()
	s, err := w.State()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func walletFixture(t *testing.T) (*domain.Wallet, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	w, err := domain.NewWallet("wallet-1", "player-1", walletMoney(t, "100.00", "BRL"), now)
	if err != nil {
		t.Fatal(err)
	}
	return w, now
}

func TestWalletDebitRejectsInsufficientBalance(t *testing.T) {
	w, now := walletFixture(t)
	bet := walletMoney(t, "80.00", "BRL")
	if err := w.Debit(bet, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	before := walletSnapshot(t, w)
	if before.Balance.Amount() != "20.00" || before.Version != 2 {
		t.Fatal(before)
	}
	if err := w.Debit(bet, now.Add(2*time.Second)); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatal("expected insufficient balance", err)
	}
	if after := walletSnapshot(t, w); after != before {
		t.Fatal("rejected debit changed wallet", after)
	}
}

func TestWalletCreditDebitAndVersion(t *testing.T) {
	w, now := walletFixture(t)
	initial := walletSnapshot(t, w)
	if initial.Version != 1 || initial.ID != "wallet-1" || initial.PlayerID != "player-1" || initial.CreatedAt != now || initial.UpdatedAt != now {
		t.Fatal(initial)
	}
	if err := w.Credit(walletMoney(t, "25.00", "BRL"), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	credited := walletSnapshot(t, w)
	if credited.Balance.Amount() != "125.00" || credited.Version != 2 || credited.UpdatedAt != now.Add(time.Second) || credited.CreatedAt != initial.CreatedAt {
		t.Fatal(credited)
	}
	if err := w.Debit(walletMoney(t, "125.00", "BRL"), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	s := walletSnapshot(t, w)
	if s.Balance.Amount() != "0.00" || s.Version != 3 || s.CreatedAt != now || s.UpdatedAt != now.Add(2*time.Second) {
		t.Fatal(s)
	}
	if err := w.Debit(walletMoney(t, "0.01", "BRL"), now.Add(3*time.Second)); !errors.Is(err, domain.ErrInsufficient) {
		t.Fatal(err)
	}
	if walletSnapshot(t, w) != s {
		t.Fatal("empty-wallet debit changed state")
	}
}

func TestWalletInitialZeroAndCurrency(t *testing.T) {
	_, now := walletFixture(t)
	for _, currency := range []string{"BRL", "USD", "EUR"} {
		zero, err := domain.Zero(currency)
		if err != nil {
			t.Fatal(err)
		}
		w, err := domain.NewWallet("wallet", "player", zero, now)
		if err != nil {
			t.Fatal(err)
		}
		if s := walletSnapshot(t, w); s.Balance.Units() != 0 || s.Balance.Currency() != currency || s.Version != 1 {
			t.Fatal(s)
		}
	}
}

func TestWalletCreationRejectsInvalidState(t *testing.T) {
	_, now := walletFixture(t)
	valid := walletMoney(t, "1.00", "BRL")
	negative := walletMoney(t, "-1.00", "BRL")
	for _, tc := range []struct {
		name, id, player string
		balance          domain.Money
		at               time.Time
	}{
		{"empty wallet ID", "", "player", valid, now},
		{"whitespace wallet ID", "  ", "player", valid, now},
		{"empty player ID", "wallet", "", valid, now},
		{"whitespace player ID", "wallet", " player ", valid, now},
		{"negative balance", "wallet", "player", negative, now},
		{"uninitialized money", "wallet", "player", domain.Money{}, now},
		{"zero timestamp", "wallet", "player", valid, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, err := domain.NewWallet(tc.id, tc.player, tc.balance, tc.at)
			if w != nil || !errors.Is(err, domain.ErrInvalid) {
				t.Fatal("invalid wallet accepted", w, err)
			}
		})
	}
}

func TestWalletFailedMovementsPreserveEveryField(t *testing.T) {
	_, now := walletFixture(t)
	for _, tc := range []struct {
		name   string
		amount domain.Money
		at     time.Time
		want   error
	}{
		{"negative", walletMoney(t, "-0.01", "BRL"), now, domain.ErrInvalid},
		{"zero", walletMoney(t, "0.00", "BRL"), now, domain.ErrInvalid},
		{"uninitialized", domain.Money{}, now, domain.ErrInvalid},
		{"different currency", walletMoney(t, "1.00", "USD"), now, domain.ErrCurrency},
		{"past timestamp", walletMoney(t, "1.00", "BRL"), now.Add(-time.Second), domain.ErrInvalid},
		{"zero timestamp", walletMoney(t, "1.00", "BRL"), time.Time{}, domain.ErrInvalid},
	} {
		for _, debit := range []bool{false, true} {
			name := tc.name + "/credit"
			if debit {
				name = tc.name + "/debit"
			}
			t.Run(name, func(t *testing.T) {
				w, _ := walletFixture(t)
				before := walletSnapshot(t, w)
				var err error
				if debit {
					err = w.Debit(tc.amount, tc.at)
				} else {
					err = w.Credit(tc.amount, tc.at)
				}
				if !errors.Is(err, tc.want) {
					t.Fatalf("wanted %v, got %v", tc.want, err)
				}
				if walletSnapshot(t, w) != before {
					t.Fatal("failed operation mutated state")
				}
			})
		}
	}
}

func TestWalletRestoreDoesNotReplayOrShareState(t *testing.T) {
	w, now := walletFixture(t)
	if err := w.Debit(walletMoney(t, "80.00", "BRL"), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	saved := walletSnapshot(t, w)
	restored, err := domain.RestoreWallet(saved)
	if err != nil {
		t.Fatal(err)
	}
	if walletSnapshot(t, restored) != saved {
		t.Fatal("restore changed persisted state")
	}
	copyOfState := saved
	copyOfState.ID = "other"
	copyOfState.Balance = walletMoney(t, "999.00", "BRL")
	if walletSnapshot(t, restored) != saved {
		t.Fatal("snapshot exposed mutable state")
	}
	if err := restored.Credit(walletMoney(t, "5.00", "BRL"), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if walletSnapshot(t, w) != saved {
		t.Fatal("restored wallet shares mutable state with original")
	}
	if walletSnapshot(t, restored).Version != 3 || walletSnapshot(t, restored).Balance.Amount() != "25.00" {
		t.Fatal("restore did not preserve usable version and balance")
	}
}

func TestWalletRestoreRejectsCorruptState(t *testing.T) {
	w, now := walletFixture(t)
	base := walletSnapshot(t, w)
	for _, tc := range []struct {
		name    string
		corrupt func(*domain.WalletState)
	}{
		{"zero version", func(s *domain.WalletState) { s.Version = 0 }},
		{"negative version", func(s *domain.WalletState) { s.Version = -1 }},
		{"negative balance", func(s *domain.WalletState) { s.Balance = walletMoney(t, "-0.01", "BRL") }},
		{"missing identity", func(s *domain.WalletState) { s.ID = "" }},
		{"missing player", func(s *domain.WalletState) { s.PlayerID = "" }},
		{"zero money", func(s *domain.WalletState) { s.Balance = domain.Money{} }},
		{"zero creation", func(s *domain.WalletState) { s.CreatedAt = time.Time{} }},
		{"zero update", func(s *domain.WalletState) { s.UpdatedAt = time.Time{} }},
		{"update before creation", func(s *domain.WalletState) { s.UpdatedAt = now.Add(-time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.corrupt(&s)
			got, err := domain.RestoreWallet(s)
			if got != nil || !errors.Is(err, domain.ErrInvalid) {
				t.Fatal("corrupt state restored", got, err)
			}
		})
	}
}

func TestWalletOverflowLeavesStateUnchanged(t *testing.T) {
	w, now := walletFixture(t)
	s := walletSnapshot(t, w)
	s.Balance, _ = domain.FromUnits(math.MaxInt64, "BRL")
	w, err := domain.RestoreWallet(s)
	if err != nil {
		t.Fatal(err)
	}
	one := walletMoney(t, "0.01", "BRL")
	if err := w.Credit(one, now); !errors.Is(err, domain.ErrOverflow) {
		t.Fatal(err)
	}
	if walletSnapshot(t, w) != s {
		t.Fatal("balance overflow mutated state")
	}
	s.Version = math.MaxInt64
	w, err = domain.RestoreWallet(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []func(domain.Money, time.Time) error{w.Credit, w.Debit} {
		if err := op(one, now); !errors.Is(err, domain.ErrOverflow) {
			t.Fatal(err)
		}
		if walletSnapshot(t, w) != s {
			t.Fatal("version overflow mutated state")
		}
	}
}

func TestWalletUninitializedAndNilRejected(t *testing.T) {
	_, now := walletFixture(t)
	one := walletMoney(t, "1.00", "BRL")
	for _, w := range []*domain.Wallet{nil, {}} {
		if _, err := w.State(); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal(err)
		}
		if err := w.Credit(one, now); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal(err)
		}
		if err := w.Debit(one, now); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal(err)
		}
	}
}

func TestWalletTimesNormalizedToUTC(t *testing.T) {
	local := time.Date(2026, 10, 3, 9, 0, 0, 0, time.FixedZone("UTC-3", -3*60*60))
	w, err := domain.NewWallet("wallet", "player", walletMoney(t, "1.00", "BRL"), local)
	if err != nil {
		t.Fatal(err)
	}
	s := walletSnapshot(t, w)
	if s.CreatedAt.Location() != time.UTC || !s.CreatedAt.Equal(local) {
		t.Fatal(s)
	}
	// Equal timestamps are allowed: two operations may occur at the same clock tick.
	if err := w.Credit(walletMoney(t, "0.01", "BRL"), local); err != nil {
		t.Fatal(err)
	}
	s = walletSnapshot(t, w)
	if s.UpdatedAt.Location() != time.UTC || !s.UpdatedAt.Equal(local) || s.Version != 2 {
		t.Fatal(s)
	}
}
