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
	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if authTokenCommand(logger, os.Args[1:]) {
		return
	}

	configManager, cfg, err := config.LoadManagerFromEnv()
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
	calculator, err := cost.LoadDefault(cfg.Pricing.OverridePath)
	if err != nil {
		logger.Error("load local price catalog", "error", err)
		os.Exit(1)
	}
	repository, err := sqlite.Open(filepath.Join(telemetryDir, "telemetryiq.db"), calculator)
	if err != nil {
		logger.Error("open local session storage", "error", err)
		os.Exit(1)
	}
	defer func() { _ = repository.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if deleted, err := repository.ApplyRetention(ctx, cfg.Storage.RetentionDays, time.Now()); err != nil {
		logger.Error("apply storage retention", "error", err)
		os.Exit(1)
	} else if deleted > 0 {
		logger.Info("applied storage retention", "deleted_sessions", deleted, "retention_days", cfg.Storage.RetentionDays)
	}
	go runRetentionLoop(ctx, logger, repository, cfg.Storage.RetentionDays)

	thresholds := api.InsightThresholds{
		ContextWaste: insights.ContextWasteThresholds{
			CachedContextRatioThreshold: cfg.Insights.ContextWaste.CachedContextRatioThreshold,
			InputTokenGrowthThreshold:   cfg.Insights.ContextWaste.InputTokenGrowthThreshold,
		},
		MCPAllowlist:          cfg.Governance.MCPAllowlist,
		MCPAllowlistSource:    configManager,
		SkillsAllowlist:       cfg.Governance.SkillsAllowlist,
		SkillsAllowlistSource: configManager,
	}

	handler := api.NewAuthenticatedPersistentHandler(logger, repository, token, thresholds, configManager)
	if os.Getenv("TELEMETRYIQ_DEVELOPMENT_INSPECTOR") == "1" {
		handler = api.NewAuthenticatedPersistentDevelopmentHandler(logger, repository, token, thresholds, configManager)
	}
	server := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

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

func runRetentionLoop(ctx context.Context, logger *slog.Logger, repository *sqlite.Repository, retentionDays int) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, err := repository.ApplyRetention(ctx, retentionDays, time.Now())
			if err != nil {
				logger.Error("apply storage retention", "error", err)
				continue
			}
			if deleted > 0 {
				logger.Info("applied storage retention", "deleted_sessions", deleted, "retention_days", retentionDays)
			}
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
