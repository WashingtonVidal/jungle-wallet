package httpserver

import (
	"net/http"

	"jungle-wallet/internal/infrastructure/auth"
)

func NewMux(
	wagerHandler *WagerHandler,
	authenticator *auth.Authenticator,
) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", healthHandler)

	mux.HandleFunc(
		"POST /wagering/transactions",
		func(w http.ResponseWriter, r *http.Request) {
			claims, err := authenticator.Authenticate(
				r.Context(),
				auth.BearerToken(r),
			)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			}

			ctx := auth.WithClaims(r.Context(), claims)
			wagerHandler.Process(w, r.WithContext(ctx))
		},
	)

	return mux
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_, _ = w.Write([]byte(`{"error":"` + message + `"}`))
}
