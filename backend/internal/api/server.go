// Package api exposes the versioned HTTP + WebSocket API described in docs/api.md.
package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/altered-intelligence/milesight/internal/auth"
	"github.com/altered-intelligence/milesight/internal/commands"
	"github.com/altered-intelligence/milesight/internal/config"
	"github.com/altered-intelligence/milesight/internal/ids"
	"github.com/altered-intelligence/milesight/internal/ingest"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/realtime"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/tesla"
	"github.com/altered-intelligence/milesight/internal/transport"
)

// Server holds the API's dependencies.
type Server struct {
	Cfg        config.Config
	Store      store.Store
	Sessions   *auth.Sessions
	Creds      *tesla.Credentials
	FleetOAuth *tesla.OAuth // nil when Fleet OAuth is not configured
	OwnerOAuth *tesla.OAuth // nil when Owner token login is disabled
	Ingest     *ingest.Manager
	Commands   *commands.Service
	Hub        *realtime.Hub
	Transports transport.Registry
	Log        *slog.Logger
	Version    string
	Now        func() time.Time
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Handler builds the routed, middleware-wrapped handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	a := s.authed
	idem := s.idempotent

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /v1/config", s.getConfig)

	// Auth
	mux.HandleFunc("POST /v1/auth/tesla/authorize", s.teslaAuthorize)
	mux.HandleFunc("GET /v1/auth/tesla/redirect", s.teslaRedirect)
	mux.HandleFunc("POST /v1/auth/tesla/callback", s.teslaCallback)
	mux.HandleFunc("POST /v1/auth/dev/login", s.devLogin)
	mux.HandleFunc("POST /v1/auth/dev/tesla-token", s.ownerTokenLogin)
	mux.HandleFunc("POST /v1/auth/refresh", s.refresh)
	mux.HandleFunc("DELETE /v1/auth/session", a(s.signOut))
	mux.HandleFunc("GET /v1/me", a(s.getMe))
	mux.HandleFunc("PATCH /v1/me", a(s.patchMe))
	mux.HandleFunc("DELETE /v1/account", a(s.deleteAccount))

	// Vehicles & live state
	mux.HandleFunc("GET /v1/vehicles", a(s.listVehicles))
	mux.HandleFunc("POST /v1/vehicles/sync", a(s.syncVehicles))
	mux.HandleFunc("GET /v1/vehicles/{id}", a(s.getVehicle))
	mux.HandleFunc("PATCH /v1/vehicles/{id}", a(s.patchVehicle))
	mux.HandleFunc("POST /v1/vehicles/{id}/wake", a(idem(s.wakeVehicle)))
	mux.HandleFunc("GET /v1/vehicles/{id}/live", a(s.liveState))
	mux.HandleFunc("GET /v1/vehicles/{id}/ws", s.websocket) // authenticates itself (header or query)
	mux.HandleFunc("POST /v1/vehicles/{id}/commands/{name}", a(idem(s.command)))

	// History & analytics
	mux.HandleFunc("GET /v1/vehicles/{id}/telemetry", a(s.telemetry))
	mux.HandleFunc("GET /v1/vehicles/{id}/trips", a(s.listTrips))
	mux.HandleFunc("GET /v1/vehicles/{id}/trips/{trip_id}", a(s.getTrip))
	mux.HandleFunc("GET /v1/vehicles/{id}/charges", a(s.listCharges))
	mux.HandleFunc("GET /v1/vehicles/{id}/charges/{session_id}", a(s.getCharge))
	mux.HandleFunc("GET /v1/vehicles/{id}/stats/{period}", a(s.stats))
	mux.HandleFunc("GET /v1/vehicles/{id}/battery/health", a(s.batteryHealth))
	mux.HandleFunc("GET /v1/vehicles/{id}/export", a(s.export))

	// Places, rules, push, events, billing
	mux.HandleFunc("GET /v1/geofences", a(s.listGeofences))
	mux.HandleFunc("POST /v1/geofences", a(idem(s.createGeofence)))
	mux.HandleFunc("PATCH /v1/geofences/{id}", a(s.patchGeofence))
	mux.HandleFunc("DELETE /v1/geofences/{id}", a(s.deleteGeofence))
	mux.HandleFunc("GET /v1/rules", a(s.getRules))
	mux.HandleFunc("PUT /v1/rules", a(s.putRules))
	mux.HandleFunc("POST /v1/rules", a(s.putRules))
	mux.HandleFunc("GET /v1/push/devices", a(s.listDevices))
	mux.HandleFunc("POST /v1/push/devices", a(s.registerDevice))
	mux.HandleFunc("DELETE /v1/push/devices/{token}", a(s.deleteDevice))
	mux.HandleFunc("GET /v1/events", a(s.listEvents))
	mux.HandleFunc("GET /v1/billing/plan", a(s.billingPlan))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, r, http.StatusNotFound, "not_found", "no such endpoint")
	})
	return s.middleware(mux)
}

