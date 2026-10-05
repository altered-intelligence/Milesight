// Package config loads server configuration from the environment.
package config

import (
	"os"
	"strings"
)

// Config holds runtime configuration for the Milesight backend.
type Config struct {
	Addr        string // HTTP/WS listen address (e.g. ":8080")
	DatabaseURL string // Postgres connection string
	RedisAddr   string // Redis address (streams/jobs)
	JWTSecret   string // HMAC secret for signing JWTs
	Transport   string // "fleet" (prod) or "owner" (dev/bootstrap)
	TeslaScopes []string
}

// Load reads configuration from environment variables with sane defaults.
func Load() Config {
	return Config{
		Addr:        env("MILESIGHT_ADDR", ":8080"),
		DatabaseURL: env("DATABASE_URL", ""),
		RedisAddr:   env("REDIS_ADDR", "localhost:6379"),
		JWTSecret:   env("JWT_SECRET", "dev-secret-change-me"),
		Transport:   env("MILESIGHT_TRANSPORT", "owner"),
		TeslaScopes: splitCSV(env("TESLA_SCOPES",
			"vehicle_device_data vehicle_charging_cmds vehicle_remote_start")),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
