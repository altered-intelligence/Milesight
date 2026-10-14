package postgres_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/altered-intelligence/milesight/internal/ids"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/store/postgres"
	"github.com/altered-intelligence/milesight/internal/store/storetest"
)

// TestConformance runs the shared store suite against a real database.
// Set MILESIGHT_TEST_DATABASE_URL to a server where the user may CREATE DATABASE;
// each subtest gets its own throwaway database.
func TestConformance(t *testing.T) {
	admin := os.Getenv("MILESIGHT_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("MILESIGHT_TEST_DATABASE_URL not set")
	}
	storetest.Run(t, func(t *testing.T) store.Store {
		ctx := context.Background()
		cfg, err := pgx.ParseConfig(admin)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		name := "ms_test_" + ids.Secret(6)
		name = sanitize(name)
		if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
			t.Fatal(err)
		}
		conn.Close(ctx)
		u, err := url.Parse(admin)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + name
		s, err := postgres.New(ctx, u.String())
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		// Migrations must be idempotent.
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			s.Close()
			c, err := pgx.ConnectConfig(context.Background(), mustParse(t, admin))
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, _ = c.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
				c.Close(ctx)
			}
		})
		return s
	})
}

func mustParse(t *testing.T, url string) *pgx.ConnConfig {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			out = append(out, c)
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
