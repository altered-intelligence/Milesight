package tesla

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/altered-intelligence/milesight/internal/auth"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// Token is an OAuth token response from auth.tesla.com / fleet-auth.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// Expiry returns the absolute expiry relative to now (defaults to 8h if unspecified).
func (t *Token) Expiry(now time.Time) time.Time {
	if t.ExpiresIn <= 0 {
		return now.Add(8 * time.Hour)
	}
	return now.Add(time.Duration(t.ExpiresIn) * time.Second)
}

// Identity is the subset of id_token / access_token claims used to key accounts.
type Identity struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
}

// IdentityOf extracts the Tesla account identity from a token response. The
// tokens come straight from Tesla's token endpoint over TLS, so their payload
// is trusted without signature verification (OIDC Core §3.1.3.7).
func IdentityOf(t *Token) (Identity, error) {
	var id Identity
	for _, raw := range []string{t.IDToken, t.AccessToken} {
		if raw == "" {
			continue
		}
		var c Identity
		if auth.UnverifiedClaims(raw, &c) == nil && c.Subject != "" {
			if id.Subject == "" {
				id.Subject = c.Subject
			}
			if id.Email == "" {
				id.Email = c.Email
			}
		}
	}
	if id.Subject == "" {
		return id, fmt.Errorf("tesla: token carries no subject")
	}
	return id, nil
}

// OAuth is a Tesla OAuth 2.0 client (Fleet: confidential client with secret;
// Owner: public "ownerapi" client).
type OAuth struct {
	HTTP         *http.Client
	ClientID     string
	ClientSecret string
	TokenURL     string
	Scopes       []string
	JSONBody     bool // the Owner flow posts JSON; Fleet posts form-encoded
}

// NewFleetOAuth returns the Fleet API OAuth client.
func NewFleetOAuth(h *http.Client, clientID, clientSecret string, scopes []string) *OAuth {
	return &OAuth{HTTP: orDefault(h), ClientID: clientID, ClientSecret: clientSecret, TokenURL: FleetTokenURL, Scopes: scopes}
}

// NewOwnerOAuth returns the legacy Owner API OAuth client (dev only).
func NewOwnerOAuth(h *http.Client) *OAuth {
	return &OAuth{HTTP: orDefault(h), ClientID: OwnerClientID, TokenURL: OwnerTokenURL,
		Scopes: []string{"openid", "email", "offline_access"}, JSONBody: true}
}

func orDefault(h *http.Client) *http.Client {
	if h == nil {
		return &http.Client{Timeout: defaultTimeout}
	}
	return h
}

// AuthorizeURL builds the consent URL the user opens in a browser.
func (o *OAuth) AuthorizeURL(redirectURI, state, codeChallenge string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", o.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(o.Scopes, " "))
	q.Set("state", state)
	q.Set("prompt_missing_scopes", "true")
	if codeChallenge != "" {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "S256")
	}
	return AuthorizeURL + "?" + q.Encode()
}

// Exchange trades an authorization code for tokens. audience is the regional
// Fleet API base URL.
func (o *OAuth) Exchange(ctx context.Context, code, redirectURI, audience, codeVerifier string) (*Token, error) {
	p := map[string]string{
		"grant_type": "authorization_code", "client_id": o.ClientID, "code": code, "redirect_uri": redirectURI,
	}
	if o.ClientSecret != "" {
		p["client_secret"] = o.ClientSecret
	}
	if audience != "" {
		p["audience"] = audience
	}
	if codeVerifier != "" {
		p["code_verifier"] = codeVerifier
	}
	return o.post(ctx, p)
}

// Refresh exchanges a refresh token. Tesla refresh tokens are single use: the
// returned token must be persisted before the old one is discarded.
func (o *OAuth) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	p := map[string]string{"grant_type": "refresh_token", "client_id": o.ClientID, "refresh_token": refreshToken}
	if o.JSONBody {
		p["scope"] = strings.Join(o.Scopes, " ")
	}
	return o.post(ctx, p)
}

func (o *OAuth) post(ctx context.Context, params map[string]string) (*Token, error) {
	var body io.Reader
	contentType := "application/x-www-form-urlencoded"
	if o.JSONBody {
		b, _ := json.Marshal(params)
		body, contentType = bytes.NewReader(b), "application/json"
	} else {
		form := url.Values{}
		for k, v := range params {
			form.Set(k, v)
		}
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tesla oauth: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
			Desc  string `json:"error_description"`
		}
		_ = json.Unmarshal(raw, &e)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest &&
			(e.Error == "invalid_grant" || e.Error == "login_required" || e.Error == "invalid_auth_code") {
			return nil, fmt.Errorf("%w (%s)", transport.ErrReauthRequired, strings.TrimSpace(e.Error+" "+e.Desc))
		}
		return nil, &transport.APIError{Status: resp.StatusCode, Message: strings.TrimSpace("oauth: " + e.Error + " " + e.Desc)}
	}
	var t Token
	if err := json.Unmarshal(raw, &t); err != nil || t.AccessToken == "" {
		return nil, fmt.Errorf("tesla oauth: malformed token response")
	}
	return &t, nil
}
