package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/postgres"
)

type CreateWalletInput struct {
	ID             string
	PlayerID       string
	InitialBalance domain.Money
	CreatedAt      time.Time
}

type CreateWalletResult struct {
	Wallet *domain.Wallet
}

type CreateWalletService struct {
	db           *postgres.DB
	wallets      *postgres.WalletRepository
	transactions *postgres.WagerTransactionRepository
	ledger       *postgres.LedgerRepository
	outbox       *postgres.OutboxRepository
}

func NewCreateWalletService(
	db *postgres.DB,
	wallets *postgres.WalletRepository,
	transactions *postgres.WagerTransactionRepository,
	ledger *postgres.LedgerRepository,
	outbox *postgres.OutboxRepository,
) *CreateWalletService {
	return &CreateWalletService{
		db:           db,
		wallets:      wallets,
		transactions: transactions,
		ledger:       ledger,
		outbox:       outbox,
	}
}

func (s *CreateWalletService) Execute(
	ctx context.Context,
	input CreateWalletInput,
) (*CreateWalletResult, error) {
	if input.ID == "" ||
		input.PlayerID == "" ||
		input.CreatedAt.IsZero() {
		return nil, fmt.Errorf("%w: invalid create wallet input", domain.ErrInvalid)
	}

	if err := input.InitialBalance.ValidateExternal(); err != nil {
		return nil, err
	}

	wallet, err := domain.NewWallet(
		input.ID,
		input.PlayerID,
		input.InitialBalance,
		input.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.wallets.Insert(ctx, tx, wallet); err != nil {
			return err
		}

		if input.InitialBalance.Units() == 0 {
			return nil
		}

		opening, err := domain.NewOpeningTransaction(
			domain.NewOpeningTransactionParams{
				ID:        uuid.NewString(),
				WalletID:  input.ID,
				PlayerID:  input.PlayerID,
				Money:     input.InitialBalance,
				CreatedAt: input.CreatedAt,
			},
		)
		if err != nil {
			return err
		}

		if err := s.transactions.Insert(ctx, tx, opening); err != nil {
			return err
		}

		zero, err := domain.Zero(input.InitialBalance.Currency())
		if err != nil {
			return err
		}

		entry, err := domain.NewLedger(domain.LedgerState{
			ID:            uuid.NewString(),
			WalletID:      input.ID,
			TransactionID: opening.ID(),
			Direction:     domain.LedgerCredit,
			Money:         input.InitialBalance,
			BalanceBefore: zero,
			BalanceAfter:  input.InitialBalance,
			CreatedAt:     input.CreatedAt,
		})
		if err != nil {
			return err
		}

		if err := s.ledger.Insert(ctx, tx, entry); err != nil {
			return err
		}

		processedPayload, err := json.Marshal(wagerProcessedEvent{TransactionID: opening.ID(), WalletID: input.ID, PlayerID: input.PlayerID, Kind: opening.Kind(), Status: opening.Status(), OccurredAt: input.CreatedAt.UTC()})

		if err != nil {
			return err
		}

		if err := s.outbox.Insert(
			ctx,
			tx,
			postgres.OutboxMessage{
				ID:            uuid.NewString(),
				AggregateType: "WAGER_TRANSACTION",
				AggregateID:   opening.ID(),
				EventType:     "WAGER_TRANSACTION_PROCESSED",
				Payload:       processedPayload,
				AvailableAt:   input.CreatedAt.UTC(),
				CreatedAt:     input.CreatedAt.UTC(),
			},
		); err != nil {
			return err
		}

		balancePayload, err := json.Marshal(balanceChangedEvent{WalletID: input.ID, TransactionID: opening.ID(), Direction: domain.LedgerCredit, Money: input.InitialBalance, BalanceBefore: zero, BalanceAfter: input.InitialBalance, WalletVersion: 1})
		if err != nil {
			return err
		}

		if err := s.outbox.Insert(
			ctx,
			tx,
			postgres.OutboxMessage{
				ID:            uuid.NewString(),
				AggregateType: "WALLET",
				AggregateID:   input.ID,
				EventType:     "WALLET_BALANCE_CHANGED",
				Payload:       balancePayload,
				AvailableAt:   input.CreatedAt.UTC(),
				CreatedAt:     input.CreatedAt.UTC(),
			},
		); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &CreateWalletResult{
		Wallet: wallet,
	}, nil
}
