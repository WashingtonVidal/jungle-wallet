package domain

import (
	"fmt"
	"time"
)

// WagerMovement describes the financial effect of a wager operation.
type WagerMovement struct {
	Direction     LedgerDirection
	BalanceBefore Money
	BalanceAfter  Money
}

// ApplyWagerMovement applies the direct financial effect of BET, WIN or LOSS.
//
// REFUND and ROLLBACK require the referenced transaction to be resolved first,
// so they are intentionally handled separately.
func ApplyWagerMovement(
	wallet *Wallet,
	kind WagerTransactionKind,
	money Money,
	now time.Time,
) (*WagerMovement, error) {
	if wallet == nil {
		return nil, fmt.Errorf("%w: nil wallet", ErrInvalid)
	}

	stateBefore, err := wallet.State()
	if err != nil {
		return nil, err
	}

	if money.Currency() != stateBefore.Balance.Currency() {
		return nil, ErrCurrency
	}

	switch kind {
	case WagerTransactionKindBet:
		if money.Units() <= 0 {
			return nil, fmt.Errorf("%w: BET amount must be positive", ErrInvalid)
		}

		if err := wallet.Debit(money, now); err != nil {
			return nil, err
		}

		stateAfter, err := wallet.State()
		if err != nil {
			return nil, err
		}

		return &WagerMovement{
			Direction:     LedgerDebit,
			BalanceBefore: stateBefore.Balance,
			BalanceAfter:  stateAfter.Balance,
		}, nil

	case WagerTransactionKindWin:
		if money.Units() <= 0 {
			return nil, fmt.Errorf("%w: WIN amount must be positive", ErrInvalid)
		}

		if err := wallet.Credit(money, now); err != nil {
			return nil, err
		}

		stateAfter, err := wallet.State()
		if err != nil {
			return nil, err
		}

		return &WagerMovement{
			Direction:     LedgerCredit,
			BalanceBefore: stateBefore.Balance,
			BalanceAfter:  stateAfter.Balance,
		}, nil

	case WagerTransactionKindLoss:
		if money.Units() != 0 {
			return nil, fmt.Errorf("%w: LOSS amount must be zero", ErrInvalid)
		}

		// LOSS is a successfully processed operation without financial movement.
		return nil, nil

	default:
		return nil, fmt.Errorf(
			"%w: operation requires reference-aware processing",
			ErrInvalidTransactionKind,
		)
	}
}

// ApplyReversalMovement applies the financial effect of REFUND or ROLLBACK
// after the referenced transaction has already been resolved and validated.
//
// REFUND always reverses a BET, therefore it credits the wallet.
//
// ROLLBACK applies the opposite movement of the referenced operation:
// BET -> CREDIT
// WIN -> DEBIT
// REFUND -> DEBIT
func ApplyReversalMovement(
	wallet *Wallet,
	kind WagerTransactionKind,
	referencedKind WagerTransactionKind,
	money Money,
	now time.Time,
) (*WagerMovement, error) {
	if wallet == nil {
		return nil, fmt.Errorf("%w: nil wallet", ErrInvalid)
	}

	stateBefore, err := wallet.State()
	if err != nil {
		return nil, err
	}

	if money.Currency() != stateBefore.Balance.Currency() {
		return nil, ErrCurrency
	}

	if money.Units() <= 0 {
		return nil, fmt.Errorf("%w: reversal amount must be positive", ErrInvalid)
	}

	var direction LedgerDirection

	switch kind {
	case WagerTransactionKindRefund:
		if referencedKind != WagerTransactionKindBet {
			return nil, fmt.Errorf(
				"%w: REFUND must reference BET",
				ErrInvalidTransactionKind,
			)
		}

		if err := wallet.Credit(money, now); err != nil {
			return nil, err
		}

		direction = LedgerCredit

	case WagerTransactionKindRollback:
		switch referencedKind {
		case WagerTransactionKindBet:
			if err := wallet.Credit(money, now); err != nil {
				return nil, err
			}
			direction = LedgerCredit

		case WagerTransactionKindWin, WagerTransactionKindRefund:
			if err := wallet.Debit(money, now); err != nil {
				return nil, err
			}
			direction = LedgerDebit

		default:
			return nil, fmt.Errorf(
				"%w: ROLLBACK cannot reverse referenced kind",
				ErrInvalidTransactionKind,
			)
		}

	default:
		return nil, fmt.Errorf(
			"%w: expected REFUND or ROLLBACK",
			ErrInvalidTransactionKind,
		)
	}

	stateAfter, err := wallet.State()
	if err != nil {
		return nil, err
	}

	return &WagerMovement{
		Direction:     direction,
		BalanceBefore: stateBefore.Balance,
		BalanceAfter:  stateAfter.Balance,
	}, nil
}
