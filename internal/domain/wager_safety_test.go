package domain

import (
	"errors"
	"testing"
	"time"
)

func TestWagerResultBalanceIsImmutable(t *testing.T) {
	transaction := newTestWagerTransaction(t)
	balance, _ := Parse("75.00", "BRL")
	if err := transaction.MarkProcessed(balance, time.Now()); err != nil {
		t.Fatal(err)
	}
	returned := transaction.ResultBalance()
	*returned, _ = Parse("0.00", "BRL")
	if transaction.ResultBalance().Amount() != "75.00" {
		t.Fatal("external pointer changed terminal balance")
	}
}
func TestWagerRejectsZeroStateAndInvalidResult(t *testing.T) {
	var zero WagerTransaction
	if err := zero.Reject("INVALID", time.Now()); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("zero state: %v", err)
	}
	transaction := newTestWagerTransaction(t)
	if err := transaction.MarkProcessed(Money{}, time.Now()); err == nil {
		t.Fatal("accepted zero Money")
	}
	negative, _ := FromUnits(-1, "BRL")
	if err := transaction.MarkProcessed(negative, time.Now()); err == nil {
		t.Fatal("accepted negative result")
	}
	other, _ := Parse("75.00", "USD")
	if err := transaction.MarkProcessed(other, time.Now()); !errors.Is(err, ErrCurrency) {
		t.Fatal(err)
	}
	if transaction.Status() != WagerTransactionStatusPending {
		t.Fatal("failed transition mutated state")
	}
}
