package domain

import (
	"errors"
	"strings"
	"time"
)

type WagerTransactionKind string

const (
	WagerTransactionKindOpening  WagerTransactionKind = "OPENING"
	WagerTransactionKindBet      WagerTransactionKind = "BET"
	WagerTransactionKindWin      WagerTransactionKind = "WIN"
	WagerTransactionKindLoss     WagerTransactionKind = "LOSS"
	WagerTransactionKindRefund   WagerTransactionKind = "REFUND"
	WagerTransactionKindRollback WagerTransactionKind = "ROLLBACK"
)

type WagerTransactionStatus string

const (
	WagerTransactionStatusPending          WagerTransactionStatus = "PENDING"
	WagerTransactionStatusPendingReference WagerTransactionStatus = "PENDING_REFERENCE"
	WagerTransactionStatusProcessed        WagerTransactionStatus = "PROCESSED"
	WagerTransactionStatusRejected         WagerTransactionStatus = "REJECTED"
	WagerTransactionStatusFailed           WagerTransactionStatus = "FAILED"
)

var (
	ErrInvalidTransactionKind   = errors.New("invalid wager transaction kind")
	ErrInvalidTransactionStatus = errors.New("invalid wager transaction status")
	ErrInvalidTransaction       = errors.New("invalid wager transaction")
	ErrTerminalTransaction      = errors.New("wager transaction is already terminal")
	ErrFailureCodeRequired      = errors.New("failure code is required")
)

type WagerTransaction struct {
	id                             string
	externalTransactionID          string
	providerID                     string
	idempotencyKey                 string
	payloadHash                    string
	walletID                       string
	playerID                       string
	roundID                        string
	gameID                         string
	kind                           WagerTransactionKind
	money                          Money
	referenceExternalTransactionID string
	referenceTransactionID         string
	status                         WagerTransactionStatus
	failureCode                    string
	resultBalance                  *Money
	createdAt                      time.Time
	updatedAt                      time.Time
}

type NewWagerTransactionParams struct {
	ID                             string
	ExternalTransactionID          string
	ProviderID                     string
	IdempotencyKey                 string
	PayloadHash                    string
	WalletID                       string
	PlayerID                       string
	RoundID                        string
	GameID                         string
	Kind                           WagerTransactionKind
	Money                          Money
	ReferenceExternalTransactionID string
	CreatedAt                      time.Time
}

type RehydrateWagerTransactionParams struct {
	ID                             string
	ExternalTransactionID          string
	ProviderID                     string
	IdempotencyKey                 string
	PayloadHash                    string
	WalletID                       string
	PlayerID                       string
	RoundID                        string
	GameID                         string
	Kind                           WagerTransactionKind
	Money                          Money
	ReferenceExternalTransactionID string
	ReferenceTransactionID         string
	Status                         WagerTransactionStatus
	FailureCode                    string
	ResultBalance                  *Money
	CreatedAt                      time.Time
	UpdatedAt                      time.Time
}

func NewWagerTransaction(params NewWagerTransactionParams) (*WagerTransaction, error) {
	if params.Kind == WagerTransactionKindOpening {
		return nil, ErrInvalidTransactionKind
	}

	if !params.Kind.IsExternal() {
		return nil, ErrInvalidTransactionKind
	}

	if strings.TrimSpace(params.ID) == "" ||
		strings.TrimSpace(params.ExternalTransactionID) == "" ||
		strings.TrimSpace(params.ProviderID) == "" ||
		strings.TrimSpace(params.IdempotencyKey) == "" ||
		strings.TrimSpace(params.PayloadHash) == "" ||
		strings.TrimSpace(params.WalletID) == "" ||
		strings.TrimSpace(params.PlayerID) == "" ||
		strings.TrimSpace(params.RoundID) == "" ||
		strings.TrimSpace(params.GameID) == "" ||
		params.CreatedAt.IsZero() {
		return nil, ErrInvalidTransaction
	}

	return &WagerTransaction{
		id:                             params.ID,
		externalTransactionID:          params.ExternalTransactionID,
		providerID:                     params.ProviderID,
		idempotencyKey:                 params.IdempotencyKey,
		payloadHash:                    params.PayloadHash,
		walletID:                       params.WalletID,
		playerID:                       params.PlayerID,
		roundID:                        params.RoundID,
		gameID:                         params.GameID,
		kind:                           params.Kind,
		money:                          params.Money,
		referenceExternalTransactionID: params.ReferenceExternalTransactionID,
		status:                         WagerTransactionStatusPending,
		createdAt:                      params.CreatedAt,
		updatedAt:                      params.CreatedAt,
	}, nil
}

func RehydrateWagerTransaction(params RehydrateWagerTransactionParams) (*WagerTransaction, error) {
	if !params.Kind.IsValid() {
		return nil, ErrInvalidTransactionKind
	}

	if !params.Status.IsValid() {
		return nil, ErrInvalidTransactionStatus
	}

	if strings.TrimSpace(params.ID) == "" ||
		strings.TrimSpace(params.WalletID) == "" ||
		strings.TrimSpace(params.PlayerID) == "" ||
		params.CreatedAt.IsZero() ||
		params.UpdatedAt.IsZero() {
		return nil, ErrInvalidTransaction
	}

	return &WagerTransaction{
		id:                             params.ID,
		externalTransactionID:          params.ExternalTransactionID,
		providerID:                     params.ProviderID,
		idempotencyKey:                 params.IdempotencyKey,
		payloadHash:                    params.PayloadHash,
		walletID:                       params.WalletID,
		playerID:                       params.PlayerID,
		roundID:                        params.RoundID,
		gameID:                         params.GameID,
		kind:                           params.Kind,
		money:                          params.Money,
		referenceExternalTransactionID: params.ReferenceExternalTransactionID,
		referenceTransactionID:         params.ReferenceTransactionID,
		status:                         params.Status,
		failureCode:                    params.FailureCode,
		resultBalance:                  params.ResultBalance,
		createdAt:                      params.CreatedAt,
		updatedAt:                      params.UpdatedAt,
	}, nil
}

