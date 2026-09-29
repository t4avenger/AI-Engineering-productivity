package main

import (
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/auth"
)

const maxCodexRolloutImportBytes int64 = 32 << 20

func isCodexRolloutImportCommand(args []string) bool {
	return len(args) > 0 && args[0] == "import-codex-rollout"
}

func localCommand(logger *slog.Logger, args []string) bool {
	if !isCodexRolloutImportCommand(args) {
		return authTokenCommand(logger, args)
	}
	if err := importCodexRollout(args[1:]); err != nil {
		logger.Error("Codex rollout import failed", "reason", err.Error())
		os.Exit(1)
	}
	logger.Info("Codex rollout import accepted")
	return true
}

func importCodexRollout(args []string) error {
	flags := flag.NewFlagSet("import-codex-rollout", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	filePath := flags.String("file", "", "Codex rollout JSONL file")
	endpoint := flags.String("endpoint", "http://127.0.0.1:8080", "TelemetryIQ loopback endpoint")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || strings.TrimSpace(*filePath) == "" {
		return errors.New("usage: telemetryiq import-codex-rollout --file <path> [--endpoint <loopback-url>]")
	}
	rolloutURL, err := localRolloutURL(*endpoint)
	if err != nil {
		return err
	}
	file, err := os.Open(*filePath)
	if err != nil {
		return errors.New("rollout file could not be opened")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return errors.New("rollout file could not be inspected")
	}
	if info.Size() > maxCodexRolloutImportBytes {
		return errors.New("rollout file exceeds the 32 MiB limit")
	}
	token, err := localAPIToken()
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, rolloutURL, file)
	if err != nil {
		return errors.New("rollout import request could not be created")
	}
	request.ContentLength = info.Size()
	request.Header.Set("Content-Type", "application/x-ndjson")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return errors.New("rollout import request failed")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusAccepted {
		return errors.New("rollout import was rejected by the local daemon")
	}
	return nil
}

func localRolloutURL(endpoint string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("endpoint must be an HTTP(S) loopback URL")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("endpoint must not include a path")
	}
	hostname := parsed.Hostname()
	if hostname != "localhost" {
		ip := net.ParseIP(hostname)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("endpoint must resolve explicitly to loopback")
		}
	}
	if parsed.Port() == "" {
		return "", errors.New("endpoint must include the daemon port")
	}
	return strings.TrimRight(endpoint, "/") + "/v1/codex/rollout", nil
}

func localAPIToken() (string, error) {
	if token := strings.TrimSpace(os.Getenv("TELEMETRYIQ_AUTH_TOKEN")); token != "" {
		return token, nil
	}
	dataDir, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("local API authentication token could not be located")
	}
	token, err := auth.Read(filepath.Join(dataDir, "telemetryiq"))
	if err != nil {
		return "", errors.New("local API authentication token could not be read")
	}
	return token, nil
}
