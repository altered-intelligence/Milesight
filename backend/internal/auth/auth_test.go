package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/altered-intelligence/milesight/internal/store/memory"
)

func TestJWTRoundTripAndTamper(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123"))
	tok, exp := s.Issue("user-1", "j1", time.Minute)
	if time.Until(exp) <= 0 {
		t.Fatal("expiry in the past")
	}
	c, err := s.Verify(tok)
	if err != nil || c.Subject != "user-1" || c.ID != "j1" {
		t.Fatalf("verify: %v %+v", err, c)
	}
	parts := strings.Split(tok, ".")
	forged := parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"admin","iss":"milesight","aud":"milesight-api","exp":9999999999}`)) + "." + parts[2]
	if _, err := s.Verify(forged); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("forged payload accepted")
	}
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + parts[1] + "."
	if _, err := s.Verify(none); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("alg=none accepted")
	}
	other := NewSigner([]byte("another-secret-entirely"))
	if _, err := other.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("wrong key accepted")
	}
	s.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err := s.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("expired token accepted")
	}
}

func TestCipher(t *testing.T) {
	c, err := NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal([]byte("tesla-refresh"), []byte("user-a"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := c.Open(sealed, []byte("user-a"))
	if err != nil || string(pt) != "tesla-refresh" {
		t.Fatalf("open: %v %q", err, pt)
	}
	if _, err := c.Open(sealed, []byte("user-b")); err == nil {
		t.Fatal("ciphertext must be bound to its owner")
	}
	sealed2, _ := c.Seal([]byte("tesla-refresh"), []byte("user-a"))
	if bytes.Equal(sealed, sealed2) {
		t.Fatal("nonce reuse")
	}
	if _, err := NewCipher([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestPKCE(t *testing.T) {
	v, c := PKCE()
	if len(v) < 43 || len(c) != 43 || v == c {
		t.Fatalf("pkce lengths: %d %d", len(v), len(c))
	}
}

func TestSessionRotationAndReuseDetection(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	user, _ := st.UpsertUser(ctx, "dev:x", "x@example.com", "sim")
	ss := &Sessions{Store: st, Signer: NewSigner([]byte("0123456789abcdef0123")), AccessTTL: time.Minute, RefreshTTL: time.Hour}

	s1, err := ss.Create(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Signer.Verify(s1.AccessToken); err != nil {
		t.Fatal(err)
	}
	s2, err := ss.Refresh(ctx, s1.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if s2.RefreshToken == s1.RefreshToken {
		t.Fatal("refresh token must rotate")
	}
	// Replaying the old token revokes the whole family, including s2's token.
	if _, err := ss.Refresh(ctx, s1.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatal("reuse must fail")
	}
	if _, err := ss.Refresh(ctx, s2.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatal("family must be revoked after reuse")
	}
	// Another device's family is unaffected until RevokeAll.
	s3, _ := ss.Create(ctx, user)
	if _, err := ss.Refresh(ctx, s3.RefreshToken); err != nil {
		t.Fatalf("independent family: %v", err)
	}
	if _, err := ss.Refresh(ctx, "garbage"); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatal("garbage accepted")
	}
	ss.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	s4, _ := (&Sessions{Store: st, Signer: ss.Signer, AccessTTL: time.Minute, RefreshTTL: time.Hour}).Create(ctx, user)
	if _, err := ss.Refresh(ctx, s4.RefreshToken); !errors.Is(err, ErrRefreshInvalid) {
		t.Fatal("expired refresh accepted")
	}
}
