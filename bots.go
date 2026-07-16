package main

// Bot lifecycle: add/remove/balance/clear and the per-tick driver.
// AI decision logic (the brain) lives in bots_ai.go.
// Modeled on lab1702/netrek-web server/bots.go.

import (
	"fmt"
	"math"
	"math/rand"
)

var botNames = []string{
	"HAL-9000", "R2-D2", "C-3PO", "Data", "Bishop", "T-800",
	"Johnny-5", "WALL-E", "EVE", "Optimus", "Bender", "K-2SO",
	"BB-8", "IG-88", "HK-47", "GLaDOS", "SHODAN", "Cortana",
	"Friday", "Jarvis", "Vision", "Ultron", "Skynet", "Agent-Smith",
}

type botState struct {
	Cooldown       int     // ticks until next decision
	Role           botRole // persistent free-play assignment
	Target         int     // current combat target id, -1
	TargetLock     int     // ticks remaining on target lock (prevents thrashing)
	TargetValue    float64
	PlanetApproach int // planet we were heading to before a fight, -1
	DefenseTarget  int // planet we are defending, -1
	PrevDamage     int
	HitTimer       int // >0 for a while after taking damage (unseen attackers)
	RespawnDelay   int
	GoalX, GoalY   float64 // patrol destination (0,0 = none)
	VolleyLeft     int     // torps still to fire in the current spread
	VolleyIdx      int
	VolleyDir      float64
}

type botRole int

const (
	botRoleUnset botRole = iota
	botRoleHunter
	botRoleDefender
	botRoleRaider
)

func newBotState() *botState {
	return &botState{Role: botRoleUnset, Target: -1, PlanetApproach: -1, DefenseTarget: -1}
}

func teamIndex(letter string) int {
	for i, l := range teamLetters {
		if l == letter {
			return i
		}
	}
	return -1
}

// selectBotShip mirrors netrek-web selectBotShipType (bot_helpers.go:139):
// balanced composition, no starbases from auto-selection.
func (g *Game) selectBotShip(team int) *ShipStats {
	counts := map[string]int{}
	total := 0
	for _, p := range g.players {
		if p != nil && p.Team == team && p.Status == "alive" {
			counts[p.Ship.Type]++
			total++
		}
	}
	if total == 0 {
		return shipTypes[[]string{"DD", "CA", "BB", "AS"}[rand.Intn(4)]]
	}
	if counts["DD"] < 2 {
		return shipTypes["DD"]
	}
	if counts["CA"] < 2 {
		return shipTypes["CA"]
	}
	if counts["AS"] == 0 && total > 3 {
		return shipTypes["AS"]
	}
	return shipTypes[[]string{"SC", "DD", "CA", "BB", "AS"}[rand.Intn(5)]]
}

// addBot creates a bot on team; returns false if the team/server is full.
// Caller must hold g.mu.
func (g *Game) addBot(team int) bool {
	if team < 0 || team > 3 {
		return false
	}
	count := 0
	for _, p := range g.players {
		if p != nil && p.Team == team {
			count++
		}
	}
	if count >= MaxPerTeam {
		return false
	}
	slot := -1
	for i, q := range g.players {
		if q == nil {
			slot = i
			break
		}
	}
	if slot < 0 {
		return false
	}
	p := &Player{
		ID:   slot,
		Name: fmt.Sprintf("[BOT] %s", botNames[rand.Intn(len(botNames))]),
		Team: team,
		Ship: g.selectBotShip(team),
		Bot:  newBotState(),
	}
	g.players[slot] = p
	g.spawn(p)
	return true
}

func (g *Game) removeBot(team int) {
	for i := len(g.players) - 1; i >= 0; i-- {
		p := g.players[i]
		if p == nil || p.Bot == nil || p.Team != team {
			continue
		}
		g.purgeBotTorps(p)
		g.players[i] = nil
		return
	}
}

// purgeBotTorps removes projectiles by player identity, not by reusable slot
// number. This prevents a later occupant of the same slot inheriting (or losing)
// the departing bot's torpedo count.
func (g *Game) purgeBotTorps(p *Player) {
	for id, t := range g.torps {
		if t.owner == p || t.owner == nil && t.Owner == p.ID {
			delete(g.torps, id)
		}
	}
	p.NTorps = 0
}

