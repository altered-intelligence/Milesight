// Package auth issues and verifies Milesight sessions (ADR-014): short-lived
// HS256 JWT access tokens plus rotating, hashed refresh tokens with reuse
// detection. Tesla tokens are encrypted at rest with AES-256-GCM.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	issuer   = "milesight"
	audience = "milesight-api"
)

// ErrInvalidToken covers malformed, forged, expired or wrong-audience tokens.
var ErrInvalidToken = errors.New("auth: invalid token")

// Claims are the access-token claims Milesight uses.
type Claims struct {
	Subject   string `json:"sub"`
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	ID        string `json:"jti,omitempty"`
}

// Signer issues and verifies HS256 JWTs. It is stdlib-only by design: the
// surface we need is tiny and avoiding a dependency avoids alg-confusion bugs.
type Signer struct {
	secret []byte
	now    func() time.Time
}

// NewSigner returns a signer for secret.
func NewSigner(secret []byte) *Signer { return &Signer{secret: secret, now: time.Now} }

var jwtHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// Issue returns a signed access token for subject valid for ttl.
func (s *Signer) Issue(subject, jti string, ttl time.Duration) (string, time.Time) {
	now := s.now().UTC()
	exp := now.Add(ttl)
	payload, _ := json.Marshal(Claims{Subject: subject, Issuer: issuer, Audience: audience,
		IssuedAt: now.Unix(), ExpiresAt: exp.Unix(), ID: jti})
	signing := jwtHeader + "." + base64.RawURLEncoding.EncodeToString(payload)
	return signing + "." + s.sign(signing), exp
}

// Verify checks signature, algorithm, issuer, audience and expiry.
func (s *Signer) Verify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var h struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(hdr, &h) != nil || h.Alg != "HS256" {
		return nil, ErrInvalidToken
	}
	want := s.sign(parts[0] + "." + parts[1])
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return nil, ErrInvalidToken
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var c Claims
	if json.Unmarshal(body, &c) != nil {
		return nil, ErrInvalidToken
	}
	now := s.now().Unix()
	if c.Issuer != issuer || c.Audience != audience || c.Subject == "" || now >= c.ExpiresAt || c.IssuedAt > now+60 {
		return nil, ErrInvalidToken
	}
	return &c, nil
}

func (s *Signer) sign(input string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(input))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// UnverifiedClaims decodes a JWT payload without verifying it. Only for tokens
// received directly from Tesla's token endpoint over TLS (OIDC §3.1.3.7).
func UnverifiedClaims(token string, dst any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrInvalidToken
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidToken
	}
	return json.Unmarshal(body, dst)
}
