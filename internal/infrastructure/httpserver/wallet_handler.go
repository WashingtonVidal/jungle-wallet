package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"jungle-wallet/internal/application"
	"jungle-wallet/internal/domain"
)

type createWalletRequest struct {
	PlayerID       string            `json:"playerId"`
	InitialBalance domain.MoneyInput `json:"initialBalance"`
}

type createWalletResponse struct {
	WalletID string       `json:"id"`
	Version  int64        `json:"version"`
	PlayerID string       `json:"playerId"`
	Balance  domain.Money `json:"balance"`
}

type WalletHandler struct {
	service *application.CreateWalletService
}

func NewWalletHandler(
	service *application.CreateWalletService,
) *WalletHandler {
	return &WalletHandler{
		service: service,
	}
}

func (h *WalletHandler) Create(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	var request createWalletRequest

	if err := decodeBody(w, r, &request); err != nil {
		http.Error(
			w,
			"invalid JSON body",
			http.StatusBadRequest,
		)
		return
	}

	initialBalance, err := request.InitialBalance.Parse()
	if err != nil {
		http.Error(
			w,
			"invalid initial balance",
			http.StatusBadRequest,
		)
		return
	}

	result, err := h.service.Execute(
		r.Context(),
		application.CreateWalletInput{
			ID:             uuid.NewString(),
			PlayerID:       request.PlayerID,
			InitialBalance: initialBalance,
			CreatedAt:      time.Now().UTC(),
		},
	)
	if err != nil {
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &pgErr) && pgErr.Code == "23505":
			http.Error(w, "wallet already exists", http.StatusConflict)
		case errors.Is(err, domain.ErrInvalid),
			errors.Is(err, domain.ErrCurrency):
			http.Error(
				w,
				err.Error(),
				http.StatusBadRequest,
			)

		default:
			http.Error(
				w,
				"internal server error",
				http.StatusInternalServerError,
			)
		}

		return
	}

	walletState, err := result.Wallet.State()
	if err != nil {
		http.Error(
			w,
			"internal server error",
			http.StatusInternalServerError,
		)
		return
	}

	response := createWalletResponse{
		WalletID: walletState.ID,
		Version:  walletState.Version,
		PlayerID: walletState.PlayerID,
		Balance:  walletState.Balance,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	_ = json.NewEncoder(w).Encode(response)
}
