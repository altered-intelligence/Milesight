package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/altered-intelligence/milesight/internal/models"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, r *http.Request, code int, errCode, msg string) {
	writeJSON(w, code, models.APIError{Error: models.ErrorBody{Code: errCode, Message: msg, RequestID: requestID(r)}})
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}

// decode reads a JSON body into dst; an empty body leaves dst untouched.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	b, err := readBody(r)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "request body too large")
		} else {
			writeErr(w, r, http.StatusBadRequest, "bad_request", "could not read body")
		}
		return false
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return true
	}
	if err := json.Unmarshal(b, dst); err != nil {
		writeErr(w, r, http.StatusBadRequest, "invalid_json", "malformed JSON: "+err.Error())
		return false
	}
	return true
}

// timeParam parses an RFC 3339 query parameter.
func timeParam(r *http.Request, key string, fallback time.Time) (time.Time, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, errors.New(key + " must be an RFC 3339 timestamp")
	}
	return t.UTC(), nil
}

func intParam(r *http.Request, key string, fallback, lo, hi int) (int, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, errors.New(key + " must be an integer between " + strconv.Itoa(lo) + " and " + strconv.Itoa(hi))
	}
	return n, nil
}

// Cursors are opaque to clients: base64url of an RFC 3339 timestamp.
func encodeCursor(t time.Time) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano)))
}

func decodeCursor(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, errors.New("invalid cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, string(b))
	if err != nil {
		return nil, errors.New("invalid cursor")
	}
	return &t, nil
}
