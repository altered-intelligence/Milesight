package api

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/altered-intelligence/milesight/internal/auth"
	"github.com/altered-intelligence/milesight/internal/ids"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
	"github.com/altered-intelligence/milesight/internal/tesla"
	"github.com/altered-intelligence/milesight/internal/transport"
)

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, models.ServerConfig{
		Version: s.Version, Transport: s.Cfg.Transport, DevLogin: s.Cfg.DevLogin,
		TeslaOAuth: s.FleetOAuth != nil, OwnerTokenLogin: s.OwnerOAuth != nil, Regions: []string{"na", "eu", "cn"},
	})
}

// ---------------------------------------------------------------- Fleet OAuth

type authorizeRequest struct {
	Region string `json:"region"`
}

type authorizeResponse struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
	RedirectURI  string `json:"redirect_uri"`
	AppRedirect  string `json:"app_redirect"`
}

func (s *Server) teslaAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.FleetOAuth == nil {
		writeErr(w, r, http.StatusNotImplemented, "tesla_oauth_disabled", "Sign in with Tesla is not configured on this server")
		return
	}
	var req authorizeRequest
	if !decode(w, r, &req) {
		return
	}
	region := strings.ToLower(req.Region)
	if region == "" {
		region = s.Cfg.Tesla.DefaultRegion
	}
	if _, ok := tesla.FleetBaseURLs[region]; !ok {
		writeErr(w, r, http.StatusBadRequest, "invalid_region", "region must be na, eu or cn")
		return
	}
	now := s.now()
	st := &store.AuthState{State: ids.Secret(24), Transport: "fleet", Region: region, RedirectURI: s.Cfg.Tesla.RedirectURI,
		AppRedirect: s.Cfg.AppRedirect, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	if err := s.Store.SaveAuthState(r.Context(), st); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, authorizeResponse{
		AuthorizeURL: s.FleetOAuth.AuthorizeURL(st.RedirectURI, st.State, ""),
		State:        st.State, RedirectURI: st.RedirectURI, AppRedirect: st.AppRedirect,
	})
}

