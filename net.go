package main

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

type Server struct {
	game    *Game
	mu      sync.Mutex
	clients map[*Client]bool
}

type Client struct {
	srv    *Server
	conn   *websocket.Conn
	send   chan []byte
	player *Player
}

var upgrader = websocket.Upgrader{
	ReadBufferSize: 1024, WriteBufferSize: 16384,
	// permessage-deflate when the browser offers it: the 10 Hz JSON snapshots
	// are highly repetitive and compress ~5-10x
	EnableCompression: true,
	// ponytail: same-origin game served by this binary; tighten if ever exposed
	CheckOrigin: func(r *http.Request) bool { return true },
}

type inMsg struct {
	T    string  `json:"t"`
	Name string  `json:"name"`
	Team string  `json:"team"`
	Ship string  `json:"ship"`
	D    float64 `json:"d"`
	V    int     `json:"v"`
	To   string  `json:"to"`
	Text string  `json:"text"`
}

// printable ASCII only, capped — same rule as player names
func sanitizeText(s string, maxLen int) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return -1
		}
		return r
	}, s)
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return strings.TrimSpace(s)
}

// ---- wire formats (field names are what the client reads) ----

type wirePlanetFull struct {
	N    int     `json:"n"`
	Name string  `json:"name"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	O    string  `json:"o"`
	A    int     `json:"a"`
	F    int     `json:"f"`
}

type wirePlanet struct {
	N int    `json:"n"`
	O string `json:"o"`
	A int    `json:"a"`
	F int    `json:"f"`
}

type wirePlayer struct {
	I  int     `json:"i"`
	Nm string  `json:"nm"`
	Tm string  `json:"tm"`
	S  string  `json:"s"`
	X  int     `json:"x"`
	Y  int     `json:"y"`
	D  float64 `json:"d"`
	Ki float64 `json:"ki"`
	St string  `json:"st"`
	Cl bool    `json:"cl"`
}

type wireTorp struct {
	I  int    `json:"i"`
	X  int    `json:"x"`
	Y  int    `json:"y"`
	Tm string `json:"tm"`
}

type wireYou struct {
	I     int     `json:"i"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	D     float64 `json:"d"`
	Sp    int     `json:"sp"`
	MaxSp int     `json:"maxsp"`
	Sh    int     `json:"sh"`
	MaxSh int     `json:"maxsh"`
	Dm    int     `json:"dm"`
	MaxDm int     `json:"maxdm"`
	Fu    int     `json:"fu"`
	MaxFu int     `json:"maxfu"`
	Wt    int     `json:"wt"`
	MaxWt int     `json:"maxwt"`
	Et    int     `json:"et"`
	MaxEt int     `json:"maxet"`
	Tp    int     `json:"tp"`
	Ar    int     `json:"ar"`
	Ki    float64 `json:"ki"`
	Orb   int     `json:"orb"`
	ShUp  bool    `json:"shup"`
	Cl    bool    `json:"cl"`
	Rep   bool    `json:"rep"`
	Bmb   bool    `json:"bmb"`
	Sd    int     `json:"sd"` // self-destruct countdown, seconds; 0 = disarmed
	Lk    int     `json:"lk"` // locked planet index, -1 off
	St    string  `json:"st"`
	Tm    string  `json:"tm"`
}

type wireTmode struct {
	On   bool `json:"on"`
	Left int  `json:"left"`
}

