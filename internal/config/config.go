package config

import (
	"net"
	"os"
)

const (
	defaultHost = "127.0.0.1"
	defaultPort = "8080"
)

type Config struct {
	Host string
	Port string
}

func FromEnv() Config {
	host := getenv("TELEMETRYIQ_HOST", defaultHost)
	port := getenv("TELEMETRYIQ_PORT", defaultPort)

	return Config{Host: host, Port: port}
}

func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, c.Port)
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
