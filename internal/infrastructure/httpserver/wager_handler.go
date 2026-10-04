package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"jungle-wallet/internal/application"
	"jungle-wallet/internal/domain"
	"jungle-wallet/internal/infrastructure/auth"
)

type processWagerRequest struct {
	ProviderID                     string            `json:"providerId"`
	ExternalTransactionID          string            `json:"externalTransactionId"`
	PlayerID                       string            `json:"playerId"`
	WalletID                       string            `json:"walletId"`
	RoundID                        string            `json:"roundId"`
	GameID                         string            `json:"gameId"`
	Kind                           string            `json:"kind"`
	Money                          domain.MoneyInput `json:"money"`
	ReferenceExternalTransactionID string            `json:"referenceExternalTransactionId,omitempty"`
}

type processWagerResponse struct {
	TransactionID    string       `json:"transactionId"`
	Status           string       `json:"status"`
	Balance          domain.Money `json:"balance"`
	IdempotentReplay bool         `json:"idempotentReplay"`
}

type WagerHandler struct {
	service *application.ProcessWagerService
}

func NewWagerHandler(
	service *application.ProcessWagerService,
) *WagerHandler {
	return &WagerHandler{
		service: service,
	}
}

func (h *WagerHandler) Process(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		http.Error(w, "Idempotency-Key header is required", http.StatusBadRequest)
		return
	}

	var request processWagerRequest

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if claims.ClientID != request.ProviderID {
		writeJSONError(w, http.StatusForbidden, "provider is not authorized")
		return
	}

	money, err := request.Money.Parse()
	if err != nil {
		http.Error(w, "invalid money", http.StatusBadRequest)
		return
	}

	kind, err := parseWagerKind(request.Kind)
	if err != nil {
		http.Error(w, "invalid wager kind", http.StatusBadRequest)
		return
	}

	payloadHash, err := wagerPayloadHash(request)
	if err != nil {
		http.Error(w, "could not calculate payload hash", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()

	result, err := h.service.Execute(
		r.Context(),
		application.ProcessWagerInput{
			ID:                             uuid.NewString(),
			LedgerID:                       uuid.NewString(),
			WalletID:                       request.WalletID,
			ExternalTransactionID:          request.ExternalTransactionID,
			ProviderID:                     request.ProviderID,
			IdempotencyKey:                 idempotencyKey,
			PayloadHash:                    payloadHash,
			ReferenceExternalTransactionID: request.ReferenceExternalTransactionID,
			PlayerID:                       request.PlayerID,
			RoundID:                        request.RoundID,
			GameID:                         request.GameID,
			Kind:                           kind,
			Money:                          money,
			CreatedAt:                      now,
		},
	)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalid),
			errors.Is(err, domain.ErrInvalidTransactionKind):
			http.Error(w, err.Error(), http.StatusBadRequest)

		case errors.Is(err, domain.ErrInsufficient):
			http.Error(w, err.Error(), http.StatusConflict)

		default:
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	transaction := result.Transaction

	resultBalance := transaction.ResultBalance()
	if resultBalance == nil {
		http.Error(w, "transaction has no resulting balance", http.StatusInternalServerError)
		return
	}

	response := processWagerResponse{
		TransactionID:    transaction.ID(),
		Status:           string(transaction.Status()),
		Balance:          *resultBalance,
		IdempotentReplay: result.Replay,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(response)
}

func wagerPayloadHash(request processWagerRequest) (string, error) {
	// This struct is the canonical business representation used for
	// idempotency. Transport metadata such as Idempotency-Key is excluded.
	canonical := struct {
		ProviderID                     string            `json:"providerId"`
		ExternalTransactionID          string            `json:"externalTransactionId"`
		PlayerID                       string            `json:"playerId"`
		WalletID                       string            `json:"walletId"`
		RoundID                        string            `json:"roundId"`
		GameID                         string            `json:"gameId"`
		Kind                           string            `json:"kind"`
		Money                          domain.MoneyInput `json:"money"`
		ReferenceExternalTransactionID string            `json:"referenceExternalTransactionId,omitempty"`
	}{
		ProviderID:                     request.ProviderID,
		ExternalTransactionID:          request.ExternalTransactionID,
		PlayerID:                       request.PlayerID,
		WalletID:                       request.WalletID,
		RoundID:                        request.RoundID,
		GameID:                         request.GameID,
		Kind:                           request.Kind,
		Money:                          request.Money,
		ReferenceExternalTransactionID: request.ReferenceExternalTransactionID,
	}

	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:]), nil
}

func parseWagerKind(value string) (domain.WagerTransactionKind, error) {
	switch value {
	case "BET":
		return domain.WagerTransactionKindBet, nil
	case "WIN":
		return domain.WagerTransactionKindWin, nil
	case "LOSS":
		return domain.WagerTransactionKindLoss, nil
	case "REFUND":
		return domain.WagerTransactionKindRefund, nil
	case "ROLLBACK":
		return domain.WagerTransactionKindRollback, nil
	default:
		return "", domain.ErrInvalidTransactionKind
	}
}
