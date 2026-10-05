package httpserver

import (
	"context"
	"go.uber.org/fx"
	"jungle-wallet/internal/config"
	"log/slog"
	"net"
	"net/http"
	"time"
)

type Server struct {
	server *http.Server
	done   chan struct{}
}

func NewServer(cfg *config.Config, mux *http.ServeMux) *Server {
	return &Server{server: &http.Server{Addr: cfg.HTTPAddress, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}, done: make(chan struct{})}
}
func RegisterLifecycle(lifecycle fx.Lifecycle, server *Server) {
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			listener, err := net.Listen("tcp", server.server.Addr)
			if err != nil {
				return err
			}
			go func() {
				defer close(server.done)
				if err := server.server.Serve(listener); err != nil && err != http.ErrServerClosed {
					slog.Error("HTTP server failed", "error", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			err := server.server.Shutdown(ctx)
			if err != nil {
				_ = server.server.Close()
			}
			select {
			case <-server.done:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
