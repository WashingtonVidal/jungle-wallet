package httpserver

import (
	"net/http"
)

func NewMux(
	wagerHandler *WagerHandler,
) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", healthHandler)
	mux.HandleFunc("POST /wagering/transactions", wagerHandler.Process)

	return mux
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
