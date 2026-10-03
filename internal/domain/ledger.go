package domain

import (
	"fmt"
	"strings"
	"time"
)

type LedgerDirection string

const (
	LedgerDebit  LedgerDirection = "DEBIT"
	LedgerCredit LedgerDirection = "CREDIT"
)

// WalletLedgerEntry is an immutable record of one positive balance movement.
// Database constraints must additionally enforce append-only storage and uniqueness.
type WalletLedgerEntry struct {
	state LedgerState
}

// LedgerState is a value copy for persistence and queries, not a mutable entry.
type LedgerState struct {
	ID            string          `json:"id"`
	WalletID      string          `json:"walletId"`
	TransactionID string          `json:"transactionId"`
	Direction     LedgerDirection `json:"direction"`
	Money         Money           `json:"money"`
	BalanceBefore Money           `json:"balanceBefore"`
	BalanceAfter  Money           `json:"balanceAfter"`
	CreatedAt     time.Time       `json:"createdAt"`
}

// NewLedger validates the full accounting equation before constructing an entry.
// It does not debit or credit a Wallet. Both must be saved in one SQL transaction.
func NewLedger(s LedgerState) (WalletLedgerEntry, error) {
	s.CreatedAt = s.CreatedAt.UTC()
	if err := validateLedgerState(s); err != nil {
		return WalletLedgerEntry{}, err
	}
	return WalletLedgerEntry{state: s}, nil
}

// RestoreLedger validates saved data without replaying a movement or emitting events.
func RestoreLedger(s LedgerState) (WalletLedgerEntry, error) {
	if err := validateLedgerState(s); err != nil {
		return WalletLedgerEntry{}, err
	}
	return WalletLedgerEntry{state: s}, nil
}

// State returns a validated copy. The zero Go value is rejected.
func (e WalletLedgerEntry) State() (LedgerState, error) {
	if err := validateLedgerState(e.state); err != nil {
		return LedgerState{}, err
	}
	return e.state, nil
}

func validateLedgerState(s LedgerState) error {
	for _, id := range []string{s.ID, s.WalletID, s.TransactionID} {
		if id == "" || strings.TrimSpace(id) != id {
			return fmt.Errorf("%w: ledger identities required without surrounding whitespace", ErrInvalid)
		}
	}
	if s.CreatedAt.IsZero() {
		return fmt.Errorf("%w: ledger timestamp required", ErrInvalid)
	}
	for _, m := range []Money{s.Money, s.BalanceBefore, s.BalanceAfter} {
		if err := m.ValidateExternal(); err != nil {
			return fmt.Errorf("%w: invalid ledger money or balance", err)
		}
	}
	if s.Money.Units() == 0 {
		return fmt.Errorf("%w: ledger movement must be positive", ErrInvalid)
	}
	if s.Money.Currency() != s.BalanceBefore.Currency() || s.Money.Currency() != s.BalanceAfter.Currency() {
		return ErrCurrency
	}
	var expected Money
	var err error
	switch s.Direction {
	case LedgerDebit:
		expected, err = s.BalanceBefore.Sub(s.Money)
	case LedgerCredit:
		expected, err = s.BalanceBefore.Add(s.Money)
	default:
		return fmt.Errorf("%w: unknown ledger direction", ErrInvalid)
	}
	if err != nil {
		return err
	}
	if expected != s.BalanceAfter {
		return fmt.Errorf("%w: ledger balance equation does not match", ErrInvalid)
	}
	return nil
}
