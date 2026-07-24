package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wayne/telemetryiq/internal/api"
	"github.com/wayne/telemetryiq/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := config.FromEnv()
	server := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           api.NewHandler(logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("telemetryiq daemon starting", "addr", cfg.Addr())
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("telemetryiq daemon shutdown failed", "error", err)
			os.Exit(1)
		}
		logger.Info("telemetryiq daemon stopped")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("telemetryiq daemon failed", "error", err)
			os.Exit(1)
		}
	}
}
