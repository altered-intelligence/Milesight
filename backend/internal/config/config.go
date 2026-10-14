// Package config loads and validates server configuration from the environment.
package config

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration for the Milesight backend.
type Config struct {
	Addr        string // HTTP/WS listen address (":8080")
	PublicURL   string // externally reachable base URL, used for OAuth redirects
	Transport   string // primary Tesla transport: fleet | owner | sim
	Store       string // postgres | memory
	DatabaseURL string
	LogLevel    string
	CORSOrigins []string

	JWTSecret     []byte
	EncryptionKey []byte // 32 bytes, AES-256-GCM for Tesla tokens at rest
	AccessTTL     time.Duration
	RefreshTTL    time.Duration

	DevLogin    bool   // POST /v1/auth/dev/login (simulator accounts)
	OwnerLogin  bool   // POST /v1/auth/dev/tesla-token (Owner API refresh token)
	AppRedirect string // where /v1/auth/tesla/redirect bounces the OAuth code (app URL scheme)

	Tesla TeslaConfig
	APNs  APNsConfig
	Sim   SimConfig

	// Insecure is true when running with development defaults that must never
	// reach production (default JWT secret, derived encryption key).
	Insecure bool
}

// TeslaConfig configures Fleet API OAuth and transport.
type TeslaConfig struct {
	ClientID        string
	ClientSecret    string
	RedirectURI     string
	Scopes          []string
	DefaultRegion   string
	CommandProxyURL string // vehicle-command HTTP proxy (signs commands for VCP vehicles)
	CommandProxyCA  string // PEM file trusted for the proxy's TLS certificate
}

// APNsConfig configures Apple push (token-based auth).
type APNsConfig struct {
	KeyFile     string // .p8
	KeyID       string
	TeamID      string
	Topic       string // bundle id
	Environment string // sandbox | production
}

// Enabled reports whether APNs credentials are complete.
func (a APNsConfig) Enabled() bool {
	return a.KeyFile != "" && a.KeyID != "" && a.TeamID != "" && a.Topic != ""
}

// SimConfig configures the vehicle simulator transport.
type SimConfig struct {
	HomeLat     float64
	HomeLon     float64
	Vehicles    int
	HistoryDays int
}

