package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wayne/telemetryiq/internal/api"
	"github.com/wayne/telemetryiq/internal/config"
	"github.com/wayne/telemetryiq/internal/privacy"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.LoadFromEnv()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	handler := api.NewHandler(logger)
	if os.Getenv("TELEMETRYIQ_DEVELOPMENT_INSPECTOR") == "1" {
		dataDir, err := os.UserConfigDir()
		if err != nil {
			logger.Error("locate development inspector data directory", "error", err)
			os.Exit(1)
		}
		salt, err := privacy.LoadOrCreateSalt(filepath.Join(dataDir, "telemetryiq"))
		if err != nil {
			logger.Error("load development inspector privacy salt", "error", err)
			os.Exit(1)
		}
		sanitizer, err := privacy.New(salt)
		if err != nil {
			logger.Error("create development inspector sanitizer", "error", err)
			os.Exit(1)
		}
		handler = api.NewDevelopmentHandler(logger, sanitizer)
	}
	server := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           handler,
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
