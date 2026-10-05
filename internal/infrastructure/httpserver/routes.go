package httpserver

import (
	"jungle-wallet/internal/config"
	"jungle-wallet/internal/infrastructure/auth"
	"net/http"
)

func NewMux(wagerHandler *WagerHandler, walletHandler *WalletHandler, authenticator *auth.Authenticator, queries *QueryHandler, cfg *config.Config) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)
	mux.HandleFunc("GET /health/live", healthHandler)
	mux.HandleFunc("GET /health/ready", queries.Ready)
	protect := func(internal bool, next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			claims, err := authenticator.Authenticate(r.Context(), auth.BearerToken(r))
			if err != nil {
				writeJSONError(w, 401, "unauthorized")
				return
			}
			if internal && claims.ClientID != cfg.InternalClientID {
				writeJSONError(w, 403, "internal service required")
				return
			}
			next(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
		}
	}
	mux.HandleFunc("POST /wallets", protect(true, walletHandler.Create))
	mux.HandleFunc("GET /wallets/{walletId}", protect(true, queries.Wallet))
	mux.HandleFunc("GET /wallets/{walletId}/ledger", protect(true, queries.Ledger))
	mux.HandleFunc("POST /wallets/{walletId}/reconciliation", protect(true, queries.Reconcile))
	mux.HandleFunc("POST /wagering/transactions", protect(false, wagerHandler.Process))
	mux.HandleFunc("GET /wagering/transactions/{transactionId}", protect(false, queries.Transaction))
	mux.HandleFunc("GET /providers/{providerId}/wagering/transactions/{externalTransactionId}", protect(false, queries.Transaction))
	mux.HandleFunc("GET /metrics", protect(true, queries.Metrics))
	return mux
}
func healthHandler(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, map[string]string{"status": "ok"})
}
func writeJSONError(w http.ResponseWriter, status int, message string) {
	sendJSON(w, status, map[string]string{"error": message})
}
