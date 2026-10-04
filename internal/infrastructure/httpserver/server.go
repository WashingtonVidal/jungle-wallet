package httpserver

import (
	"context"
	"fmt"
	"net/http"

	"go.uber.org/fx"

	"jungle-wallet/internal/config"
)

type Server struct {
	server *http.Server
}

func NewServer(
	cfg *config.Config,
	mux *http.ServeMux,
) *Server {
	return &Server{
		server: &http.Server{
			Addr:    cfg.HTTPAddress,
			Handler: mux,
		},
	}
}

func RegisterLifecycle(
	lifecycle fx.Lifecycle,
	server *Server,
) {
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				if err := server.server.ListenAndServe(); err != nil &&
					err != http.ErrServerClosed {
					fmt.Printf("HTTP server error: %v\n", err)
				}
			}()

			return nil
		},

		OnStop: func(ctx context.Context) error {
			return server.server.Shutdown(ctx)
		},
	})
}
