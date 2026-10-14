package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func setenv(t *testing.T, kv map[string]string) {
	for _, k := range []string{"MILESIGHT_TRANSPORT", "MILESIGHT_STORE", "DATABASE_URL", "JWT_SECRET", "MILESIGHT_ENCRYPTION_KEY",
		"TESLA_CLIENT_ID", "TESLA_CLIENT_SECRET", "TESLA_REGION", "TESLA_SCOPES", "MILESIGHT_DEV_LOGIN", "MILESIGHT_PUBLIC_URL", "TESLA_REDIRECT_URI"} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestDefaultsAreSimulatorWithMemoryStore(t *testing.T) {
	setenv(t, nil)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Transport != "sim" || c.Store != "memory" || !c.DevLogin || !c.Insecure || len(c.EncryptionKey) != 32 {
		t.Fatalf("defaults: %+v", c)
	}
	if c.Tesla.RedirectURI != "http://localhost:8080/v1/auth/tesla/redirect" {
		t.Fatalf("redirect: %s", c.Tesla.RedirectURI)
	}
}

func TestScopesAcceptCommasOrSpaces(t *testing.T) {
	setenv(t, map[string]string{"TESLA_SCOPES": "openid, offline_access vehicle_cmds"})
	c, err := Load()
	if err != nil || strings.Join(c.Tesla.Scopes, "|") != "openid|offline_access|vehicle_cmds" {
		t.Fatalf("scopes: %v %v", err, c.Tesla.Scopes)
	}
}

func TestFleetRequiresRealSecrets(t *testing.T) {
	setenv(t, map[string]string{"MILESIGHT_TRANSPORT": "fleet"})
	if _, err := Load(); err == nil {
		t.Fatal("fleet without client credentials must fail")
	}
	setenv(t, map[string]string{"MILESIGHT_TRANSPORT": "fleet", "TESLA_CLIENT_ID": "id", "TESLA_CLIENT_SECRET": "secret"})
	if _, err := Load(); err == nil {
		t.Fatal("fleet with development secrets must fail")
	}
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	setenv(t, map[string]string{"MILESIGHT_TRANSPORT": "fleet", "TESLA_CLIENT_ID": "id", "TESLA_CLIENT_SECRET": "secret",
		"JWT_SECRET": "a-real-production-secret", "MILESIGHT_ENCRYPTION_KEY": key, "DATABASE_URL": "postgres://x/y"})
	c, err := Load()
	if err != nil || c.Insecure || c.DevLogin || c.Store != "postgres" || !c.FleetEnabled() {
		t.Fatalf("fleet config: %v %+v", err, c)
	}
}

func TestValidation(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"bad transport":  {"MILESIGHT_TRANSPORT": "carrier-pigeon"},
		"bad store":      {"MILESIGHT_STORE": "redis"},
		"pg without url": {"MILESIGHT_STORE": "postgres"},
		"short secret":   {"JWT_SECRET": "short"},
		"bad key":        {"MILESIGHT_ENCRYPTION_KEY": "bm90LTMyLWJ5dGVz"},
		"bad region":     {"TESLA_REGION": "mars"},
	} {
		setenv(t, env)
		if _, err := Load(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
