package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/altered-intelligence/milesight/internal/ids"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/realtime"
)

const (
	wsWriteWait  = 10 * time.Second
	wsPongWait   = 60 * time.Second
	wsPingPeriod = 25 * time.Second
)

// websocket streams realtime.Messages for one vehicle:
//
//	{"type":"state","vehicle_id":"…","at":"…","data":{LiveState}}
//	{"type":"event"|"trip"|"charge"|"command", …}
//
// Native clients authenticate with the Authorization header; browsers, which
// cannot set headers on WebSockets, may pass ?access_token=. The first message
// is always the current state. Clients may send {"type":"refresh"} to request
// an immediate read.
func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.authenticate(r, true)
	if !ok {
		writeErr(w, r, http.StatusUnauthorized, "unauthorized", "missing or invalid access token")
		return
	}
	vid := r.PathValue("id")
	if !ids.IsUUID(vid) {
		writeErr(w, r, http.StatusNotFound, "not_found", "vehicle not found")
		return
	}
	v, _, err := s.Store.GetUserVehicle(r.Context(), uid, vid)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, "not_found", "vehicle not found")
		return
	}
	up := websocket.Upgrader{
		ReadBufferSize: 1024, WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" { // native apps
				return true
			}
			for _, o := range s.Cfg.CORSOrigins {
				if o == "*" || o == origin {
					return true
				}
			}
			return false
		},
	}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already replied
	}
	defer conn.Close()

	sub := s.Hub.Subscribe(vid)
	defer s.Hub.Unsubscribe(sub)
	s.Ingest.Poke(vid, []time.Duration{0}) // someone's watching: refresh now

	// Reader: handles pongs, client refresh requests, and detects disconnects.
	done := make(chan struct{})
	conn.SetReadLimit(4096)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(wsPongWait)) })
	go func() {
		defer close(done)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var in struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(msg, &in) == nil && in.Type == "refresh" {
				s.Hub.MarkViewed(vid)
				s.Ingest.Poke(vid, []time.Duration{0})
			}
		}
	}()

	write := func(m realtime.Message) bool {
		_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
		return conn.WriteJSON(m) == nil
	}
	initial := models.LiveState{VehicleID: vid, State: v.State}
	if v.Latest != nil {
		initial = *v.Latest
		initial.State = v.State
	}
	if !write(realtime.Message{Type: realtime.TypeState, VehicleID: vid, At: s.now(), Data: initial}) {
		return
	}
	ping := time.NewTicker(wsPingPeriod)
	defer ping.Stop()
	for {
		select {
		case <-done:
			return
		case m, ok := <-sub.C():
			if !ok { // fell behind: tell the client to reconnect for a fresh snapshot
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "slow consumer"), time.Now().Add(wsWriteWait))
				return
			}
			if !write(m) {
				return
			}
		case <-ping.C:
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)) != nil {
				return
			}
		}
	}
}
