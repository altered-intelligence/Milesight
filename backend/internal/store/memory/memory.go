// Package memory is an in-process Store for zero-infrastructure development and
// tests. Data lives for the life of the process.
package memory

import (
	"context"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/altered-intelligence/milesight/internal/ids"
	"github.com/altered-intelligence/milesight/internal/models"
	"github.com/altered-intelligence/milesight/internal/store"
)

type userRow struct {
	user       models.User
	externalID string
}

// Store implements store.Store in memory.
type Store struct {
	mu sync.RWMutex

	users      map[string]*userRow
	usersByExt map[string]string
	creds      map[string]store.Credentials
	refresh    map[string]*store.RefreshToken
	refreshBy  map[string]string // hex(hash) -> id
	authStates map[string]store.AuthState

	vehicles  map[string]*store.VehicleRecord
	byVIN     map[string]string
	links     map[string]map[string]string // vehicleID -> userID -> role
	procState map[string][]byte

	telemetry map[string][]models.TelemetryPoint
	trips     map[string]map[string]models.Trip
	charges   map[string]map[string]models.ChargeSession
	drain     map[string][]models.DrainSegment

	events    []models.Event
	nextEvent int64

	rules     map[string][]models.Rule
	geofences map[string]map[string]models.Geofence
	devices   map[string]map[string]models.PushDevice
	idem      map[string]store.IdempotencyRecord
	cmdLog    []store.CommandLog
}

// New returns an empty store.
func New() *Store {
	return &Store{
		users: map[string]*userRow{}, usersByExt: map[string]string{},
		creds: map[string]store.Credentials{}, refresh: map[string]*store.RefreshToken{},
		refreshBy: map[string]string{}, authStates: map[string]store.AuthState{},
		vehicles: map[string]*store.VehicleRecord{}, byVIN: map[string]string{},
		links: map[string]map[string]string{}, procState: map[string][]byte{},
		telemetry: map[string][]models.TelemetryPoint{}, trips: map[string]map[string]models.Trip{},
		charges: map[string]map[string]models.ChargeSession{}, drain: map[string][]models.DrainSegment{},
		rules: map[string][]models.Rule{}, geofences: map[string]map[string]models.Geofence{},
		devices: map[string]map[string]models.PushDevice{}, idem: map[string]store.IdempotencyRecord{},
	}
}

var _ store.Store = (*Store)(nil)

func (s *Store) Migrate(context.Context) error { return nil }
func (s *Store) Close()                        {}

// ---------------------------------------------------------------- users

func (s *Store) UpsertUser(_ context.Context, externalID, email, transport string) (*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.usersByExt[externalID]; ok {
		u := s.users[id]
		if email != "" {
			u.user.Email = email
		}
		u.user.Transport = transport
		cp := u.user
		return &cp, nil
	}
	u := &userRow{externalID: externalID, user: models.User{
		ID: ids.New(), Email: email, Plan: "free", Transport: transport,
		CreatedAt: time.Now().UTC(), Preferences: models.DefaultPreferences(),
	}}
	s.users[u.user.ID] = u
	s.usersByExt[externalID] = u.user.ID
	cp := u.user
	return &cp, nil
}

func (s *Store) GetUser(_ context.Context, id string) (*models.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := u.user
	return &cp, nil
}

func (s *Store) UpdatePreferences(_ context.Context, userID string, p models.Preferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	u.user.Preferences = p
	return nil
}

func (s *Store) DeleteUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	delete(s.usersByExt, u.externalID)
	delete(s.users, userID)
	delete(s.creds, userID)
	for id, t := range s.refresh {
		if t.UserID == userID {
			delete(s.refreshBy, hex.EncodeToString(t.Hash))
			delete(s.refresh, id)
		}
	}
	delete(s.rules, userID)
	delete(s.geofences, userID)
	delete(s.devices, userID)
	for k := range s.idem {
		if strings.HasPrefix(k, userID+"|") {
			delete(s.idem, k)
		}
	}
	for vid, members := range s.links {
		delete(members, userID)
		if len(members) == 0 {
			s.deleteVehicleLocked(vid)
		}
	}
	return nil
}

func (s *Store) deleteVehicleLocked(vid string) {
	if v, ok := s.vehicles[vid]; ok {
		delete(s.byVIN, v.VIN)
	}
	delete(s.vehicles, vid)
	delete(s.links, vid)
	delete(s.procState, vid)
	delete(s.telemetry, vid)
	delete(s.trips, vid)
	delete(s.charges, vid)
	delete(s.drain, vid)
	kept := s.events[:0]
	for _, e := range s.events {
		if e.VehicleID != vid {
			kept = append(kept, e)
		}
	}
	s.events = kept
}

// ---------------------------------------------------------------- credentials

