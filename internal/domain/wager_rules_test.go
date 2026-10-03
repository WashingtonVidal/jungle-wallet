package domain

import (
	"errors"
	"testing"
	"time"
)

func testWalletWithBalance(t *testing.T, amount string) *Wallet {
	t.Helper()

	money, err := Parse(amount, "BRL")
	if err != nil {
		t.Fatalf("unexpected money error: %v", err)
	}

	wallet, err := NewWallet(
		"wallet-1",
		"player-1",
		money,
		time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected wallet error: %v", err)
	}

	return wallet
}

func TestApplyBetDebitsWallet(t *testing.T) {
	wallet := testWalletWithBalance(t, "100.00")

	bet, err := Parse("25.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	movement, err := ApplyWagerMovement(
		wallet,
		WagerTransactionKindBet,
		bet,
		time.Date(2026, 10, 3, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if movement == nil {
		t.Fatal("expected financial movement")
	}

	if movement.Direction != LedgerDebit {
		t.Fatalf("expected DEBIT, got %s", movement.Direction)
	}

	if movement.BalanceBefore.Amount() != "100.00" {
		t.Fatalf(
			"expected balance before 100.00, got %s",
			movement.BalanceBefore.Amount(),
		)
	}

	if movement.BalanceAfter.Amount() != "75.00" {
		t.Fatalf(
			"expected balance after 75.00, got %s",
			movement.BalanceAfter.Amount(),
		)
	}

	state, err := wallet.State()
	if err != nil {
		t.Fatal(err)
	}

	if state.Version != 2 {
		t.Fatalf("expected wallet version 2, got %d", state.Version)
	}
}

func TestApplyBetRejectsInsufficientBalance(t *testing.T) {
	wallet := testWalletWithBalance(t, "50.00")

	bet, err := Parse("80.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	_, err = ApplyWagerMovement(
		wallet,
		WagerTransactionKindBet,
		bet,
		time.Date(2026, 10, 3, 12, 1, 0, 0, time.UTC),
	)

	if !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected ErrInsufficient, got %v", err)
	}

	state, stateErr := wallet.State()
	if stateErr != nil {
		t.Fatal(stateErr)
	}

	if state.Balance.Amount() != "50.00" {
		t.Fatalf(
			"failed BET changed balance to %s",
			state.Balance.Amount(),
		)
	}

	if state.Version != 1 {
		t.Fatalf(
			"failed BET changed version to %d",
			state.Version,
		)
	}
}

func TestApplyWinCreditsWallet(t *testing.T) {
	wallet := testWalletWithBalance(t, "100.00")

	win, err := Parse("30.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	movement, err := ApplyWagerMovement(
		wallet,
		WagerTransactionKindWin,
		win,
		time.Date(2026, 10, 3, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if movement.Direction != LedgerCredit {
		t.Fatalf("expected CREDIT, got %s", movement.Direction)
	}

	if movement.BalanceAfter.Amount() != "130.00" {
		t.Fatalf(
			"expected 130.00, got %s",
			movement.BalanceAfter.Amount(),
		)
	}
}

func TestApplyLossDoesNotChangeWallet(t *testing.T) {
	wallet := testWalletWithBalance(t, "100.00")

	loss, err := Parse("0.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	movement, err := ApplyWagerMovement(
		wallet,
		WagerTransactionKindLoss,
		loss,
		time.Date(2026, 10, 3, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if movement != nil {
		t.Fatal("LOSS must not create financial movement")
	}

	state, err := wallet.State()
	if err != nil {
		t.Fatal(err)
	}

	if state.Balance.Amount() != "100.00" {
		t.Fatalf("LOSS changed balance to %s", state.Balance.Amount())
	}

	if state.Version != 1 {
		t.Fatalf("LOSS changed wallet version to %d", state.Version)
	}
}

func TestRefundCreditsOriginalBet(t *testing.T) {
	wallet := testWalletWithBalance(t, "20.00")

	refund, err := Parse("80.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	movement, err := ApplyReversalMovement(
		wallet,
		WagerTransactionKindRefund,
		WagerTransactionKindBet,
		refund,
		time.Date(2026, 10, 3, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if movement.Direction != LedgerCredit {
		t.Fatalf("expected CREDIT, got %s", movement.Direction)
	}

	if movement.BalanceAfter.Amount() != "100.00" {
		t.Fatalf(
			"expected 100.00, got %s",
			movement.BalanceAfter.Amount(),
		)
	}
}

func TestRollbackBetCreditsWallet(t *testing.T) {
	wallet := testWalletWithBalance(t, "20.00")

	amount, err := Parse("80.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	movement, err := ApplyReversalMovement(
		wallet,
		WagerTransactionKindRollback,
		WagerTransactionKindBet,
		amount,
		time.Date(2026, 10, 3, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if movement.Direction != LedgerCredit {
		t.Fatalf("expected CREDIT, got %s", movement.Direction)
	}

	if movement.BalanceAfter.Amount() != "100.00" {
		t.Fatalf(
			"expected 100.00, got %s",
			movement.BalanceAfter.Amount(),
		)
	}
}

func TestRollbackWinDebitsWallet(t *testing.T) {
	wallet := testWalletWithBalance(t, "130.00")

	amount, err := Parse("30.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	movement, err := ApplyReversalMovement(
		wallet,
		WagerTransactionKindRollback,
		WagerTransactionKindWin,
		amount,
		time.Date(2026, 10, 3, 12, 1, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if movement.Direction != LedgerDebit {
		t.Fatalf("expected DEBIT, got %s", movement.Direction)
	}

	if movement.BalanceAfter.Amount() != "100.00" {
		t.Fatalf(
			"expected 100.00, got %s",
			movement.BalanceAfter.Amount(),
		)
	}
}

func TestRollbackRejectsDebitWhenBalanceIsInsufficient(t *testing.T) {
	wallet := testWalletWithBalance(t, "10.00")

	amount, err := Parse("30.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}

	_, err = ApplyReversalMovement(
		wallet,
		WagerTransactionKindRollback,
		WagerTransactionKindWin,
		amount,
		time.Date(2026, 10, 3, 12, 1, 0, 0, time.UTC),
	)

	if !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected ErrInsufficient, got %v", err)
	}
}