// ---------------------------------------------------------------- middleware

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxUserID
)

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(ctxRequestID).(string)
	return id
}

func userID(r *http.Request) string {
	id, _ := r.Context().Value(ctxUserID).(string)
	return id
}

// statusWriter captures the status code while staying hijackable for WebSockets.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	w.status = http.StatusSwitchingProtocols
	return h.Hijack()
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) middleware(next http.Handler) http.Handler {
	allowAll := false
	allowed := map[string]bool{}
	for _, o := range s.Cfg.CORSOrigins {
		if o == "*" {
			allowAll = true
		}
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rid := r.Header.Get("X-Request-ID")
		if rid == "" || len(rid) > 64 {
			rid = ids.New()
		}
		w.Header().Set("X-Request-ID", rid)
		ctx := context.WithValue(r.Context(), ctxRequestID, rid)
		r = r.WithContext(ctx)
		sw := &statusWriter{ResponseWriter: w}

		if origin := r.Header.Get("Origin"); origin != "" && (allowAll || allowed[origin]) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-ID")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Expose-Headers", "X-Request-ID, Retry-After")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(sw, r.Body, 1<<20)
		}
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic", "request_id", rid, "panic", rec, "stack", string(debug.Stack()))
				if sw.status == 0 {
					writeErr(sw, r, http.StatusInternalServerError, "internal", "internal error")
				}
			}
			s.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
				"ms", time.Since(start).Milliseconds(), "request_id", rid)
		}()
		next.ServeHTTP(sw, r)
	})
}

// bearer extracts the access token from the Authorization header (or, for
// WebSocket upgrades only, the access_token query parameter).
func bearer(r *http.Request, allowQuery bool) string {
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if allowQuery {
		return r.URL.Query().Get("access_token")
	}
	return ""
}

func (s *Server) authenticate(r *http.Request, allowQuery bool) (string, bool) {
	tok := bearer(r, allowQuery)
	if tok == "" {
		return "", false
	}
	c, err := s.Sessions.Signer.Verify(tok)
	if err != nil {
		return "", false
	}
	return c.Subject, true
}