func (s *Store) SaveCredentials(_ context.Context, c *store.Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[c.UserID]; !ok {
		return store.ErrNotFound
	}
	cp := *c
	cp.UpdatedAt = time.Now().UTC()
	s.creds[c.UserID] = cp
	return nil
}

func (s *Store) GetCredentials(_ context.Context, userID string) (*store.Credentials, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.creds[userID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &c, nil
}

// ---------------------------------------------------------------- refresh tokens

func (s *Store) InsertRefreshToken(_ context.Context, t *store.RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.ID == "" {
		t.ID = ids.New()
	}
	cp := *t
	s.refresh[t.ID] = &cp
	s.refreshBy[hex.EncodeToString(t.Hash)] = t.ID
	return nil
}

func (s *Store) GetRefreshTokenByHash(_ context.Context, hash []byte) (*store.RefreshToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.refreshBy[hex.EncodeToString(hash)]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *s.refresh[id]
	return &cp, nil
}

func (s *Store) MarkRefreshTokenUsed(_ context.Context, id string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.refresh[id]
	if !ok {
		return false, store.ErrNotFound
	}
	if t.UsedAt != nil || t.RevokedAt != nil {
		return false, nil
	}
	t.UsedAt = &at
	return true, nil
}

func (s *Store) RevokeRefreshFamily(_ context.Context, familyID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.refresh {
		if t.FamilyID == familyID && t.RevokedAt == nil {
			t.RevokedAt = &at
		}
	}
	return nil
}

func (s *Store) RevokeUserRefreshTokens(_ context.Context, userID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.refresh {
		if t.UserID == userID && t.RevokedAt == nil {
			t.RevokedAt = &at
		}
	}
	return nil
}

// ---------------------------------------------------------------- auth states

func (s *Store) SaveAuthState(_ context.Context, a *store.AuthState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authStates[a.State] = *a
	return nil
}

func (s *Store) ConsumeAuthState(_ context.Context, state string, now time.Time) (*store.AuthState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.authStates[state]
	delete(s.authStates, state)
	if !ok || now.After(a.ExpiresAt) {
		return nil, store.ErrNotFound
	}
	return &a, nil
}

// ---------------------------------------------------------------- vehicles

func (s *Store) UpsertVehicle(_ context.Context, v *store.VehicleRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.byVIN[v.VIN]; ok {
		cur := s.vehicles[id]
		v.ID = id
		cur.TeslaID = v.TeslaID
		if v.DisplayName != "" && cur.DisplayName == "" {
			cur.DisplayName = v.DisplayName
		}
		if v.Model != "" {
			cur.Model = v.Model
		}
		if v.Trim != "" {
			cur.Trim = v.Trim
		}
		if v.Color != "" {
			cur.Color = v.Color
		}
		if v.Region != "" {
			cur.Region = v.Region
		}
		return nil
	}
	if v.ID == "" {
		v.ID = ids.New()
	}
	if v.State == "" {
		v.State = models.StateUnknown
	}
	if v.SyncStatus == "" {
		v.SyncStatus = "idle"
	}
	if v.Region == "" {
		v.Region = "na"
	}
	v.CreatedAt = time.Now().UTC()
	cp := *v
	s.vehicles[v.ID] = &cp
	s.byVIN[v.VIN] = v.ID
	return nil
}

func (s *Store) LinkVehicle(_ context.Context, userID, vehicleID, role string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vehicles[vehicleID]; !ok {
		return store.ErrNotFound
	}
	if s.links[vehicleID] == nil {
		s.links[vehicleID] = map[string]string{}
	}
	s.links[vehicleID][userID] = role
	return nil
}

func (s *Store) ListUserVehicles(_ context.Context, userID string) ([]models.Vehicle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.Vehicle
	for vid, members := range s.links {
		if role, ok := members[userID]; ok {
			out = append(out, s.vehicles[vid].ToModel(role))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DisplayName != out[j].DisplayName {
			return out[i].DisplayName < out[j].DisplayName
		}
		return out[i].VIN < out[j].VIN
	})
	return out, nil
}

func (s *Store) GetUserVehicle(_ context.Context, userID, vehicleID string) (*store.VehicleRecord, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	role, ok := s.links[vehicleID][userID]
	if !ok {
		return nil, "", store.ErrNotFound
	}
	cp := *s.vehicles[vehicleID]
	return &cp, role, nil
}

func (s *Store) GetVehicle(_ context.Context, vehicleID string) (*store.VehicleRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *Store) VehicleUsers(_ context.Context, vehicleID string) ([]store.VehicleUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []store.VehicleUser
	for uid, role := range s.links[vehicleID] {
		out = append(out, store.VehicleUser{UserID: uid, Role: role})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (s *Store) RenameVehicle(_ context.Context, vehicleID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return store.ErrNotFound
	}
	v.DisplayName = name
	return nil
}

func (s *Store) UpdateVehicleStatus(_ context.Context, vehicleID, state, syncStatus string, lastSeen *time.Time, latest *models.LiveState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return store.ErrNotFound
	}
	if state != "" {
		v.State = state
	}
	if syncStatus != "" {
		v.SyncStatus = syncStatus
	}
	if lastSeen != nil {
		t := *lastSeen
		v.LastSeen = &t
	}
	if latest != nil {
		cp := *latest
		v.Latest = &cp
	}
	return nil
}

func (s *Store) ListPollTargets(_ context.Context) ([]store.PollTarget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []store.PollTarget
	for vid, members := range s.links {
		// Prefer an owner with credentials; deterministic by user id.
		uids := make([]string, 0, len(members))
		for uid := range members {
			uids = append(uids, uid)
		}
		sort.Strings(uids)
		for _, uid := range uids {
			if members[uid] != "owner" {
				continue
			}
			if c, ok := s.creds[uid]; ok {
				out = append(out, store.PollTarget{Vehicle: *s.vehicles[vid], UserID: uid, Transport: c.Transport})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Vehicle.ID < out[j].Vehicle.ID })
	return out, nil
}

func (s *Store) GetProcessorState(_ context.Context, vehicleID string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.procState[vehicleID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return append([]byte(nil), b...), nil
}

func (s *Store) SaveProcessorState(_ context.Context, vehicleID string, state []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.procState[vehicleID] = append([]byte(nil), state...)
	return nil
}

// ---------------------------------------------------------------- telemetry

func (s *Store) InsertTelemetry(_ context.Context, vehicleID string, pts []models.TelemetryPoint) error {
	if len(pts) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.telemetry[vehicleID]
	sorted := len(cur) == 0 || !pts[0].Time.Before(cur[len(cur)-1].Time)
	cur = append(cur, pts...)
	if !sorted || !sort.SliceIsSorted(pts, func(i, j int) bool { return pts[i].Time.Before(pts[j].Time) }) {
		sort.SliceStable(cur, func(i, j int) bool { return cur[i].Time.Before(cur[j].Time) })
	}
	s.telemetry[vehicleID] = cur
	return nil
}

func (s *Store) QueryTelemetry(_ context.Context, vehicleID string, from, to time.Time, bucket time.Duration, limit int) ([]models.TelemetryPoint, error) {
	s.mu.RLock()
	all := s.telemetry[vehicleID]
	lo := sort.Search(len(all), func(i int) bool { return !all[i].Time.Before(from) })
	hi := sort.Search(len(all), func(i int) bool { return !all[i].Time.Before(to) })
	pts := append([]models.TelemetryPoint(nil), all[lo:hi]...)
	s.mu.RUnlock()
	pts = store.BucketPoints(pts, bucket)
	if limit > 0 && len(pts) > limit {
		pts = pts[:limit]
	}
	return pts, nil
}

// ---------------------------------------------------------------- trips & charges

func inRange(t time.Time, o store.ListOptions) bool {
	if !o.From.IsZero() && t.Before(o.From) {
		return false
	}
	if !o.To.IsZero() && !t.Before(o.To) {
		return false
	}
	if o.Before != nil && !t.Before(*o.Before) {
		return false
	}
	return true
}

func (s *Store) SaveTrip(_ context.Context, t *models.Trip) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vehicles[t.VehicleID]; !ok {
		return store.ErrNotFound
	}
	if t.ID == "" {
		t.ID = ids.New()
	}
	if s.trips[t.VehicleID] == nil {
		s.trips[t.VehicleID] = map[string]models.Trip{}
	}
	s.trips[t.VehicleID][t.ID] = *t
	return nil
}

func (s *Store) ListTrips(_ context.Context, vehicleID string, o store.ListOptions) ([]models.Trip, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.Trip
	for _, t := range s.trips[vehicleID] {
		if inRange(t.StartedAt, o) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if o.Limit > 0 && len(out) > o.Limit {
		out = out[:o.Limit]
	}
	return out, nil
}

func (s *Store) GetTrip(_ context.Context, vehicleID, id string) (*models.Trip, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.trips[vehicleID][id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &t, nil
}

func (s *Store) SaveChargeSession(_ context.Context, c *models.ChargeSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.vehicles[c.VehicleID]; !ok {
		return store.ErrNotFound
	}
	if c.ID == "" {
		c.ID = ids.New()
	}
	if s.charges[c.VehicleID] == nil {
		s.charges[c.VehicleID] = map[string]models.ChargeSession{}
	}
	s.charges[c.VehicleID][c.ID] = *c
	return nil
}

func (s *Store) ListChargeSessions(_ context.Context, vehicleID string, o store.ListOptions) ([]models.ChargeSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.ChargeSession
	for _, c := range s.charges[vehicleID] {
		if inRange(c.StartedAt, o) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if o.Limit > 0 && len(out) > o.Limit {
		out = out[:o.Limit]
	}
	return out, nil
}

func (s *Store) GetChargeSession(_ context.Context, vehicleID, id string) (*models.ChargeSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.charges[vehicleID][id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &c, nil
}

func (s *Store) InsertDrain(_ context.Context, d models.DrainSegment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drain[d.VehicleID] = append(s.drain[d.VehicleID], d)
	return nil
}

func (s *Store) ListDrain(_ context.Context, vehicleID string, from, to time.Time) ([]models.DrainSegment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.DrainSegment
	for _, d := range s.drain[vehicleID] {
		if !d.EndedAt.Before(from) && d.StartedAt.Before(to) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

// ---------------------------------------------------------------- events

func (s *Store) InsertEvent(_ context.Context, e *models.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextEvent++
	e.ID = s.nextEvent
	s.events = append(s.events, *e)
	return nil
}

func (s *Store) ListEvents(_ context.Context, userID, vehicleID string, limit int, beforeID int64) ([]models.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.Event
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if beforeID > 0 && e.ID >= beforeID {
			continue
		}
		if vehicleID != "" && e.VehicleID != vehicleID {
			continue
		}
		if _, ok := s.links[e.VehicleID][userID]; !ok {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *Store) MarkEventDelivered(_ context.Context, id int64, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.events {
		if s.events[i].ID == id {
			s.events[i].DeliveredAt = &at
			return nil
		}
	}
	return store.ErrNotFound
}

// ---------------------------------------------------------------- rules

func (s *Store) GetRules(_ context.Context, userID string) ([]models.Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]models.Rule(nil), s.rules[userID]...), nil
}

func (s *Store) SaveRules(_ context.Context, userID string, rules []models.Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[userID] = append([]models.Rule(nil), rules...)
	return nil
}

// ---------------------------------------------------------------- geofences

func (s *Store) ListGeofences(_ context.Context, userID string) ([]models.Geofence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.Geofence
	for _, g := range s.geofences[userID] {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Store) GetGeofence(_ context.Context, userID, id string) (*models.Geofence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.geofences[userID][id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &g, nil
}

func (s *Store) CreateGeofence(_ context.Context, userID string, g *models.Geofence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g.ID = ids.New()
	if s.geofences[userID] == nil {
		s.geofences[userID] = map[string]models.Geofence{}
	}
	s.geofences[userID][g.ID] = *g
	return nil
}

func (s *Store) UpdateGeofence(_ context.Context, userID string, g *models.Geofence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.geofences[userID][g.ID]; !ok {
		return store.ErrNotFound
	}
	s.geofences[userID][g.ID] = *g
	return nil
}

func (s *Store) DeleteGeofence(_ context.Context, userID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.geofences[userID][id]; !ok {
		return store.ErrNotFound
	}
	delete(s.geofences[userID], id)
	return nil
}

// ---------------------------------------------------------------- push devices

func (s *Store) UpsertPushDevice(_ context.Context, userID string, d *models.PushDevice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A device token belongs to exactly one user.
	for uid, devs := range s.devices {
		if uid != userID {
			delete(devs, d.Token)
		}
	}
	if s.devices[userID] == nil {
		s.devices[userID] = map[string]models.PushDevice{}
	}
	if cur, ok := s.devices[userID][d.Token]; ok {
		d.ID, d.CreatedAt = cur.ID, cur.CreatedAt
	} else {
		d.ID, d.CreatedAt = ids.New(), time.Now().UTC()
	}
	s.devices[userID][d.Token] = *d
	return nil
}

func (s *Store) ListPushDevices(_ context.Context, userID string) ([]models.PushDevice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.PushDevice
	for _, d := range s.devices[userID] {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) DeletePushDevice(_ context.Context, userID, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[userID][token]; !ok {
		return store.ErrNotFound
	}
	delete(s.devices[userID], token)
	return nil
}

func (s *Store) DeletePushToken(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, devs := range s.devices {
		delete(devs, token)
	}
	return nil
}

// ---------------------------------------------------------------- idempotency & audit

func (s *Store) GetIdempotency(_ context.Context, userID, key string) (*store.IdempotencyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.idem[userID+"|"+key]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &r, nil
}

func (s *Store) SaveIdempotency(_ context.Context, r *store.IdempotencyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := r.UserID + "|" + r.Key
	if _, ok := s.idem[k]; ok {
		return store.ErrConflict
	}
	s.idem[k] = *r
	return nil
}

func (s *Store) InsertCommandLog(_ context.Context, l *store.CommandLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.ID == "" {
		l.ID = ids.New()
	}
	s.cmdLog = append(s.cmdLog, *l)
	return nil
}
