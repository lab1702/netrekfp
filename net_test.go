package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNormalizeDirectionAndRound3StayFinite(t *testing.T) {
	d, ok := normalizeDirection(1e308)
	if !ok || math.IsNaN(d) || math.IsInf(d, 0) || math.Abs(d) > math.Pi {
		t.Fatalf("huge finite direction was not normalized: d=%v ok=%v", d, ok)
	}
	if _, ok := normalizeDirection(math.Inf(1)); ok {
		t.Fatal("infinite direction should be rejected")
	}

	got := round3(math.MaxFloat64)
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("round3 overflowed: %v", got)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("rounded finite value should remain JSON-safe: %v", err)
	}
	if got := round3(math.NaN()); got != 0 {
		t.Fatalf("round3 should contain non-finite internal state, got %v", got)
	}
}

func TestWebSocketReservationIncludesPendingUpgrades(t *testing.T) {
	s := &Server{clients: make(map[*Client]bool)}
	for i := 0; i < maxWSClients-1; i++ {
		s.clients[&Client{}] = true
	}
	if !s.reserveClient() {
		t.Fatal("last websocket slot should be reservable")
	}
	if s.reserveClient() {
		t.Fatal("a pending upgrade must count against websocket capacity")
	}
	s.finishReservation(nil)
	if !s.reserveClient() {
		t.Fatal("releasing a failed upgrade should restore capacity")
	}
	s.finishReservation(&Client{})
	if s.reserveClient() {
		t.Fatal("established websocket at capacity should be rejected")
	}
}

func TestBroadcastUsesLatestSnapshotAndReliableEvents(t *testing.T) {
	g := NewGame()
	c := &Client{send: make(chan []byte, 4), snap: make(chan []byte, 1)}
	p, deny := g.Join(c, "pilot", "F", "CA")
	if deny != "" {
		t.Fatalf("join denied: %s", deny)
	}
	ghost := &Client{send: make(chan []byte, 1), snap: make(chan []byte, 1)}
	s := &Server{game: g, clients: map[*Client]bool{c: true, ghost: true}}

	g.msgs = append(g.msgs, "one-shot")
	s.broadcast()
	if g.clientsOnline != 1 {
		t.Fatalf("clientsOnline=%d, want one joined human", g.clientsOnline)
	}
	select {
	case data := <-c.send:
		var events wireEvents
		if err := json.Unmarshal(data, &events); err != nil {
			t.Fatalf("decode events: %v", err)
		}
		if events.T != "events" || len(events.Msgs) != 1 || events.Msgs[0] != "one-shot" {
			t.Fatalf("unexpected reliable event payload: %+v", events)
		}
	default:
		t.Fatal("transient event was not queued reliably")
	}

	// Leave the first snapshot unread. The second broadcast must replace it,
	// not append another stale frame behind it.
	p.X = 4321.5
	s.broadcast()
	if len(c.snap) != 1 {
		t.Fatalf("snapshot mailbox length=%d, want 1", len(c.snap))
	}
	var snap wireSnap
	if err := json.Unmarshal(<-c.snap, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snap.You.X != p.X {
		t.Fatalf("snapshot X=%v, want latest %v", snap.You.X, p.X)
	}
	if len(c.send) != 0 {
		t.Fatal("consumed transient event was sent more than once")
	}
}

func TestReliableQueueOverflowFailsInsteadOfDropping(t *testing.T) {
	c := &Client{send: make(chan []byte, 1)}
	if !c.queueReliable([]byte("first")) {
		t.Fatal("first reliable message should queue")
	}
	if c.queueReliable([]byte("second")) {
		t.Fatal("full reliable queue should fail and disconnect")
	}
}

func TestWebSocketHugeDirectionKeepsSnapshotsAndEventsLive(t *testing.T) {
	g := NewGame()
	s := &Server{game: g, clients: make(map[*Client]bool)}
	httpServer := httptest.NewServer(http.HandlerFunc(s.handleWS))
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	readType := func() (string, []byte) {
		t.Helper()
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket message: %v", err)
		}
		var envelope struct {
			T string `json:"t"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatalf("decode websocket envelope: %v", err)
		}
		return envelope.T, data
	}

	if typ, _ := readType(); typ != "welcome" {
		t.Fatalf("first message=%q, want welcome", typ)
	}
	if err := conn.WriteJSON(inMsg{T: "join", Name: "pilot", Team: "F", Ship: "CA"}); err != nil {
		t.Fatalf("write join: %v", err)
	}
	if typ, _ := readType(); typ != "joined" {
		t.Fatalf("join reply=%q, want joined", typ)
	}

	wantDir, ok := normalizeDirection(1e308)
	if !ok {
		t.Fatal("test direction unexpectedly rejected")
	}
	if err := conn.WriteJSON(inMsg{T: "course", D: 1e308}); err != nil {
		t.Fatalf("write course: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		g.mu.Lock()
		p := g.players[0]
		applied := p != nil && math.Abs(p.DesDir-wantDir) < 1e-12
		g.mu.Unlock()
		if applied {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("huge finite course was not normalized and applied")
		}
		time.Sleep(time.Millisecond)
	}

	g.Tick() // zero-speed turning copies the normalized course into Dir
	g.mu.Lock()
	g.msgs = append(g.msgs, "still live")
	g.mu.Unlock()
	s.broadcast()

	gotSnap, gotEvents := false, false
	for !gotSnap || !gotEvents {
		typ, data := readType()
		switch typ {
		case "snap":
			var snap wireSnap
			if err := json.Unmarshal(data, &snap); err != nil {
				t.Fatalf("decode snapshot: %v", err)
			}
			if math.IsNaN(snap.You.D) || math.IsInf(snap.You.D, 0) || math.Abs(snap.You.D) > math.Pi {
				t.Fatalf("snapshot contains invalid normalized direction: %v", snap.You.D)
			}
			gotSnap = true
		case "events":
			var events wireEvents
			if err := json.Unmarshal(data, &events); err != nil {
				t.Fatalf("decode events: %v", err)
			}
			if len(events.Msgs) != 1 || events.Msgs[0] != "still live" {
				t.Fatalf("unexpected event payload: %+v", events)
			}
			gotEvents = true
		default:
			t.Fatalf("unexpected websocket message type %q", typ)
		}
	}
}