const devSecret = "dev-secret-change-me"

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	c := Config{
		Addr:        env("MILESIGHT_ADDR", ":8080"),
		PublicURL:   strings.TrimRight(env("MILESIGHT_PUBLIC_URL", "http://localhost:8080"), "/"),
		Transport:   strings.ToLower(env("MILESIGHT_TRANSPORT", "sim")),
		DatabaseURL: env("DATABASE_URL", ""),
		LogLevel:    env("LOG_LEVEL", "info"),
		CORSOrigins: splitList(env("MILESIGHT_CORS_ORIGINS", "")),
		AccessTTL:   envDuration("MILESIGHT_ACCESS_TTL", 15*time.Minute),
		RefreshTTL:  envDuration("MILESIGHT_REFRESH_TTL", 60*24*time.Hour),
		AppRedirect: env("MILESIGHT_APP_REDIRECT", "milesight://auth/callback"),
		Tesla: TeslaConfig{
			ClientID:        env("TESLA_CLIENT_ID", ""),
			ClientSecret:    env("TESLA_CLIENT_SECRET", ""),
			RedirectURI:     env("TESLA_REDIRECT_URI", ""),
			Scopes:          splitList(env("TESLA_SCOPES", "openid offline_access user_data vehicle_device_data vehicle_location vehicle_cmds vehicle_charging_cmds")),
			DefaultRegion:   strings.ToLower(env("TESLA_REGION", "na")),
			CommandProxyURL: strings.TrimRight(env("TESLA_COMMAND_PROXY_URL", ""), "/"),
			CommandProxyCA:  env("TESLA_COMMAND_PROXY_CA_FILE", ""),
		},
		APNs: APNsConfig{
			KeyFile:     env("APNS_KEY_FILE", ""),
			KeyID:       env("APNS_KEY_ID", ""),
			TeamID:      env("APNS_TEAM_ID", ""),
			Topic:       env("APNS_TOPIC", "com.milesight.ios"),
			Environment: env("APNS_ENVIRONMENT", "sandbox"),
		},
		Sim: SimConfig{
			HomeLat:     envFloat("MILESIGHT_SIM_HOME_LAT", 37.3947),
			HomeLon:     envFloat("MILESIGHT_SIM_HOME_LON", -122.1503),
			Vehicles:    envInt("MILESIGHT_SIM_VEHICLES", 2),
			HistoryDays: envInt("MILESIGHT_SIM_HISTORY_DAYS", 30),
		},
	}

	switch c.Transport {
	case "fleet", "owner", "sim":
	default:
		return c, fmt.Errorf("MILESIGHT_TRANSPORT must be fleet, owner or sim (got %q)", c.Transport)
	}
	c.DevLogin = envBool("MILESIGHT_DEV_LOGIN", c.Transport == "sim")
	c.OwnerLogin = envBool("MILESIGHT_OWNER_LOGIN", c.Transport == "owner")

	c.Store = strings.ToLower(env("MILESIGHT_STORE", ""))
	if c.Store == "" {
		c.Store = "memory"
		if c.DatabaseURL != "" {
			c.Store = "postgres"
		}
	}
	if c.Store != "memory" && c.Store != "postgres" {
		return c, fmt.Errorf("MILESIGHT_STORE must be memory or postgres (got %q)", c.Store)
	}
	if c.Store == "postgres" && c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required when MILESIGHT_STORE=postgres")
	}

	secret := env("JWT_SECRET", devSecret)
	if secret == devSecret {
		c.Insecure = true
	}
	if len(secret) < 16 {
		return c, errors.New("JWT_SECRET must be at least 16 characters")
	}
	c.JWTSecret = []byte(secret)

	if k := os.Getenv("MILESIGHT_ENCRYPTION_KEY"); k != "" {
		key, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(key) != 32 {
			return c, errors.New("MILESIGHT_ENCRYPTION_KEY must be 32 bytes, base64-encoded (openssl rand -base64 32)")
		}
		c.EncryptionKey = key
	} else {
		// Development fallback: derive from the JWT secret. Never acceptable in production.
		sum := sha256.Sum256([]byte("milesight-token-encryption:" + secret))
		c.EncryptionKey = sum[:]
		c.Insecure = true
	}

	if c.Transport == "fleet" {
		if c.Tesla.ClientID == "" || c.Tesla.ClientSecret == "" {
			return c, errors.New("fleet transport requires TESLA_CLIENT_ID and TESLA_CLIENT_SECRET")
		}
		if c.Insecure {
			return c, errors.New("fleet transport requires a real JWT_SECRET and MILESIGHT_ENCRYPTION_KEY")
		}
	}
	if c.Tesla.RedirectURI == "" {
		c.Tesla.RedirectURI = c.PublicURL + "/v1/auth/tesla/redirect"
	}
	switch c.Tesla.DefaultRegion {
	case "na", "eu", "cn":
	default:
		return c, fmt.Errorf("TESLA_REGION must be na, eu or cn (got %q)", c.Tesla.DefaultRegion)
	}
	if c.Sim.Vehicles < 1 || c.Sim.Vehicles > 5 {
		c.Sim.Vehicles = 2
	}
	if c.Sim.HistoryDays < 0 || c.Sim.HistoryDays > 365 {
		c.Sim.HistoryDays = 30
	}
	return c, nil
}

// FleetEnabled reports whether Fleet OAuth can be offered.
func (c Config) FleetEnabled() bool { return c.Tesla.ClientID != "" && c.Tesla.ClientSecret != "" }

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}

// splitList splits on commas and/or whitespace.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}