func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, ok := s.authenticate(r, false)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="milesight"`)
			writeErr(w, r, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxUserID, uid)))
	}
}

// bufferWriter records a response so it can be cached for idempotent replay.
type bufferWriter struct {
	header http.Header
	status int
	body   []byte
}

func (b *bufferWriter) Header() http.Header { return b.header }
func (b *bufferWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	b.body = append(b.body, p...)
	return len(p), nil
}
func (b *bufferWriter) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

// idempotent honors the Idempotency-Key header on mutating requests: the first
// response (non-5xx) is stored for 24 h and replayed for retries with the same
// key and body; a different body with the same key is rejected.
func (s *Server) idempotent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next(w, r)
			return
		}
		if len(key) > 128 {
			writeErr(w, r, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must be at most 128 characters")
			return
		}
		body, err := readBody(r)
		if err != nil {
			writeErr(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "request body too large")
			return
		}
		sum := sha256.Sum256([]byte(r.Method + " " + r.URL.Path + "\n" + string(body)))
		hash := hex.EncodeToString(sum[:])
		uid := userID(r)
		if rec, err := s.Store.GetIdempotency(r.Context(), uid, key); err == nil {
			if rec.RequestHash != hash {
				writeErr(w, r, http.StatusUnprocessableEntity, "idempotency_key_reused", "Idempotency-Key was used with a different request")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(rec.StatusCode)
			_, _ = w.Write(rec.Body)
			return
		}
		buf := &bufferWriter{header: w.Header()}
		next(buf, r)
		if buf.status == 0 {
			buf.status = http.StatusOK
		}
		if buf.status < 500 {
			_ = s.Store.SaveIdempotency(r.Context(), &store.IdempotencyRecord{UserID: uid, Key: key, RequestHash: hash,
				StatusCode: buf.status, Body: buf.body, CreatedAt: s.now()})
		}
		w.WriteHeader(buf.status)
		_, _ = w.Write(buf.body)
	}
}

// fail maps domain errors onto HTTP responses.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *commands.ValidationError
	var rl *transport.RateLimitError
	var ae *transport.APIError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, "not_found", "not found")
	case errors.As(err, &ve):
		writeErr(w, r, http.StatusBadRequest, "invalid_command", ve.Msg)
	case errors.Is(err, commands.ErrForbidden):
		writeErr(w, r, http.StatusForbidden, "forbidden", err.Error())
	case errors.Is(err, commands.ErrRateLimited):
		w.Header().Set("Retry-After", "30")
		writeErr(w, r, http.StatusTooManyRequests, "rate_limited", err.Error())
	case errors.Is(err, transport.ErrReauthRequired):
		writeErr(w, r, http.StatusConflict, "tesla_reauth_required", "Tesla access expired. Sign in with Tesla again.")
	case errors.Is(err, transport.ErrVehicleUnavailable), errors.Is(err, commands.ErrWakeTimeout):
		writeErr(w, r, http.StatusGatewayTimeout, "vehicle_unavailable", "The vehicle is asleep or offline and did not respond.")
	case errors.Is(err, transport.ErrNotFound):
		writeErr(w, r, http.StatusNotFound, "vehicle_not_found", "Tesla no longer lists this vehicle on your account.")
	case errors.As(err, &rl):
		w.Header().Set("Retry-After", fmt.Sprint(int(rl.RetryAfter.Seconds())))
		writeErr(w, r, http.StatusTooManyRequests, "tesla_rate_limited", "Tesla rate limit reached; try again shortly.")
	case errors.As(err, &ae):
		writeErr(w, r, http.StatusBadGateway, "tesla_error", ae.Message)
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, r, http.StatusGatewayTimeout, "timeout", "the request timed out")
	default:
		s.Log.Error("request failed", "request_id", requestID(r), "path", r.URL.Path, "err", err)
		writeErr(w, r, http.StatusInternalServerError, "internal", "internal error")
	}
}

// vehicle loads the {id} vehicle if the caller is a member.
func (s *Server) vehicle(w http.ResponseWriter, r *http.Request) (*store.VehicleRecord, string, bool) {
	id := r.PathValue("id")
	if !ids.IsUUID(id) {
		writeErr(w, r, http.StatusNotFound, "not_found", "vehicle not found")
		return nil, "", false
	}
	v, role, err := s.Store.GetUserVehicle(r.Context(), userID(r), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, r, http.StatusNotFound, "not_found", "vehicle not found")
		} else {
			s.fail(w, r, err)
		}
		return nil, "", false
	}
	return v, role, true
}

func (s *Server) user(w http.ResponseWriter, r *http.Request) (*models.User, bool) {
	u, err := s.Store.GetUser(r.Context(), userID(r))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, r, http.StatusUnauthorized, "unauthorized", "account no longer exists")
		} else {
			s.fail(w, r, err)
		}
		return nil, false
	}
	return u, true
}
