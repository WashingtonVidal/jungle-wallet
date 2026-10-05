package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
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
	TransactionID    string        `json:"transactionId"`
	Status           string        `json:"status"`
	FailureCode      string        `json:"failureCode,omitempty"`
	Balance          *domain.Money `json:"balance,omitempty"`
	IdempotentReplay bool          `json:"idempotentReplay"`
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

	if err := decodeBody(w, r, &request); err != nil {
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

	for _, v := range []string{request.ExternalTransactionID, request.ProviderID, request.PlayerID, request.WalletID, request.RoundID, request.GameID, idempotencyKey} {
		if strings.TrimSpace(v) == "" {
			writeJSONError(w, 400, "incomplete wager input")
			return
		}
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

	payloadHash, legacyHash, err := wagerPayloadHashes(request)
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
			LegacyPayloadHash:              legacyHash,
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
		case errors.Is(err, domain.ErrIdempotencyConflict):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, domain.ErrInvalid),
			errors.Is(err, domain.ErrInvalidTransactionKind), errors.Is(err, domain.ErrInvalidTransaction), errors.Is(err, domain.ErrCurrency):
			http.Error(w, err.Error(), http.StatusBadRequest)

		case errors.Is(err, domain.ErrInsufficient):
			http.Error(w, err.Error(), http.StatusConflict)

		default:
			http.Error(w, "internal server error", http.StatusServiceUnavailable)
		}
		return
	}

	transaction := result.Transaction

	resultBalance := transaction.ResultBalance()

	response := processWagerResponse{
		TransactionID:    transaction.ID(),
		Status:           string(transaction.Status()),
		Balance:          resultBalance,
		FailureCode:      transaction.FailureCode(),
		IdempotentReplay: result.Replay,
	}

	w.Header().Set("Content-Type", "application/json")
	status := http.StatusOK
	if transaction.Status() == domain.WagerTransactionStatusPendingReference {
		status = http.StatusAccepted
	}
	if transaction.Status() == domain.WagerTransactionStatusRejected {
		status = http.StatusUnprocessableEntity
	}
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(response)
}

func wagerPayloadHashes(request processWagerRequest) (string, string, error) {
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
		return "", "", err
	}

	return domain.CanonicalPayloadHash(data)
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
