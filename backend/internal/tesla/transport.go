package tesla

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/altered-intelligence/milesight/internal/transport"
)

// TokenSource yields a valid Tesla access token (and the account's region).
type TokenSource interface {
	AccessToken(ctx context.Context, userID string, force bool) (token, region string, err error)
}

// RESTTransport implements transport.Transport over Tesla's REST APIs. The
// Fleet and Owner APIs share paths and payloads; they differ in base URL, how
// a vehicle is addressed (VIN vs numeric id) and how commands are signed.
type RESTTransport struct {
	name          string
	client        *Client
	tokens        TokenSource
	baseURL       func(region string) string
	byVIN         bool         // Fleet addresses vehicles by VIN; Owner by id
	commandBase   string       // optional vehicle-command proxy (Fleet)
	commandClient *http.Client // client trusting the proxy's certificate
	now           func() time.Time

	mu  sync.Mutex
	ids map[string]map[string]string // userID -> VIN -> tesla id (Owner API)
}

// NewFleetTransport is the production transport (ADR-002). commandProxyURL
// points at Tesla's vehicle-command HTTP proxy, which signs commands for
// vehicles that require the Vehicle Command Protocol (all 2021+ vehicles).
func NewFleetTransport(tokens TokenSource, commandProxyURL, commandProxyCA string) (*RESTTransport, error) {
	t := &RESTTransport{name: "fleet", client: NewClient(nil), tokens: tokens, baseURL: FleetBaseURL, byVIN: true,
		commandBase: commandProxyURL, now: time.Now}
	t.commandClient = t.client.HTTP
	if commandProxyCA != "" {
		pem, err := os.ReadFile(commandProxyCA)
		if err != nil {
			return nil, fmt.Errorf("fleet: read command proxy CA: %w", err)
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("fleet: command proxy CA contains no certificates")
		}
		t.commandClient = &http.Client{Timeout: defaultTimeout, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}}
	}
	return t, nil
}

// NewOwnerTransport is the legacy Owner API transport (development only, ADR-006).
// Tesla has restricted it heavily; most 2021+ vehicles reject unsigned commands.
func NewOwnerTransport(tokens TokenSource) *RESTTransport {
	return &RESTTransport{name: "owner", client: NewClient(nil), tokens: tokens,
		baseURL: func(string) string { return OwnerBaseURL }, now: time.Now}
}

func (t *RESTTransport) Name() string                     { return t.name }
func (t *RESTTransport) Policy() transport.PollPolicy     { return transport.DefaultPolicy() }
func (t *RESTTransport) vehiclePath(id string) string     { return "/api/1/vehicles/" + url.PathEscape(id) }
func (t *RESTTransport) withBase(region, p string) string { return t.baseURL(region) + p }

// call performs an authenticated request, refreshing the token once on 401.
func (t *RESTTransport) call(ctx context.Context, userID, method, path string, body, out any, viaProxy bool) error {
	for attempt := 0; attempt < 2; attempt++ {
		tok, region, err := t.tokens.AccessToken(ctx, userID, attempt > 0)
		if err != nil {
			return err
		}
		u := t.withBase(region, path)
		c := t.client
		if viaProxy && t.commandBase != "" {
			u = t.commandBase + path
			c = &Client{HTTP: t.commandClient}
		}
		err = c.Do(ctx, method, u, tok, body, out)
		if errors.Is(err, transport.ErrUnauthorized) && attempt == 0 {
			continue
		}
		return err
	}
	return transport.ErrUnauthorized
}

// ListVehicles lists vehicles (does not wake them).
func (t *RESTTransport) ListVehicles(ctx context.Context, userID string) ([]transport.Vehicle, error) {
	var list []VehicleSummary
	if err := t.call(ctx, userID, http.MethodGet, "/api/1/vehicles", nil, &list, false); err != nil {
		return nil, err
	}
	out := make([]transport.Vehicle, 0, len(list))
	ids := map[string]string{}
	for _, v := range list {
		id := v.IDS
		if id == "" {
			id = fmt.Sprint(v.ID)
		}
		ids[v.VIN] = id
		out = append(out, transport.Vehicle{TeslaID: id, VIN: v.VIN, DisplayName: v.DisplayName, State: strings.ToLower(v.State)})
	}
	t.mu.Lock()
	if t.ids == nil {
		t.ids = map[string]map[string]string{}
	}
	t.ids[userID] = ids
	t.mu.Unlock()
	return out, nil
}

// ref returns the path identifier for a vehicle.
func (t *RESTTransport) ref(ctx context.Context, userID, vin string) (string, error) {
	if t.byVIN {
		return vin, nil
	}
	t.mu.Lock()
	id, ok := t.ids[userID][vin]
	t.mu.Unlock()
	if ok {
		return id, nil
	}
	if _, err := t.ListVehicles(ctx, userID); err != nil {
		return "", err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if id, ok := t.ids[userID][vin]; ok {
		return id, nil
	}
	return "", transport.ErrNotFound
}

// VehicleState returns online|asleep|offline without waking.
func (t *RESTTransport) VehicleState(ctx context.Context, userID, vin string) (string, error) {
	ref, err := t.ref(ctx, userID, vin)
	if err != nil {
		return "", err
	}
	var v VehicleSummary
	if err := t.call(ctx, userID, http.MethodGet, t.vehiclePath(ref), nil, &v, false); err != nil {
		return "", err
	}
	return strings.ToLower(v.State), nil
}

// VehicleData reads the full snapshot; ErrVehicleUnavailable if asleep.
func (t *RESTTransport) VehicleData(ctx context.Context, userID, vin string) (*transport.Snapshot, error) {
	ref, err := t.ref(ctx, userID, vin)
	if err != nil {
		return nil, err
	}
	q := url.Values{"endpoints": {VehicleDataEndpoints}}
	var vd VehicleData
	if err := t.call(ctx, userID, http.MethodGet, t.vehiclePath(ref)+"/vehicle_data?"+q.Encode(), nil, &vd, false); err != nil {
		return nil, err
	}
	if vd.ChargeState == nil && vd.VehicleState == nil && vd.DriveState == nil {
		return nil, transport.ErrVehicleUnavailable
	}
	return ToSnapshot(&vd, t.now()), nil
}

// Wake sends wake_up (billed; used only on explicit user action).
func (t *RESTTransport) Wake(ctx context.Context, userID, vin string) error {
	ref, err := t.ref(ctx, userID, vin)
	if err != nil {
		return err
	}
	var v VehicleSummary
	return t.call(ctx, userID, http.MethodPost, t.vehiclePath(ref)+"/wake_up", nil, &v, false)
}

// Command sends one command, through the signing proxy when configured.
func (t *RESTTransport) Command(ctx context.Context, userID, vin string, cmd transport.Command) error {
	ref, err := t.ref(ctx, userID, vin)
	if err != nil {
		return err
	}
	body := cmd.Body
	if body == nil {
		body = map[string]any{}
	}
	var res struct {
		Result bool   `json:"result"`
		Reason string `json:"reason"`
	}
	if err := t.call(ctx, userID, http.MethodPost, t.vehiclePath(ref)+"/command/"+url.PathEscape(cmd.Name), body, &res, true); err != nil {
		return err
	}
	if !res.Result {
		reason := res.Reason
		if reason == "" {
			reason = "unknown"
		}
		return &transport.CommandError{Reason: reason}
	}
	return nil
}

var _ transport.Transport = (*RESTTransport)(nil)
