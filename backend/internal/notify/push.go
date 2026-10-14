package notify

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
)

// ErrDeviceGone means the device token is no longer valid and should be deleted.
var ErrDeviceGone = errors.New("push: device token no longer valid")

// Notification is one rendered push.
type Notification struct {
	Title     string
	Body      string
	Type      string
	VehicleID string
	EventID   int64
	DeepLink  string
}

// Pusher delivers a notification to one device.
type Pusher interface {
	Push(ctx context.Context, d models.PushDevice, n Notification) error
}

// LogPusher logs pushes instead of sending them (development, or APNs unset).
type LogPusher struct{ Log *slog.Logger }

// Push logs the notification.
func (l LogPusher) Push(_ context.Context, d models.PushDevice, n Notification) error {
	tok := d.Token
	if len(tok) > 8 {
		tok = tok[:8] + "…"
	}
	l.Log.Info("push (log only)", "device", tok, "type", n.Type, "title", n.Title, "body", n.Body)
	return nil
}

// APNs sends alert pushes via Apple's HTTP/2 provider API with token auth.
type APNs struct {
	keyID, teamID, topic string
	key                  *ecdsa.PrivateKey
	client               *http.Client
	hosts                map[string]string // environment -> base URL
	now                  func() time.Time

	mu      sync.Mutex
	token   string
	tokenAt time.Time
}

// NewAPNs loads the .p8 signing key.
func NewAPNs(keyFile, keyID, teamID, topic string) (*APNs, error) {
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("apns: read key: %w", err)
	}
	key, err := ParseP8(raw)
	if err != nil {
		return nil, err
	}
	return &APNs{keyID: keyID, teamID: teamID, topic: topic, key: key,
		client: &http.Client{Timeout: 15 * time.Second}, now: time.Now,
		hosts: map[string]string{"production": "https://api.push.apple.com", "sandbox": "https://api.sandbox.push.apple.com"}}, nil
}

// ParseP8 parses an APNs .p8 (PKCS#8 PEM, P-256) key.
func ParseP8(raw []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("apns: key is not PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns: parse key: %w", err)
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apns: key is not ECDSA")
	}
	return ek, nil
}

// providerToken returns a cached ES256 JWT (Apple accepts tokens up to 60 min old).
func (a *APNs) providerToken() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if a.token != "" && now.Sub(a.tokenAt) < 50*time.Minute {
		return a.token, nil
	}
	enc := base64.RawURLEncoding.EncodeToString
	hdr, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": a.keyID})
	claims, _ := json.Marshal(map[string]any{"iss": a.teamID, "iat": now.Unix()})
	signing := enc(hdr) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, a.key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64) // JOSE ES256: fixed-width r || s
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	a.token, a.tokenAt = signing+"."+enc(sig), now
	return a.token, nil
}

type apnsPayload struct {
	APS struct {
		Alert struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		} `json:"alert"`
		Sound    string `json:"sound"`
		ThreadID string `json:"thread-id,omitempty"`
		Category string `json:"category,omitempty"`
	} `json:"aps"`
	Type      string `json:"type"`
	VehicleID string `json:"vehicle_id"`
	EventID   int64  `json:"event_id"`
	DeepLink  string `json:"deep_link,omitempty"`
}

// Push sends one alert notification.
func (a *APNs) Push(ctx context.Context, d models.PushDevice, n Notification) error {
	host, ok := a.hosts[d.Environment]
	if !ok {
		host = a.hosts["production"]
	}
	tok, err := a.providerToken()
	if err != nil {
		return err
	}
	var p apnsPayload
	p.APS.Alert.Title, p.APS.Alert.Body = n.Title, n.Body
	p.APS.Sound, p.APS.ThreadID, p.APS.Category = "default", n.VehicleID, n.Type
	p.Type, p.VehicleID, p.EventID, p.DeepLink = n.Type, n.VehicleID, n.EventID, n.DeepLink
	body, _ := json.Marshal(p)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/3/device/"+d.Token, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+tok)
	req.Header.Set("apns-topic", a.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("content-type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("apns: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var e struct {
		Reason string `json:"reason"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = json.Unmarshal(raw, &e)
	if resp.StatusCode == http.StatusGone || e.Reason == "BadDeviceToken" || e.Reason == "Unregistered" || e.Reason == "DeviceTokenNotForTopic" {
		return ErrDeviceGone
	}
	if e.Reason == "ExpiredProviderToken" {
		a.mu.Lock()
		a.token = ""
		a.mu.Unlock()
	}
	return fmt.Errorf("apns: %d %s", resp.StatusCode, e.Reason)
}