func (k WagerTransactionKind) IsValid() bool {
	switch k {
	case WagerTransactionKindOpening,
		WagerTransactionKindBet,
		WagerTransactionKindWin,
		WagerTransactionKindLoss,
		WagerTransactionKindRefund,
		WagerTransactionKindRollback:
		return true
	default:
		return false
	}
}

func (k WagerTransactionKind) IsExternal() bool {
	switch k {
	case WagerTransactionKindBet,
		WagerTransactionKindWin,
		WagerTransactionKindLoss,
		WagerTransactionKindRefund,
		WagerTransactionKindRollback:
		return true
	default:
		return false
	}
}

func (s WagerTransactionStatus) IsValid() bool {
	switch s {
	case WagerTransactionStatusPending,
		WagerTransactionStatusPendingReference,
		WagerTransactionStatusProcessed,
		WagerTransactionStatusRejected,
		WagerTransactionStatusFailed:
		return true
	default:
		return false
	}
}

func (s WagerTransactionStatus) IsTerminal() bool {
	switch s {
	case WagerTransactionStatusProcessed,
		WagerTransactionStatusRejected,
		WagerTransactionStatusFailed:
		return true
	default:
		return false
	}
}

func (t *WagerTransaction) ensureNotTerminal() error {
	if t.status.IsTerminal() {
		return ErrTerminalTransaction
	}

	return nil
}

func (t *WagerTransaction) MarkPendingReference(updatedAt time.Time) error {
	if err := t.ensureNotTerminal(); err != nil {
		return err
	}

	if updatedAt.IsZero() {
		return ErrInvalidTransaction
	}

	t.status = WagerTransactionStatusPendingReference
	t.updatedAt = updatedAt

	return nil
}

func (t *WagerTransaction) MarkProcessed(resultBalance Money, updatedAt time.Time) error {
	if err := t.ensureNotTerminal(); err != nil {
		return err
	}

	if updatedAt.IsZero() {
		return ErrInvalidTransaction
	}

	t.status = WagerTransactionStatusProcessed
	t.failureCode = ""
	t.resultBalance = &resultBalance
	t.updatedAt = updatedAt

	return nil
}

func (t *WagerTransaction) Reject(failureCode string, updatedAt time.Time) error {
	if err := t.ensureNotTerminal(); err != nil {
		return err
	}

	if strings.TrimSpace(failureCode) == "" {
		return ErrFailureCodeRequired
	}

	if updatedAt.IsZero() {
		return ErrInvalidTransaction
	}

	t.status = WagerTransactionStatusRejected
	t.failureCode = failureCode
	t.updatedAt = updatedAt

	return nil
}

func (t *WagerTransaction) Fail(failureCode string, updatedAt time.Time) error {
	if err := t.ensureNotTerminal(); err != nil {
		return err
	}

	if strings.TrimSpace(failureCode) == "" {
		return ErrFailureCodeRequired
	}

	if updatedAt.IsZero() {
		return ErrInvalidTransaction
	}

	t.status = WagerTransactionStatusFailed
	t.failureCode = failureCode
	t.updatedAt = updatedAt

	return nil
}

func (t *WagerTransaction) ResolveReference(transactionID string, updatedAt time.Time) error {
	if err := t.ensureNotTerminal(); err != nil {
		return err
	}

	if strings.TrimSpace(transactionID) == "" || updatedAt.IsZero() {
		return ErrInvalidTransaction
	}

	t.referenceTransactionID = transactionID
	t.status = WagerTransactionStatusPending
	t.updatedAt = updatedAt

	return nil
}

func (t *WagerTransaction) ID() string {
	return t.id
}

func (t *WagerTransaction) ExternalTransactionID() string {
	return t.externalTransactionID
}

func (t *WagerTransaction) ProviderID() string {
	return t.providerID
}

func (t *WagerTransaction) IdempotencyKey() string {
	return t.idempotencyKey
}

func (t *WagerTransaction) PayloadHash() string {
	return t.payloadHash
}

func (t *WagerTransaction) WalletID() string {
	return t.walletID
}

func (t *WagerTransaction) PlayerID() string {
	return t.playerID
}

func (t *WagerTransaction) RoundID() string {
	return t.roundID
}

func (t *WagerTransaction) GameID() string {
	return t.gameID
}

func (t *WagerTransaction) Kind() WagerTransactionKind {
	return t.kind
}

func (t *WagerTransaction) Money() Money {
	return t.money
}

func (t *WagerTransaction) ReferenceExternalTransactionID() string {
	return t.referenceExternalTransactionID
}

func (t *WagerTransaction) ReferenceTransactionID() string {
	return t.referenceTransactionID
}

func (t *WagerTransaction) Status() WagerTransactionStatus {
	return t.status
}

func (t *WagerTransaction) FailureCode() string {
	return t.failureCode
}

func (t *WagerTransaction) ResultBalance() *Money {
	return t.resultBalance
}

func (t *WagerTransaction) CreatedAt() time.Time {
	return t.createdAt
}

func (t *WagerTransaction) UpdatedAt() time.Time {
	return t.updatedAt
}
