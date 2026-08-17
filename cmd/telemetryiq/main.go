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
	"github.com/wayne/telemetryiq/internal/auth"
	"github.com/wayne/telemetryiq/internal/config"
	"github.com/wayne/telemetryiq/internal/cost"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if authTokenCommand(logger, os.Args[1:]) {
		return
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	dataDir, err := os.UserConfigDir()
	if err != nil {
		logger.Error("locate application data directory", "error", err)
		os.Exit(1)
	}
	telemetryDir := filepath.Join(dataDir, "telemetryiq")
	token, err := auth.LoadOrCreate(telemetryDir, os.Getenv("TELEMETRYIQ_AUTH_TOKEN"))
	if err != nil {
		logger.Error("load local API authentication token", "error", err)
		os.Exit(1)
	}
	salt, err := privacy.LoadOrCreateSalt(telemetryDir)
	if err != nil {
		logger.Error("load privacy salt", "error", err)
		os.Exit(1)
	}
	sanitizer, err := privacy.New(salt)
	if err != nil {
		logger.Error("create privacy sanitizer", "error", err)
		os.Exit(1)
	}
	calculator, err := cost.LoadDefault(cfg.Pricing.OverridePath)
	if err != nil {
		logger.Error("load local price catalog", "error", err)
		os.Exit(1)
	}
	repository, err := sqlite.Open(filepath.Join(telemetryDir, "telemetryiq.db"), sanitizer, calculator)
	if err != nil {
		logger.Error("open local session storage", "error", err)
		os.Exit(1)
	}
	defer func() { _ = repository.Close() }()

	handler := api.NewAuthenticatedPersistentHandler(logger, sanitizer, repository, token)
	if os.Getenv("TELEMETRYIQ_DEVELOPMENT_INSPECTOR") == "1" {
		handler = api.NewAuthenticatedPersistentDevelopmentHandler(logger, sanitizer, repository, token)
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

func authTokenCommand(logger *slog.Logger, args []string) bool {
	if len(args) != 1 || args[0] != "auth-token" {
		return false
	}
	if !printAuthToken(logger) {
		os.Exit(1)
	}
	return true
}

func printAuthToken(logger *slog.Logger) bool {
	dataDir, err := os.UserConfigDir()
	if err != nil {
		logger.Error("locate application data directory", "error", err)
		return false
	}
	token, err := auth.Read(filepath.Join(dataDir, "telemetryiq"))
	if err != nil {
		logger.Error("read local API authentication token", "error", err)
		return false
	}
	_, _ = os.Stdout.WriteString(token + "\n")
	return true
}