// teslaRedirect is the OAuth redirect_uri registered with Tesla. It bounces the
// browser to the app's URL scheme (fixed by config, so not an open redirect);
// the app then POSTs the code to /v1/auth/tesla/callback.
func (s *Server) teslaRedirect(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	target, err := url.Parse(s.Cfg.AppRedirect)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, "misconfigured", "invalid MILESIGHT_APP_REDIRECT")
		return
	}
	out := target.Query()
	for _, k := range []string{"code", "state", "error", "error_description"} {
		if v := q.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	target.RawQuery = out.Encode()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Location", target.String())
	w.WriteHeader(http.StatusFound)
	// Fallback for browsers that don't follow custom-scheme redirects.
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Milesight</title>` +
		`<p>Return to the Milesight app. <a href="` + html.EscapeString(target.String()) + `">Open Milesight</a></p>`))
}

type callbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

func (s *Server) teslaCallback(w http.ResponseWriter, r *http.Request) {
	if s.FleetOAuth == nil {
		writeErr(w, r, http.StatusNotImplemented, "tesla_oauth_disabled", "Sign in with Tesla is not configured on this server")
		return
	}
	var req callbackRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Code == "" || req.State == "" {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "code and state are required")
		return
	}
	st, err := s.Store.ConsumeAuthState(r.Context(), req.State, s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, r, http.StatusBadRequest, "invalid_state", "sign-in expired or was already used; start again")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tok, err := s.FleetOAuth.Exchange(r.Context(), req.Code, st.RedirectURI, tesla.FleetBaseURL(st.Region), st.CodeVerifier)
	if err != nil {
		if errors.Is(err, transport.ErrReauthRequired) {
			writeErr(w, r, http.StatusBadRequest, "invalid_code", "Tesla rejected the authorization code; start again")
			return
		}
		s.fail(w, r, err)
		return
	}
	s.completeTeslaLogin(w, r, "fleet", st.Region, tok)
}

// ownerTokenLogin (development) signs in with an Owner API refresh token.
type ownerTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) ownerTokenLogin(w http.ResponseWriter, r *http.Request) {
	if s.OwnerOAuth == nil {
		writeErr(w, r, http.StatusNotImplemented, "owner_login_disabled", "Owner API token sign-in is disabled on this server")
		return
	}
	var req ownerTokenRequest
	if !decode(w, r, &req) {
		return
	}
	if len(req.RefreshToken) < 20 {
		writeErr(w, r, http.StatusBadRequest, "bad_request", "refresh_token is required")
		return
	}
	tok, err := s.OwnerOAuth.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, transport.ErrReauthRequired) {
			writeErr(w, r, http.StatusBadRequest, "invalid_token", "Tesla rejected the refresh token")
			return
		}
		s.fail(w, r, err)
		return
	}
	s.completeTeslaLogin(w, r, "owner", "na", tok)
}

func (s *Server) completeTeslaLogin(w http.ResponseWriter, r *http.Request, transportName, region string, tok *tesla.Token) {
	id, err := tesla.IdentityOf(tok)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, "tesla_error", "Tesla did not return an account identity")
		return
	}
	user, err := s.Store.UpsertUser(r.Context(), "tesla:"+id.Subject, id.Email, transportName)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Creds.Save(r.Context(), user.ID, transportName, region, tok); err != nil {
		s.fail(w, r, err)
		return
	}
	s.syncInBackground(user.ID, transportName)
	s.issueSession(w, r, user)
}

// syncInBackground links the account's vehicles without delaying sign-in.
func (s *Server) syncInBackground(userID, transportName string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if _, err := s.Ingest.SyncAccount(ctx, userID, transportName); err != nil {
			s.Log.Warn("vehicle sync after sign-in failed", "user", userID, "err", err)
		}
	}()
}

// ---------------------------------------------------------------- dev login (simulator)

type devLoginRequest struct {
	Email string `json:"email"`
}

func (s *Server) devLogin(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.DevLogin {
		writeErr(w, r, http.StatusNotFound, "not_found", "no such endpoint")
		return
	}
	var req devLoginRequest
	if !decode(w, r, &req) {
		return
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(req.Email))
	if err != nil || len(addr.Address) > 254 {
		writeErr(w, r, http.StatusBadRequest, "invalid_email", "a valid email is required")
		return
	}
	email := strings.ToLower(addr.Address)
	user, err := s.Store.UpsertUser(r.Context(), "dev:"+email, email, "sim")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Simulator accounts still get a credentials row so ingestion picks them up.
	if _, err := s.Store.GetCredentials(r.Context(), user.ID); errors.Is(err, store.ErrNotFound) {
		if err := s.Creds.Save(r.Context(), user.ID, "sim", "na", &tesla.Token{AccessToken: "sim", RefreshToken: "sim", ExpiresIn: 10 * 365 * 24 * 3600}); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	// Link synchronously so the first vehicles request already has cars.
	if _, err := s.Ingest.SyncAccount(r.Context(), user.ID, "sim"); err != nil {
		s.fail(w, r, err)
		return
	}
	s.issueSession(w, r, user)
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, user *models.User) {
	sess, err := s.Sessions.Create(r.Context(), user)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// ---------------------------------------------------------------- session lifecycle

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	sess, err := s.Sessions.Refresh(r.Context(), req.RefreshToken)
	if errors.Is(err, auth.ErrRefreshInvalid) {
		writeErr(w, r, http.StatusUnauthorized, "invalid_refresh_token", "session expired; sign in again")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// signOut revokes this device's session family (body refresh_token), or every
// session with ?all=true.
func (s *Server) signOut(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decode(w, r, &req) {
		return
	}
	var err error
	if r.URL.Query().Get("all") == "true" || req.RefreshToken == "" {
		err = s.Sessions.RevokeAll(r.Context(), userID(r))
	} else {
		err = s.Sessions.RevokeFamilyOf(r.Context(), req.RefreshToken)
		if errors.Is(err, store.ErrNotFound) {
			err = nil
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	if u, ok := s.user(w, r); ok {
		writeJSON(w, http.StatusOK, u)
	}
}

type preferencesPatch struct {
	DistanceUnit         *string  `json:"distance_unit"`
	TemperatureUnit      *string  `json:"temperature_unit"`
	Currency             *string  `json:"currency"`
	EnergyRatePerKWh     *float64 `json:"energy_rate_per_kwh"`
	FastChargeRatePerKWh *float64 `json:"fast_charge_rate_per_kwh"`
	TimeZone             *string  `json:"time_zone"`
}

type mePatch struct {
	Preferences *preferencesPatch `json:"preferences"`
}

func (s *Server) patchMe(w http.ResponseWriter, r *http.Request) {
	u, ok := s.user(w, r)
	if !ok {
		return
	}
	var req mePatch
	if !decode(w, r, &req) {
		return
	}
	if p := req.Preferences; p != nil {
		np := u.Preferences
		bad := func(msg string) { writeErr(w, r, http.StatusBadRequest, "invalid_preferences", msg) }
		if p.DistanceUnit != nil {
			if *p.DistanceUnit != "km" && *p.DistanceUnit != "mi" {
				bad("distance_unit must be km or mi")
				return
			}
			np.DistanceUnit = *p.DistanceUnit
		}
		if p.TemperatureUnit != nil {
			if *p.TemperatureUnit != "c" && *p.TemperatureUnit != "f" {
				bad("temperature_unit must be c or f")
				return
			}
			np.TemperatureUnit = *p.TemperatureUnit
		}
		if p.Currency != nil {
			c := strings.ToUpper(*p.Currency)
			if len(c) != 3 || strings.Trim(c, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
				bad("currency must be an ISO 4217 code")
				return
			}
			np.Currency = c
		}
		for _, rate := range []struct {
			v   *float64
			dst *float64
		}{{p.EnergyRatePerKWh, &np.EnergyRatePerKWh}, {p.FastChargeRatePerKWh, &np.FastChargeRatePerKWh}} {
			if rate.v != nil {
				if *rate.v < 0 || *rate.v > 10 {
					bad("energy rates must be between 0 and 10 per kWh")
					return
				}
				*rate.dst = *rate.v
			}
		}
		if p.TimeZone != nil {
			if _, err := time.LoadLocation(*p.TimeZone); err != nil || *p.TimeZone == "" || *p.TimeZone == "Local" {
				bad("time_zone must be an IANA zone such as America/Chicago")
				return
			}
			np.TimeZone = *p.TimeZone
		}
		if err := s.Store.UpdatePreferences(r.Context(), u.ID, np); err != nil {
			s.fail(w, r, err)
			return
		}
		u.Preferences = np
	}
	writeJSON(w, http.StatusOK, u)
}

// deleteAccount permanently deletes the account and its data (App Store requirement).
// Tesla consent is revoked by the user at Tesla; the response tells the client where.
func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	if err := s.Store.DeleteUser(r.Context(), uid); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Ingest.Discover()
	resp := map[string]string{"status": "deleted"}
	if s.FleetOAuth != nil {
		resp["revoke_tesla_access_url"] = "https://auth.tesla.com/user/revoke/consent?revoke_client_id=" + url.QueryEscape(s.FleetOAuth.ClientID)
	}
	writeJSON(w, http.StatusOK, resp)
}
