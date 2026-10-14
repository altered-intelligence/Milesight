package api

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/realtime"
)

func (s *Server) listVehicles(w http.ResponseWriter, r *http.Request) {
	vs, err := s.Store.ListUserVehicles(r.Context(), userID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if vs == nil {
		vs = []models.Vehicle{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"vehicles": vs})
}

// syncVehicles re-lists vehicles from Tesla (e.g. after adding a car).
func (s *Server) syncVehicles(w http.ResponseWriter, r *http.Request) {
	cred, err := s.Store.GetCredentials(r.Context(), userID(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	vs, err := s.Ingest.SyncAccount(ctx, userID(r), cred.Transport)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"vehicles": vs})
}

func (s *Server) getVehicle(w http.ResponseWriter, r *http.Request) {
	v, role, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, v.ToModel(role))
}

type vehiclePatch struct {
	DisplayName *string `json:"display_name"`
}

func (s *Server) patchVehicle(w http.ResponseWriter, r *http.Request) {
	v, role, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	var req vehiclePatch
	if !decode(w, r, &req) {
		return
	}
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if name == "" || utf8.RuneCountInString(name) > 40 {
			writeErr(w, r, http.StatusBadRequest, "invalid_name", "display_name must be 1–40 characters")
			return
		}
		if role != "owner" {
			writeErr(w, r, http.StatusForbidden, "forbidden", "only owners can rename a vehicle")
			return
		}
		if err := s.Store.RenameVehicle(r.Context(), v.ID, name); err != nil {
			s.fail(w, r, err)
			return
		}
		v.DisplayName = name
	}
	writeJSON(w, http.StatusOK, v.ToModel(role))
}

func (s *Server) wakeVehicle(w http.ResponseWriter, r *http.Request) {
	v, role, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	if role != "owner" {
		writeErr(w, r, http.StatusForbidden, "forbidden", "viewers cannot wake the vehicle")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	woke, err := s.Commands.Wake(ctx, userID(r), v)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": models.StateOnline, "woke_up": woke})
}

// liveState returns the latest snapshot. Reading it marks the car as watched
// (faster polling) and, if the data is stale, asks ingestion to refresh now.
func (s *Server) liveState(w http.ResponseWriter, r *http.Request) {
	v, _, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	s.Hub.MarkViewed(v.ID)
	if v.Latest == nil || s.now().Sub(v.Latest.UpdatedAt) > 30*time.Second {
		s.Ingest.Poke(v.ID, []time.Duration{0})
	}
	ls := models.LiveState{VehicleID: v.ID, State: v.State}
	if v.Latest != nil {
		ls = *v.Latest
		ls.State = v.State
	}
	writeJSON(w, http.StatusOK, ls)
}

func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	v, role, ok := s.vehicle(w, r)
	if !ok {
		return
	}
	var p models.CommandParams
	if !decode(w, r, &p) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := s.Commands.Execute(ctx, userID(r), v, role, r.PathValue("name"), p)
	if res != nil {
		s.Hub.Publish(realtime.Message{Type: realtime.TypeCommand, VehicleID: v.ID, Data: res})
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if res.Status != "ok" {
		status = http.StatusConflict // the car refused; body carries the reason
	}
	writeJSON(w, status, res)
}
