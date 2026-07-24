package config

import "testing"

func TestFromEnvDefaultsToLoopback(t *testing.T) {
	t.Setenv("TELEMETRYIQ_HOST", "")
	t.Setenv("TELEMETRYIQ_PORT", "")

	cfg := FromEnv()
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("expected loopback host, got %q", cfg.Host)
	}
	if cfg.Port != "8080" {
		t.Fatalf("expected default port, got %q", cfg.Port)
	}
	if cfg.Addr() != "127.0.0.1:8080" {
		t.Fatalf("expected loopback address, got %q", cfg.Addr())
	}
}

func TestFromEnvUsesPortOverride(t *testing.T) {
	t.Setenv("TELEMETRYIQ_PORT", "9090")
	t.Setenv("TELEMETRYIQ_HOST", "127.0.0.1")

	cfg := FromEnv()
	if cfg.Addr() != "127.0.0.1:9090" {
		t.Fatalf("expected overridden address, got %q", cfg.Addr())
	}
}
