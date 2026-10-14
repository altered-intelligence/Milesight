package realtime

import (
	"testing"
	"time"
)

func TestHubRoutesAndDropsSlowClients(t *testing.T) {
	h := NewHub()
	a := h.Subscribe("v1")
	b := h.Subscribe("v2")
	h.Publish(Message{Type: TypeState, VehicleID: "v1", Data: 1})
	select {
	case m := <-a.C():
		if m.Data != 1 || m.At.IsZero() {
			t.Fatalf("bad message %+v", m)
		}
	default:
		t.Fatal("subscriber of v1 got nothing")
	}
	select {
	case <-b.C():
		t.Fatal("v2 subscriber received v1 message")
	default:
	}
	// Fill a's buffer: it must be disconnected (channel closed), not block Publish.
	for i := 0; i < 100; i++ {
		h.Publish(Message{Type: TypeState, VehicleID: "v1", Data: i})
	}
	drained := 0
	for range a.C() {
		drained++
	}
	if drained != 64 || h.Subscribers("v1") != 0 {
		t.Fatalf("slow client: drained=%d subs=%d", drained, h.Subscribers("v1"))
	}
	h.Unsubscribe(a) // idempotent
	h.Unsubscribe(b)
	if h.Subscribers("v2") != 0 {
		t.Fatal("unsubscribe failed")
	}
}

func TestWatching(t *testing.T) {
	h := NewHub()
	now := time.Now()
	h.now = func() time.Time { return now }
	if h.Watching("v", time.Minute) {
		t.Fatal("nobody is watching")
	}
	h.MarkViewed("v")
	if !h.Watching("v", time.Minute) {
		t.Fatal("recent view should count")
	}
	now = now.Add(2 * time.Minute)
	if h.Watching("v", time.Minute) {
		t.Fatal("stale view should not count")
	}
	c := h.Subscribe("v")
	if !h.Watching("v", time.Minute) {
		t.Fatal("subscriber should count")
	}
	h.Unsubscribe(c)
}
