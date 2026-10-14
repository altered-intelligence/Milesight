package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/altered-intelligence/milesight/internal/ids"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
)

// ErrRefreshInvalid means the refresh token is unknown, expired, revoked or reused.
var ErrRefreshInvalid = errors.New("auth: refresh token invalid")

// Sessions issues access/refresh pairs and rotates refresh tokens.
//
// Rotation: every refresh consumes the presented token and issues a new one in
// the same family. Presenting an already-used token means it leaked (or a client
// raced itself), so the whole family is revoked and the user must sign in again.
type Sessions struct {
	Store      store.Store
	Signer     *Signer
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Now        func() time.Time
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Create starts a new session family for user.
func (s *Sessions) Create(ctx context.Context, user *models.User) (*models.Session, error) {
	return s.issue(ctx, user, ids.New())
}

func (s *Sessions) issue(ctx context.Context, user *models.User, family string) (*models.Session, error) {
	access, exp := s.Signer.Issue(user.ID, ids.New(), s.AccessTTL)
	refresh := ids.Secret(32)
	now := s.now()
	if err := s.Store.InsertRefreshToken(ctx, &store.RefreshToken{
		UserID: user.ID, FamilyID: family, Hash: HashToken(refresh), CreatedAt: now, ExpiresAt: now.Add(s.RefreshTTL),
	}); err != nil {
		return nil, fmt.Errorf("auth: store refresh token: %w", err)
	}
	return &models.Session{
		AccessToken: access, RefreshToken: refresh, TokenType: "Bearer",
		ExpiresIn: int(s.AccessTTL.Seconds()), ExpiresAt: exp, User: *user,
	}, nil
}

// Refresh rotates a refresh token into a new session.
func (s *Sessions) Refresh(ctx context.Context, refreshToken string) (*models.Session, error) {
	if refreshToken == "" {
		return nil, ErrRefreshInvalid
	}
	rt, err := s.Store.GetRefreshTokenByHash(ctx, HashToken(refreshToken))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRefreshInvalid
	}
	if err != nil {
		return nil, err
	}
	now := s.now()
	if rt.RevokedAt != nil || now.After(rt.ExpiresAt) {
		return nil, ErrRefreshInvalid
	}
	ok, err := s.Store.MarkRefreshTokenUsed(ctx, rt.ID, now)
	if err != nil {
		return nil, err
	}
	if !ok { // reuse → assume theft, kill the family
		_ = s.Store.RevokeRefreshFamily(ctx, rt.FamilyID, now)
		return nil, ErrRefreshInvalid
	}
	user, err := s.Store.GetUser(ctx, rt.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrRefreshInvalid
	}
	if err != nil {
		return nil, err
	}
	return s.issue(ctx, user, rt.FamilyID)
}

// RevokeFamilyOf revokes the session family a refresh token belongs to (sign-out of one device).
func (s *Sessions) RevokeFamilyOf(ctx context.Context, refreshToken string) error {
	rt, err := s.Store.GetRefreshTokenByHash(ctx, HashToken(refreshToken))
	if err != nil {
		return err
	}
	return s.Store.RevokeRefreshFamily(ctx, rt.FamilyID, s.now())
}

// RevokeAll signs the user out everywhere.
func (s *Sessions) RevokeAll(ctx context.Context, userID string) error {
	return s.Store.RevokeUserRefreshTokens(ctx, userID, s.now())
}
