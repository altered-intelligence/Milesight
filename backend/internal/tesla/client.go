// Package tesla talks to Tesla's REST APIs: OAuth, the Fleet API (production)
// and the legacy Owner API (development). It maps responses deterministically
// onto Milesight's SI-unit models (ADR-009).
package tesla

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/altered-intelligence/milesight/internal/transport"
)

// Endpoints (Fleet API docs: developer.tesla.com/docs/fleet-api).
const (
	AuthorizeURL   = "https://auth.tesla.com/oauth2/v3/authorize"
	FleetTokenURL  = "https://fleet-auth.prd.vn.cloud.tesla.com/oauth2/v3/token"
	OwnerTokenURL  = "https://auth.tesla.com/oauth2/v3/token"
	OwnerBaseURL   = "https://owner-api.teslamotors.com"
	OwnerClientID  = "ownerapi"
	userAgent      = "Milesight/1.0"
	maxBodyBytes   = 4 << 20
	defaultTimeout = 30 * time.Second
)

// FleetBaseURLs maps a region to its Fleet API base URL.
var FleetBaseURLs = map[string]string{
	"na": "https://fleet-api.prd.na.vn.cloud.tesla.com", // North America + Asia-Pacific (ex. China)
	"eu": "https://fleet-api.prd.eu.vn.cloud.tesla.com", // Europe, Middle East, Africa
	"cn": "https://fleet-api.prd.cn.vn.cloud.tesla.cn",  // China
}

// FleetBaseURL returns the base URL for region (defaulting to na).
func FleetBaseURL(region string) string {
	if u, ok := FleetBaseURLs[region]; ok {
		return u
	}
	return FleetBaseURLs["na"]
}

// Client performs authenticated JSON calls against Tesla.
type Client struct {
	HTTP *http.Client
}

// NewClient returns a client with sane timeouts.
func NewClient(h *http.Client) *Client {
	if h == nil {
		h = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{HTTP: h}
}

type envelope struct {
	Response         json.RawMessage `json:"response"`
	Error            string          `json:"error"`
	ErrorDescription string          `json:"error_description"`
}

// Do sends a request and decodes the "response" field of Tesla's envelope into out.
func (c *Client) Do(ctx context.Context, method, url, token string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("tesla: %s %s: %w", method, redact(url), err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	var env envelope
	_ = json.Unmarshal(raw, &env)
	if err := statusError(resp, env); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if len(env.Response) == 0 || string(env.Response) == "null" {
		return &transport.APIError{Status: resp.StatusCode, Message: "empty response"}
	}
	if err := json.Unmarshal(env.Response, out); err != nil {
		return fmt.Errorf("tesla: decode %s: %w", redact(url), err)
	}
	return nil
}

func statusError(resp *http.Response, env envelope) error {
	msg := strings.TrimSpace(env.Error + " " + env.ErrorDescription)
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusUnauthorized:
		if strings.Contains(msg, "login_required") || strings.Contains(msg, "invalid_grant") {
			return transport.ErrReauthRequired
		}
		return transport.ErrUnauthorized
	case code == http.StatusNotFound:
		return transport.ErrNotFound
	case code == http.StatusRequestTimeout:
		return transport.ErrVehicleUnavailable
	case code == http.StatusTooManyRequests:
		ra, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if ra <= 0 {
			ra = 60
		}
		return &transport.RateLimitError{RetryAfter: time.Duration(ra) * time.Second}
	case code == http.StatusMisdirectedRequest:
		return &transport.APIError{Status: code, Message: "wrong Fleet API region for this account: " + msg}
	case code == http.StatusPreconditionFailed:
		return &transport.APIError{Status: code, Message: "partner account not registered in this region: " + msg}
	default:
		if msg == "" {
			msg = http.StatusText(code)
		}
		return &transport.APIError{Status: code, Message: msg}
	}
}

// redact strips query strings from URLs before they reach logs/errors.
func redact(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}