type wireSnap struct {
	T       string         `json:"t"`
	You     wireYou        `json:"you"`
	Players []wirePlayer   `json:"players"`
	Torps   []wireTorp     `json:"torps"`
	Phasers []PhaserFx     `json:"phasers"`
	Planets []wirePlanet   `json:"planets"`
	Tmode   wireTmode      `json:"tmode"`
	Msgs    []string       `json:"msgs"`
	Booms   []Boom         `json:"booms"`
	Chats   []Chat         `json:"chats"`
	Counts  map[string]int `json:"counts"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &Client{srv: s, conn: conn, send: make(chan []byte, 32)}
	s.mu.Lock()
	s.clients[c] = true
	s.mu.Unlock()

	s.game.mu.Lock()
	welcome, _ := json.Marshal(map[string]any{
		"t": "welcome", "counts": s.game.teamCounts(), "planets": s.game.wirePlanetsFull(),
	})
	s.game.mu.Unlock()
	c.send <- welcome

	go c.writePump()
	c.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.srv.game.Leave(c)
		// close(send) must be mutually exclusive with broadcast's sends (both
		// under s.mu), or a disconnect mid-fanout panics the tick goroutine
		c.srv.mu.Lock()
		delete(c.srv.clients, c)
		close(c.send)
		c.srv.mu.Unlock()
	}()
	c.conn.SetReadLimit(512)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var m inMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if math.IsNaN(m.D) || math.IsInf(m.D, 0) {
			continue
		}
		if m.T == "chat" {
			if c.player != nil {
				if text := sanitizeText(m.Text, 120); text != "" {
					c.srv.game.Chat(c.player, m.To, text)
				}
			}
			continue
		}
		if m.T == "join" {
			name := sanitizeText(m.Name, 15)
			if name == "" {
				name = "guest"
			}
			p, deny := c.srv.game.Join(c, name, m.Team, m.Ship)
			var resp []byte
			if deny != "" {
				resp, _ = json.Marshal(map[string]any{"t": "deny", "reason": deny})
			} else {
				resp, _ = json.Marshal(map[string]any{"t": "joined", "id": p.ID})
			}
			// blocking send: this reply is required protocol, not a droppable
			// snapshot; writePump drains the channel even after a write error
			c.send <- resp
			continue
		}
		switch m.T {
		case "addbot":
			c.srv.game.AddBotCmd(m.Team)
			continue
		case "removebot":
			c.srv.game.RemoveBotCmd(m.Team)
			continue
		case "balancebots":
			c.srv.game.BalanceBots()
			continue
		case "fillbots":
			c.srv.game.FillBots()
			continue
		case "clearbots":
			c.srv.game.ClearBots()
			continue
		}
		if c.player != nil {
			if m.V < 0 {
				m.V = 0
			}
			c.srv.game.Command(c.player, m.T, m.D, m.V)
		}
	}
}

func (c *Client) writePump() {
	for data := range c.send {
		if c.conn.WriteMessage(websocket.TextMessage, data) != nil {
			c.conn.Close()
			// drain until readPump closes the channel
			for range c.send {
			}
			return
		}
	}
	c.conn.Close()
}

func (g *Game) wirePlanetsFull() []wirePlanetFull {
	out := make([]wirePlanetFull, len(g.planets))
	for i, pl := range g.planets {
		out[i] = wirePlanetFull{pl.N, pl.Name, pl.X, pl.Y, teamLetter(pl.Owner), pl.Armies, pl.Flags}
	}
	return out
}

// broadcast builds one snapshot per joined client (players filtered for cloak).
// ponytail: full state every tick, ~6 KB/client; delta encoding is the upgrade path.
func (s *Server) broadcast() {
	g := s.game
	g.mu.Lock()

	players := make([]wirePlayer, 0, 32)
	for _, p := range g.players {
		if p == nil || p.Team == TeamNone {
			continue // dead slots stay listed (player roster); quit slots don't
		}
		players = append(players, wirePlayer{p.ID, p.Name, teamLetter(p.Team), p.Ship.Type,
			int(p.X), int(p.Y), round3(p.Dir), math.Round(p.Kills*100) / 100,
			p.Status, p.Cloaked})
	}
	torps := make([]wireTorp, 0, len(g.torps))
	for _, t := range g.torps {
		torps = append(torps, wireTorp{t.ID, int(t.X), int(t.Y), teamLetter(t.Team)})
	}
	planets := make([]wirePlanet, len(g.planets))
	for i, pl := range g.planets {
		planets[i] = wirePlanet{pl.N, teamLetter(pl.Owner), pl.Armies, pl.Flags}
	}
	snap := wireSnap{
		T: "snap", Players: players, Torps: torps, Phasers: append([]PhaserFx{}, g.phasers...),
		Planets: planets, Tmode: wireTmode{g.tmode, g.tmodeLeft / 10},
		Msgs: append([]string{}, g.msgs...), Booms: append([]Boom{}, g.booms...),
		Counts: g.teamCounts(),
	}
	chats := append([]Chat{}, g.chats...)

	// consumed: anything Command() appends between broadcasts ships exactly once
	g.msgs = g.msgs[:0]
	g.booms = g.booms[:0]
	g.phasers = g.phasers[:0]
	g.chats = g.chats[:0]

	// sends happen under s.mu so a disconnecting client can't close its channel
	// mid-fanout; the sends are non-blocking so holding the lock is safe
	s.mu.Lock()
	g.clientsOnline = len(s.clients)
	for c := range s.clients {
		p := c.player
		if p == nil {
			continue
		}
		mine := snap
		mine.You = wireYou{
			I: p.ID, X: p.X, Y: p.Y, D: round3(p.Dir), Sp: p.Speed, MaxSp: p.Ship.MaxSpeed,
			Sh: p.Shield, MaxSh: p.Ship.MaxShield, Dm: p.Damage, MaxDm: p.Ship.MaxDamage,
			Fu: p.Fuel, MaxFu: p.Ship.MaxFuel, Wt: p.WTemp, MaxWt: p.Ship.MaxWpnTemp,
			Et: p.ETemp, MaxEt: p.Ship.MaxEgnTemp, Tp: p.NTorps, Ar: p.Armies,
			Ki: p.Kills, Orb: p.Orbiting, ShUp: p.ShieldsUp, Cl: p.Cloaked,
			Rep: p.RepairMode, Bmb: p.Bombing, Lk: p.LockPlanet,
			St: p.Status, Tm: teamLetter(p.Team),
		}
		if p.SelfDest != 0 {
			mine.You.Sd = int(p.SelfDest-g.tick+9) / 10
		}
		vis := make([]wirePlayer, 0, len(players))
		for _, wp := range players {
			if wp.Cl && wp.Tm != teamLetter(p.Team) {
				continue // cloaked ships are invisible to enemies
			}
			vis = append(vis, wp)
		}
		mine.Players = vis
		mine.Chats = nil
		for _, ch := range chats {
			if chatVisible(ch, p.Team) {
				mine.Chats = append(mine.Chats, ch)
			}
		}
		data, err := json.Marshal(&mine)
		if err != nil {
			continue
		}
		select {
		case c.send <- data:
		default: // slow consumer: skip this frame rather than block the loop
		}
	}
	s.mu.Unlock()
	g.mu.Unlock()
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

func init() { log.SetFlags(log.Ltime) }
