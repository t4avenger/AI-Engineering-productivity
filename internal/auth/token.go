// Package auth manages the local management API token.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const tokenFileName = "auth.token"

// LoadOrCreate returns the explicit runtime override or a securely stored installation token.
func LoadOrCreate(directory, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create token directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", fmt.Errorf("secure token directory: %w", err)
	}
	path := filepath.Join(directory, tokenFileName)
	if token, err := os.ReadFile(path); err == nil {
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("secure token file: %w", err)
		}
		if len(token) == 0 {
			return "", errors.New("local authentication token is empty")
		}
		return string(token), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read local authentication token: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate local authentication token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("write local authentication token: %w", err)
	}
	return token, nil
}

// Read returns an existing token for the explicit `telemetryiq auth-token` command.
func Read(directory string) (string, error) {
	path := filepath.Join(directory, tokenFileName)
	token, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("local authentication token does not exist; start telemetryiq first")
	}
	if err != nil {
		return "", fmt.Errorf("read local authentication token: %w", err)
	}
	if len(token) == 0 {
		return "", errors.New("local authentication token is empty")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("secure token file: %w", err)
	}
	return string(token), nil
}