func (g *Game) AddBotCmd(teamL string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addBot(teamIndex(teamL))
}

func (g *Game) RemoveBotCmd(teamL string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removeBot(teamIndex(teamL))
}

// BalanceBots tops up the two most-populated teams (default Fed/Rom) to at
// least TournNeeded each, so one click yields a t-mode-ready game.
// ponytail: deviates from netrek-web's equalize-all-four policy on purpose —
// the classic game is two-sided; use +/− for other arrangements.
func (g *Game) BalanceBots() {
	g.mu.Lock()
	defer g.mu.Unlock()
	var counts [4]int
	for _, p := range g.players {
		if p != nil && p.Team != TeamNone {
			counts[p.Team]++
		}
	}
	a, b := 0, 1 // default Fed vs Rom
	for i := 1; i < 4; i++ {
		if counts[i] > counts[a] {
			a, b = i, a
		} else if counts[i] > counts[b] {
			b = i
		}
	}
	target := max(TournNeeded, max(counts[a], counts[b]))
	for _, team := range []int{a, b} {
		for counts[team] < target && g.addBot(team) {
			counts[team]++
		}
	}
	g.say("Bots balanced: %s %d v %s %d", teamNames[a], counts[a], teamNames[b], counts[b])
}

// FillBots packs every team with bots, leaving one open slot per team so a
// human can always join any empire. 4 x 31 = 124, which fits under the
// 128-player cap with exactly the four reserved slots to spare.
func (g *Game) FillBots() {
	g.mu.Lock()
	defer g.mu.Unlock()
	var counts [4]int
	for _, p := range g.players {
		if p != nil && p.Team != TeamNone {
			counts[p.Team]++
		}
	}
	added := 0
	for team := 0; team < 4; team++ {
		for counts[team] < MaxPerTeam-1 && g.addBot(team) {
			counts[team]++
			added++
		}
	}
	if added > 0 {
		g.say("%d bots added — one open slot left per team.", added)
	}
}

func (g *Game) ClearBots() {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for i, p := range g.players {
		if p != nil && p.Bot != nil {
			g.purgeBotTorps(p)
			g.players[i] = nil
			n++
		}
	}
	if n > 0 {
		g.say("%d bots removed.", n)
	}
}

// updateBots runs every tick from Tick() with g.mu held.
func (g *Game) updateBots() {
	g.checkBotScuttle()
	removedScuttler := false
	for _, p := range g.players {
		if p == nil || p.Bot == nil {
			continue
		}
		switch p.Status {
		case "dead":
			if g.botsScuttling { // empty server: free the slot instead
				g.purgeBotTorps(p)
				g.players[p.ID] = nil
				removedScuttler = true
				continue
			}
			p.Bot.RespawnDelay++
			if p.Bot.RespawnDelay > 30 { // ~3s, like a quick human re-outfit
				*p.Bot = *newBotState()
				g.spawn(p)
			}
		case "alive":
			g.updateBot(p)
		}
	}
	if removedScuttler {
		// Player-count transitions normally wait for the 1 Hz tournament check,
		// but an empty-server teardown should not leave a stale tournament round.
		g.checkTmode()
	}
}

// checkBotScuttle: when the last human connection drops, every bot arms its
// self-destruct; a human returning within the fuse cancels the scuttle.
func (g *Game) checkBotScuttle() {
	if g.clientsOnline > 0 {
		if !g.botsScuttling {
			return
		}
		g.botsScuttling = false
		saved := false
		for _, p := range g.players {
			if p != nil && p.Bot != nil {
				p.SelfDest = 0
				saved = true
			}
		}
		if saved {
			g.say("Human back online — bot self destruct canceled.")
		}
		return
	}

	wasScuttling := g.botsScuttling
	anyBots, armed := false, false
	for _, p := range g.players {
		if p == nil || p.Bot == nil {
			continue
		}
		anyBots = true
		if p.Status == "alive" && p.SelfDest == 0 {
			p.SelfDest = g.tick + 100
			armed = true
		}
	}
	if !anyBots {
		g.botsScuttling = false
		return
	}
	g.botsScuttling = true
	if armed && !wasScuttling {
		g.say("No humans left — bots self destructing.")
	}
}

// dist2d is the workhorse of everything below.
func dist2d(x1, y1, x2, y2 float64) float64 {
	return math.Hypot(x2-x1, y2-y1)
}
