package tesla

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/altered-intelligence/milesight/internal/auth"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// refreshSkew refreshes tokens this long before they expire.
const refreshSkew = 5 * time.Minute

// Credentials stores Tesla tokens encrypted at rest and hands out valid access
// tokens, refreshing (once, serialized per user) when they near expiry. Tesla
// refresh tokens are single-use, so concurrent refreshes would lock users out.
type Credentials struct {
	Store  store.Store
	Cipher *auth.Cipher
	OAuth  map[string]*OAuth // by transport name: "fleet", "owner"
	Now    func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (c *Credentials) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *Credentials) userLock(userID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locks == nil {
		c.locks = map[string]*sync.Mutex{}
	}
	l, ok := c.locks[userID]
	if !ok {
		l = &sync.Mutex{}
		c.locks[userID] = l
	}
	return l
}

// Save encrypts and stores a token for user.
func (c *Credentials) Save(ctx context.Context, userID, transportName, region string, t *Token) error {
	ad := []byte(userID)
	at, err := c.Cipher.Seal([]byte(t.AccessToken), ad)
	if err != nil {
		return err
	}
	rt, err := c.Cipher.Seal([]byte(t.RefreshToken), ad)
	if err != nil {
		return err
	}
	return c.Store.SaveCredentials(ctx, &store.Credentials{
		UserID: userID, Transport: transportName, AccessTokenEnc: at, RefreshTokenEnc: rt,
		Scopes: strings.Fields(t.Scope), Region: region, ExpiresAt: t.Expiry(c.now()),
	})
}

// AccessToken returns a valid access token and region for user. force refreshes
// even if the current token has not expired (used after a 401).
func (c *Credentials) AccessToken(ctx context.Context, userID string, force bool) (string, string, error) {
	l := c.userLock(userID)
	l.Lock()
	defer l.Unlock()

	cred, err := c.Store.GetCredentials(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return "", "", transport.ErrReauthRequired
	}
	if err != nil {
		return "", "", err
	}
	ad := []byte(userID)
	if !force && c.now().Add(refreshSkew).Before(cred.ExpiresAt) {
		at, err := c.Cipher.Open(cred.AccessTokenEnc, ad)
		if err != nil {
			return "", "", fmt.Errorf("tesla credentials: decrypt: %w", err)
		}
		return string(at), cred.Region, nil
	}
	oa, ok := c.OAuth[cred.Transport]
	if !ok {
		return "", "", fmt.Errorf("tesla credentials: no OAuth client for transport %q", cred.Transport)
	}
	rt, err := c.Cipher.Open(cred.RefreshTokenEnc, ad)
	if err != nil {
		return "", "", fmt.Errorf("tesla credentials: decrypt: %w", err)
	}
	tok, err := oa.Refresh(ctx, string(rt))
	if err != nil {
		return "", "", err
	}
	if tok.RefreshToken == "" { // keep the old one if Tesla didn't rotate
		tok.RefreshToken = string(rt)
	}
	if tok.Scope == "" {
		tok.Scope = strings.Join(cred.Scopes, " ")
	}
	if err := c.Save(ctx, userID, cred.Transport, cred.Region, tok); err != nil {
		return "", "", fmt.Errorf("tesla credentials: persist refreshed token: %w", err)
	}
	return tok.AccessToken, cred.Region, nil
}
