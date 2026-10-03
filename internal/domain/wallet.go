package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Wallet controls changes to one player's balance. It is loaded and used by one
// SQL transaction at a time; database locking will coordinate different instances.
// Do not share a mutable Wallet between goroutines.
type Wallet struct {
	state WalletState
}

// WalletState is a copy used for persistence. Changing it cannot change a Wallet.
// Currency is carried by Balance, preventing two conflicting currency fields.
type WalletState struct {
	ID        string    `json:"id"`
	PlayerID  string    `json:"playerId"`
	Balance   Money     `json:"balance"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// NewWallet creates version 1, accepting a nonnegative initial balance.
// The application must persist OPENING and its ledger entry atomically with it.
func NewWallet(id, playerID string, initial Money, now time.Time) (*Wallet, error) {
	s := WalletState{
		ID: id, PlayerID: playerID, Balance: initial, Version: 1,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	if err := validateWalletState(s); err != nil {
		return nil, err
	}
	return &Wallet{state: s}, nil
}

// RestoreWallet validates persisted data without replaying any credit, debit,
// version increment or event. It preserves the original state exactly.
func RestoreWallet(s WalletState) (*Wallet, error) {
	if err := validateWalletState(s); err != nil {
		return nil, err
	}
	return &Wallet{state: s}, nil
}

// State returns a validated copy, never a pointer to the private state.
func (w *Wallet) State() (WalletState, error) {
	if w == nil {
		return WalletState{}, fmt.Errorf("%w: nil wallet", ErrInvalid)
	}
	if err := validateWalletState(w.state); err != nil {
		return WalletState{}, err
	}
	return w.state, nil
}

// Credit adds a positive amount. Failure leaves all state unchanged.
func (w *Wallet) Credit(amount Money, now time.Time) error {
	return w.move(amount, now, false)
}

// Debit subtracts a positive amount without allowing a negative balance.
// Failure leaves all state unchanged.
func (w *Wallet) Debit(amount Money, now time.Time) error {
	return w.move(amount, now, true)
}

func (w *Wallet) move(amount Money, now time.Time, debit bool) error {
	s, err := w.State()
	if err != nil {
		return err
	}
	if err := amount.ValidateExternal(); err != nil {
		return fmt.Errorf("%w: invalid movement amount", err)
	}
	if amount.Units() == 0 {
		return fmt.Errorf("%w: movement must be positive", ErrInvalid)
	}
	if now.IsZero() || now.Before(s.UpdatedAt) {
		return fmt.Errorf("%w: invalid movement timestamp", ErrInvalid)
	}
	if amount.Currency() != s.Balance.Currency() {
		return ErrCurrency
	}
	if s.Version == math.MaxInt64 {
		return fmt.Errorf("%w: wallet version", ErrOverflow)
	}
	var next Money
	if debit {
		if amount.Units() > s.Balance.Units() {
			return ErrInsufficient
		}
		next, err = s.Balance.Sub(amount)
	} else {
		next, err = s.Balance.Add(amount)
	}
	if err != nil {
		return err
	}
	// Assign only after every validation and arithmetic operation has succeeded.
	s.Balance = next
	s.Version++
	s.UpdatedAt = now.UTC()
	w.state = s
	return nil
}

func validateWalletState(s WalletState) error {
	if s.ID == "" || strings.TrimSpace(s.ID) != s.ID ||
		s.PlayerID == "" || strings.TrimSpace(s.PlayerID) != s.PlayerID {
		return fmt.Errorf("%w: wallet and player identities required without surrounding whitespace", ErrInvalid)
	}
	if err := s.Balance.ValidateExternal(); err != nil {
		return fmt.Errorf("%w: wallet balance", err)
	}
	if s.Version < 1 {
		return fmt.Errorf("%w: wallet version must be positive", ErrInvalid)
	}
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() || s.UpdatedAt.Before(s.CreatedAt) {
		return fmt.Errorf("%w: invalid wallet timestamps", ErrInvalid)
	}
	return nil
}
