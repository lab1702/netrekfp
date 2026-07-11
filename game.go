package main

// Game core. Formulas and constants follow netrek-server (Bronco) ntserv/daemon.c
// at the 10 Hz "major update" granularity; per-frame smoothing is the client's job.

import (
	"fmt"
	"math"
	"math/rand"
	"sync"
)

const (
	GWidth     = 100000
	Warp1      = 20   // units per warp per tick
	ExpDist    = 350  // torp proximity trigger
	DamDist    = 2000 // torp damage reaches this far
	ShipDamAge = 3000 // SHIPDAMDIST: ship explosion damage radius
	DetDist    = 1700
	PhaseDist  = 6000
	EntOrbDist = 900
	OrbDist    = 800
	OrbSpeed   = 2
	PFireDist  = 1500
	ZapPlayer  = 390 // phaser beam half-width
	MaxTorps   = 8

	MaxPlayers  = 128
	MaxPerTeam  = 32
	TournNeeded = 4                 // players per team for T-mode (user spec; Vanilla default is 5)
	TournTicks  = 30 * 60 * 10      // 30 minutes at 10 Hz
	ByteRad     = math.Pi * 2 / 256 // one netrek direction unit
)

type Torp struct {
	ID     int
	Owner  int
	Team   int
	X, Y   float64
	Dir    float64
	Speed  int // warp
	Fuse   int // ticks
	Damage int
	Detter int // player who detted it, -1
}

type PhaserFx struct {
	FX float64 `json:"fx"`
	FY float64 `json:"fy"`
	TX float64 `json:"tx"`
	TY float64 `json:"ty"`
	Tm string  `json:"tm"`
}

type Boom struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Big bool    `json:"big"`
}

type Player struct {
	ID     int
	Name   string
	Team   int // TeamNone when slot not on a team (outfit/quit)
	Ship   *ShipStats
	Status string // "outfit", "alive", "explode", "dead"

	X, Y     float64
	Dir      float64 // radians; velocity = (cos, sin)
	DesDir   float64
	SubDir   int
	Speed    int
	DesSpeed int
	SubSpeed int

	Shield              int
	Damage              int
	SubShield, SubDamage int
	Fuel                int
	WTemp, ETemp        int
	WLock, ELock        bool // overheat lockouts (PFWEP / PFENG)
	WTime, ETime        int

	ShieldsUp  bool
	Cloaked    bool
	RepairMode bool
	Bombing    bool
	Beaming    int // 0 none, 1 up, 2 down
	Orbiting   int // planet index or -1

	Armies       int
	Kills        float64
	NTorps       int
	PhaserBusy   int // ticks until phaser ready
	ExplodeTicks int
	LastTorpTick int64
	WhoDead      int // killer id for explosion chain credit (daemon.c blowup), -1

	Bot *botState // non-nil for AI players

	client *Client // nil for test players
}

type Game struct {
	mu      sync.Mutex
	players [MaxPlayers]*Player
	torps   map[int]*Torp
	planets []*Planet
	tick    int64
	torpSeq int

	tmode     bool
	tmodeLeft int

	popOrder []int
	popIdx   int

	// per-tick transient output
	msgs    []string
	booms   []Boom
	phasers []PhaserFx
}

func NewGame() *Game {
	g := &Game{torps: map[int]*Torp{}, planets: resetPlanets()}
	g.popOrder = rand.Perm(40)
	return g
}

func (g *Game) say(format string, a ...any) {
	g.msgs = append(g.msgs, fmt.Sprintf(format, a...))
}

func teamLetter(t int) string {
	if t >= 0 && t < 4 {
		return teamLetters[t]
	}
	return "I"
}

// ---------- join / leave ----------

func (g *Game) teamCounts() map[string]int {
	c := map[string]int{}
	for _, p := range g.players {
		if p != nil && p.Team != TeamNone {
			c[teamLetter(p.Team)]++
		}
	}
	return c
}

func (g *Game) Join(cl *Client, name string, teamL, shipT string) (*Player, string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	team := -1
	for i, l := range teamLetters {
		if l == teamL {
			team = i
		}
	}
	stats, okShip := shipTypes[shipT]
	if team < 0 || !okShip {
		return nil, "bad team or ship"
	}
	if cl.player != nil && cl.player.Status != "dead" {
		return nil, "you are still alive"
	}
	counts := 0
	sbTaken := false
	for _, p := range g.players {
		if p != nil && p.Team == team {
			counts++
			if p.Ship.Type == "SB" && p.Status != "dead" && p != cl.player {
				sbTaken = true
			}
		}
	}
	if cl.player == nil || cl.player.Team != team {
		if counts >= MaxPerTeam {
			return nil, "team is full (32)"
		}
	}
	if shipT == "SB" && sbTaken {
		return nil, "your team already has a starbase"
	}

	p := cl.player
	if p == nil {
		slot := -1
		for i, q := range g.players {
			if q == nil {
				slot = i
				break
			}
		}
		if slot < 0 {
			return nil, "server full (128)"
		}
		p = &Player{ID: slot, client: cl}
		g.players[slot] = p
		cl.player = p
	}
	p.Name = name
	p.Team = team
	p.Ship = stats
	g.spawn(p)
	return p, ""
}

// spawn: enter.c — near a random team-owned planet, full health, shields up
func (g *Game) spawn(p *Player) {
	var home []*Planet
	for _, pl := range g.planets {
		if pl.Owner == p.Team {
			home = append(home, pl)
		}
	}
	var at *Planet
	if len(home) > 0 {
		at = home[rand.Intn(len(home))]
	} else {
		at = g.planets[13] // Regulus, the Vanilla fallback
	}
	p.X = at.X + float64(rand.Intn(10000)-5000)
	p.Y = at.Y + float64(rand.Intn(10000)-5000)
	p.X = math.Max(0, math.Min(GWidth, p.X))
	p.Y = math.Max(0, math.Min(GWidth, p.Y))
	center := math.Atan2(GWidth/2-p.Y, GWidth/2-p.X)
	p.Dir, p.DesDir, p.SubDir = center, center, 0
	p.Speed, p.DesSpeed, p.SubSpeed = 0, 0, 0
	p.Shield, p.Damage, p.SubShield, p.SubDamage = p.Ship.MaxShield, 0, 0, 0
	p.Fuel = p.Ship.MaxFuel
	p.WTemp, p.ETemp, p.WLock, p.ELock = 0, 0, false, false
	p.ShieldsUp = true
	p.Cloaked, p.RepairMode, p.Bombing = false, false, false
	p.Beaming = 0
	p.Orbiting = -1
	p.Armies = 0
	p.Kills = 0
	p.NTorps = 0
	for _, t := range g.torps { // surviving torps still count against the 8-tube limit
		if t.Owner == p.ID {
			p.NTorps++
		}
	}
	p.PhaserBusy = 0
	p.ExplodeTicks = 0
	p.LastTorpTick = -1
	p.WhoDead = -1
	p.Status = "alive"
}

func (g *Game) Leave(cl *Client) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p := cl.player
	if p == nil {
		return
	}
	if p.Status == "explode" && p.ExplodeTicks > 7 {
		g.blowup(p) // don't let disconnecting cancel the pending splash
	}
	for id, t := range g.torps { // the slot's next occupant must not inherit these
		if t.Owner == p.ID {
			delete(g.torps, id)
		}
	}
	g.players[p.ID] = nil
	cl.player = nil
}

// ---------- commands (called with lock held via Command) ----------

func (g *Game) Command(p *Player, cmd string, dir float64, val int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if p.Status != "alive" {
		if cmd == "quit" {
			p.Team = TeamNone
		}
		return
	}
	switch cmd {
	case "course":
		p.DesDir = dir
		p.RepairMode = false
		g.breakOrbit(p)
	case "speed":
		p.DesSpeed = min(val, p.Ship.MaxSpeed)
		p.RepairMode = false
		g.breakOrbit(p)
	case "shields":
		p.ShieldsUp = !p.ShieldsUp
		p.RepairMode = false
	case "torp":
		g.fireTorp(p, dir)
	case "phaser":
		g.firePhaser(p, dir)
	case "orbit":
		g.enterOrbit(p)
	case "bomb":
		g.startBomb(p)
	case "beamup":
		g.startBeam(p, 1)
	case "beamdown":
		g.startBeam(p, 2)
	case "repair":
		p.RepairMode = !p.RepairMode
		if p.RepairMode {
			p.DesSpeed = 0
			p.ShieldsUp = false
			p.Bombing = false
			p.Beaming = 0
		}
	case "cloak":
		if !p.Cloaked && p.Fuel < p.Ship.CloakCost {
			return
		}
		p.Cloaked = !p.Cloaked
	case "det":
		g.detEnemyTorps(p)
	case "quit":
		p.Status = "dead"
		p.Team = TeamNone
	}
}

func (g *Game) breakOrbit(p *Player) {
	if p.Orbiting >= 0 {
		p.Orbiting = -1
		p.Bombing = false
		p.Beaming = 0
	}
}

func (g *Game) fireTorp(p *Player, dir float64) {
	s := p.Ship
	if p.WLock || p.Cloaked || p.RepairMode || p.NTorps >= MaxTorps ||
		p.Fuel < s.TorpCost || p.LastTorpTick == g.tick {
		return
	}
	p.LastTorpTick = g.tick
	p.NTorps++
	p.Fuel -= s.TorpCost
	p.WTemp += s.TorpCost/10 - 10
	g.torpSeq++
	g.torps[g.torpSeq] = &Torp{
		ID: g.torpSeq, Owner: p.ID, Team: p.Team, X: p.X, Y: p.Y, Dir: dir,
		Speed: s.TorpSpeed, Fuse: s.TorpFuse + rand.Intn(20), Damage: s.TorpDamage,
		Detter: -1,
	}
}

func (g *Game) firePhaser(p *Player, dir float64) {
	s := p.Ship
	if p.WLock || p.Cloaked || p.RepairMode || p.PhaserBusy > 0 || p.Fuel < s.PhaserCost {
		return
	}
	p.Fuel -= s.PhaserCost
	p.WTemp += s.PhaserCost / 10
	p.PhaserBusy = s.PhaserFuse

	rangeMax := float64(PhaseDist * s.PhaserDamage / 100)
	dx, dy := math.Cos(dir), math.Sin(dir)
	var hit *Player
	best := rangeMax + 1
	for _, t := range g.players {
		if t == nil || t == p || t.Status != "alive" || t.Team == p.Team {
			continue
		}
		ax, ay := t.X-p.X, t.Y-p.Y
		along := ax*dx + ay*dy // distance along the beam, clamped >= 0
		if along < 0 {
			along = 0
		}
		perp := math.Hypot(ax-along*dx, ay-along*dy)
		dist := math.Hypot(ax, ay)
		if perp <= ZapPlayer && dist <= rangeMax && dist < best {
			best, hit = dist, t
		}
	}
	if hit != nil {
		dmg := int(float64(s.PhaserDamage) * (1.0 - best/rangeMax))
		g.hurt(hit, dmg, p.ID, "phaser")
		g.phasers = append(g.phasers, PhaserFx{p.X, p.Y, hit.X, hit.Y, teamLetter(p.Team)})
	} else {
		g.phasers = append(g.phasers,
			PhaserFx{p.X, p.Y, p.X + dx*rangeMax, p.Y + dy*rangeMax, teamLetter(p.Team)})
	}
}

func (g *Game) detEnemyTorps(p *Player) {
	s := p.Ship
	if p.WLock || p.Fuel < s.DetCost {
		return
	}
	p.Fuel -= s.DetCost
	p.WTemp += s.DetCost / 5
	for _, t := range g.torps {
		if t.Team != p.Team && math.Hypot(t.X-p.X, t.Y-p.Y) <= DetDist {
			t.Detter = p.ID
			g.explodeTorp(t)
		}
	}
}

func (g *Game) enterOrbit(p *Player) {
	if p.Speed > OrbSpeed {
		g.say("%s: too fast to orbit (max warp 2)", p.Name)
		return
	}
	for _, pl := range g.planets {
		if math.Hypot(pl.X-p.X, pl.Y-p.Y) <= EntOrbDist {
			ang := math.Atan2(p.Y-pl.Y, p.X-pl.X)
			p.X = pl.X + OrbDist*math.Cos(ang)
			p.Y = pl.Y + OrbDist*math.Sin(ang)
			p.Dir, p.DesDir = ang+math.Pi/2, ang+math.Pi/2
			p.Speed, p.DesSpeed = 0, 0
			p.Orbiting = pl.N
			return
		}
	}
}

// teamHasPlayers: does anyone (human or bot) fly for this team?
func (g *Game) teamHasPlayers(team int) bool {
	for _, p := range g.players {
		if p != nil && p.Team == team {
			return true
		}
	}
	return false
}

// thirdSpace: t-mode forbids bombing planets of teams nobody flies for
// (Vanilla bomb_planet's "We're at peace with the ..." rule)
func (g *Game) thirdSpace(pl *Planet) bool {
	return g.tmode && pl.Owner != TeamNone && !g.teamHasPlayers(pl.Owner)
}

func (g *Game) startBomb(p *Player) {
	if p.Orbiting < 0 {
		return
	}
	pl := g.planets[p.Orbiting]
	if pl.Owner == p.Team {
		return
	}
	if g.thirdSpace(pl) {
		g.say("%s: we are at peace with the %ss (no 3rd-space bombing in t-mode)",
			p.Name, teamNames[pl.Owner])
		return
	}
	p.Bombing = !p.Bombing
	p.Beaming = 0
	p.RepairMode = false
}

func (g *Game) startBeam(p *Player, dir int) {
	if p.Orbiting < 0 {
		return
	}
	if p.Beaming == dir {
		p.Beaming = 0
	} else {
		p.Beaming = dir
	}
	p.Bombing = false
	p.RepairMode = false
}

// ---------- damage ----------

func (g *Game) hurt(v *Player, dmg int, killer int, why string) {
	if dmg <= 0 || v.Status != "alive" {
		return
	}
	if v.ShieldsUp {
		v.Shield -= dmg
		if v.Shield < 0 {
			v.Damage -= v.Shield
			v.Shield = 0
		}
	} else {
		v.Damage += dmg
	}
	if v.Damage >= v.Ship.MaxDamage {
		g.kill(v, killer, why)
	}
}

func (g *Game) kill(v *Player, killer int, why string) {
	v.Status = "explode"
	v.ExplodeTicks = 10
	v.WhoDead = killer
	v.Speed, v.DesSpeed = 0, 0
	v.Orbiting = -1
	v.Bombing, v.RepairMode, v.Cloaked = false, false, false
	v.Beaming = 0
	if killer >= 0 && g.players[killer] != nil {
		k := g.players[killer]
		if k.Team != v.Team {
			k.Kills += 1.0 + float64(v.Armies)*0.1 + v.Kills*0.1
			g.say("%s (%s) was kill %.2f for %s (%s) [%s]",
				v.Name, teamLetter(v.Team), k.Kills, k.Name, teamLetter(k.Team), why)
			return
		}
	}
	g.say("%s (%s) was destroyed [%s]", v.Name, teamLetter(v.Team), why)
}

// ---------- tick ----------

func (g *Game) Tick() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tick++
	// transient msgs/booms/phasers are cleared by broadcast() after sending, so
	// events appended by Command() between ticks aren't lost

	g.updateBots()

	for _, p := range g.players {
		if p == nil {
			continue
		}
		switch p.Status {
		case "alive":
			g.movePlayer(p)
			g.housekeepPlayer(p)
		case "explode":
			p.ExplodeTicks--
			if p.ExplodeTicks == 7 {
				g.blowup(p)
			}
			if p.ExplodeTicks <= 0 {
				p.Status = "dead"
			}
		}
	}

	g.moveTorps()

	if g.tick%5 == 0 {
		g.planetFight()
	}
	if g.tick%8 == 0 {
		g.beam()
	}
	if g.tick%10 == 0 {
		g.popPlanet()
		g.checkTmode()
	}
	if g.tmode {
		g.tmodeLeft--
		if g.tmodeLeft <= 0 {
			g.endTmode("time up — 30 minutes played")
		}
	}
}

func (g *Game) movePlayer(p *Player) {
	s := p.Ship

	// desired-speed clamps: damage cripple, engine lockout, fuel starvation (daemon.c:1185-1208)
	// crippled max: C truncates the float expression, i.e. ceiling of the subtrahend
	maxSpd := (s.MaxSpeed + 2) - ((s.MaxSpeed+1)*p.Damage+s.MaxDamage-1)/s.MaxDamage
	if p.DesSpeed > maxSpd {
		p.DesSpeed = maxSpd
	}
	if p.ELock && p.DesSpeed > 1 {
		p.DesSpeed = 1
	}
	starving := p.Fuel < s.WarpCost*p.Speed
	if starving { // settle at a cruise speed the recharge rate can sustain
		if s.Recharge/s.WarpCost < p.Speed {
			p.DesSpeed = s.Recharge/s.WarpCost + 2
			if p.DesSpeed > s.MaxSpeed {
				p.DesSpeed = s.MaxSpeed - 1
			}
		} else {
			p.DesSpeed = p.Speed
		}
		p.Fuel = 0
	}

	// acceleration (daemon.c:1225-1238)
	if p.DesSpeed > p.Speed {
		p.SubSpeed += s.AccInt
	}
	if p.DesSpeed < p.Speed {
		p.SubSpeed -= s.DecInt
	}
	if d := p.SubSpeed / 1000; d != 0 {
		p.Speed += d
		p.SubSpeed %= 1000
		p.Speed = max(0, min(p.Speed, s.MaxSpeed))
	}

	if !starving {
		p.Fuel -= s.WarpCost * p.Speed
		p.ETemp += p.Speed
	}

	if p.Orbiting >= 0 {
		// orbital motion: +2 direction units per update at radius 800 (daemon.c:1132)
		pl := g.planets[p.Orbiting]
		ang := math.Atan2(p.Y-pl.Y, p.X-pl.X) + 2*ByteRad
		p.X = pl.X + OrbDist*math.Cos(ang)
		p.Y = pl.Y + OrbDist*math.Sin(ang)
		p.Dir, p.DesDir = ang+math.Pi/2, ang+math.Pi/2
		return
	}

	// turning (changedir, daemon.c:1847; newturn=0 variant)
	if p.Dir != p.DesDir {
		if p.Speed == 0 {
			p.Dir, p.SubDir = p.DesDir, 0
		} else {
			shift := p.Speed
			if shift > 30 {
				shift = 30
			}
			p.SubDir += s.Turns / (1 << shift)
			ticks := p.SubDir / 1000
			if ticks > 0 {
				p.SubDir %= 1000
				diff := math.Remainder(p.DesDir-p.Dir, 2*math.Pi)
				step := float64(ticks) * ByteRad
				if math.Abs(diff) <= step {
					p.Dir = p.DesDir
				} else if diff > 0 {
					p.Dir += step
				} else {
					p.Dir -= step
				}
			}
		}
	}

	p.X += float64(p.Speed*Warp1) * math.Cos(p.Dir)
	p.Y += float64(p.Speed*Warp1) * math.Sin(p.Dir)

	// galaxy edge: bounce (daemon.c:1253)
	if p.X < 0 || p.X > GWidth {
		p.X = math.Abs(p.X)
		if p.X > GWidth {
			p.X = 2*GWidth - p.X
		}
		p.Dir = math.Pi - p.Dir
		p.DesDir = math.Pi - p.DesDir
	}
	if p.Y < 0 || p.Y > GWidth {
		p.Y = math.Abs(p.Y)
		if p.Y > GWidth {
			p.Y = 2*GWidth - p.Y
		}
		p.Dir = -p.Dir
		p.DesDir = -p.DesDir
	}
}

func (g *Game) housekeepPlayer(p *Player) {
	s := p.Ship

	// shield upkeep (daemon.c:1448; GA pays nothing — no SGALAXY case in the C switch)
	if p.ShieldsUp {
		switch s.Type {
		case "SC":
			p.Fuel -= 2
		case "DD", "CA", "BB", "AS":
			p.Fuel -= 3
		case "SB":
			p.Fuel -= 6
		}
	}

	// weapon cooling (daemon.c:1520)
	p.WTemp = max(0, p.WTemp-s.WpnCool)
	if p.WLock {
		p.WTime--
		if p.WTime <= 0 {
			p.WLock = false
		}
	} else if p.WTemp > s.MaxWpnTemp && rand.Intn(40) == 0 {
		p.WLock = true
		p.WTime = rand.Intn(150) + 100
		g.say("%s: weapons overheated", p.Name)
	}

	// engine cooling (daemon.c:1540)
	p.ETemp = max(0, p.ETemp-s.EgnCool)
	if p.ELock {
		p.ETime--
		if p.ETime <= 0 {
			p.ELock = false
		}
	} else if p.ETemp > s.MaxEgnTemp && rand.Intn(40) == 0 {
		p.ELock = true
		p.ETime = rand.Intn(150) + 100
		p.DesSpeed = 0
		g.say("%s: engines overheated", p.Name)
	}

	// cloak (daemon.c:1560)
	if p.Cloaked {
		if p.Fuel < s.CloakCost {
			p.Cloaked = false
		} else {
			p.Fuel -= s.CloakCost
		}
	}

	// fuel recharge (daemon.c:1590)
	orbitingOwn := func(flag int) bool {
		return p.Orbiting >= 0 && g.planets[p.Orbiting].Owner == p.Team &&
			g.planets[p.Orbiting].Flags&flag != 0
	}
	if orbitingOwn(PlFuel) {
		p.Fuel += 8 * s.Recharge
	} else {
		p.Fuel += 2 * s.Recharge
	}
	if p.Fuel > s.MaxFuel {
		p.Fuel = s.MaxFuel
	}
	if p.Fuel < 0 {
		p.Fuel = 0
		p.DesSpeed = 0
		p.Cloaked = false
	}

	// repair (daemon.c:1612); 1000 subunits = 1 point; repair-mode rate replaces
	// the base rate rather than stacking on it
	if p.Shield < s.MaxShield {
		if p.RepairMode && p.Speed == 0 {
			p.SubShield += s.Repair * 4
			if orbitingOwn(PlRepair) {
				p.SubShield += s.Repair * 4
			}
		} else {
			p.SubShield += s.Repair * 2
		}
		p.Shield = min(s.MaxShield, p.Shield+p.SubShield/1000)
		p.SubShield %= 1000
	}
	if p.Damage > 0 && !p.ShieldsUp {
		if p.RepairMode && p.Speed == 0 {
			p.SubDamage += s.Repair * 2
			if orbitingOwn(PlRepair) {
				p.SubDamage += s.Repair * 2
			}
		} else {
			p.SubDamage += s.Repair
		}
		p.Damage = max(0, p.Damage-p.SubDamage/1000)
		p.SubDamage %= 1000
	}

	if p.PhaserBusy > 0 {
		p.PhaserBusy--
	}
}

// ---------- torps ----------

func (g *Game) moveTorps() {
	for _, t := range g.torps {
		t.Dir += float64(rand.Intn(3)-1) * ByteRad // TWOBBLE
		t.X += float64(t.Speed*Warp1) * math.Cos(t.Dir)
		t.Y += float64(t.Speed*Warp1) * math.Sin(t.Dir)
		t.Fuse--
		if t.Fuse <= 0 { // expired torps fizzle harmlessly (daemon.c udtorps)
			g.freeTorp(t)
			continue
		}
		if t.X < 0 || t.X > GWidth || t.Y < 0 || t.Y > GWidth {
			t.Detter = t.Owner // wall hits are TDET at the owner: team-safe
			g.explodeTorp(t)
			continue
		}
		for _, p := range g.players {
			if p == nil || p.Status != "alive" || p.Team == t.Team {
				continue
			}
			if math.Hypot(p.X-t.X, p.Y-t.Y) <= ExpDist {
				g.explodeTorp(t)
				break
			}
		}
	}
}

func (g *Game) freeTorp(t *Torp) {
	delete(g.torps, t.ID)
	if o := g.players[t.Owner]; o != nil {
		o.NTorps--
	}
}

func (g *Game) explodeTorp(t *Torp) {
	g.freeTorp(t)
	g.booms = append(g.booms, Boom{t.X, t.Y, false})
	for _, p := range g.players {
		if p == nil || p.Status != "alive" || p.ID == t.Owner {
			continue
		}
		if t.Detter >= 0 && p.ID != t.Detter && g.players[t.Detter] != nil &&
			p.Team == g.players[t.Detter].Team {
			continue // TDETTEAMSAFE — but the detter himself eats the blast
		}
		dist := math.Hypot(p.X-t.X, p.Y-t.Y)
		if dist > DamDist {
			continue
		}
		dmg := t.Damage
		if dist > ExpDist {
			dmg = int(float64(t.Damage) * (DamDist - dist) / (DamDist - ExpDist))
		}
		credit := t.Owner
		if t.Detter >= 0 && p.ID != t.Detter {
			credit = t.Detter // detted torp kills credit the detter...
		} // ...except when it kills the detter: that one is the owner's
		g.hurt(p, dmg, credit, "torp")
	}
}

// ship explosion splash (blowup, daemon.c:3549)
func (g *Game) blowup(v *Player) {
	g.booms = append(g.booms, Boom{v.X, v.Y, true})
	base := 100
	switch v.Ship.Type {
	case "SC":
		base = 75
	case "SB":
		base = 200
	}
	for _, p := range g.players {
		if p == nil || p == v || p.Status != "alive" {
			continue
		}
		dist := math.Hypot(p.X-v.X, p.Y-v.Y)
		if dist > ShipDamAge {
			continue
		}
		dmg := base
		if dist > ExpDist {
			dmg = int(float64(base) * (ShipDamAge - dist) / (ShipDamAge - ExpDist))
		}
		// chain credit: whoever destroyed the exploding ship gets its splash kills
		g.hurt(p, dmg, v.WhoDead, "explosion")
	}
}

// ---------- planets ----------

// plfight (daemon.c:3059): planet defense fire + bombing, every 0.5 s
func (g *Game) planetFight() {
	for _, pl := range g.planets {
		if pl.Owner == TeamNone || pl.Armies == 0 {
			continue
		}
		for _, p := range g.players {
			if p == nil || p.Status != "alive" || p.Team == pl.Owner {
				continue
			}
			if math.Hypot(p.X-pl.X, p.Y-pl.Y) <= PFireDist {
				g.hurt(p, pl.Armies/10+2, -1, "planet fire from "+pl.Name)
			}
		}
	}
	for _, p := range g.players {
		if p == nil || p.Status != "alive" || !p.Bombing || p.Orbiting < 0 {
			continue
		}
		pl := g.planets[p.Orbiting]
		if pl.Owner == p.Team || pl.Armies < 5 || g.thirdSpace(pl) {
			continue // cannot bomb below 5 armies or 3rd space in t-mode
		}
		rnd := rand.Intn(100)
		var ab int
		switch {
		case rnd < 50:
			continue
		case rnd < 80:
			ab = 1
		case rnd < 90:
			ab = 2
		default:
			ab = 3
		}
		if p.Ship.Type == "AS" {
			ab++
		}
		pl.Armies -= ab
		p.Kills += 0.02 * float64(ab)
	}
}

// carryCapacity = trunc(kills to 0.01) * 2 (3 for AS); SB has no kills cap
func carryCapacity(p *Player) int {
	if p.Ship.Type == "SB" {
		return p.Ship.MaxArmies
	}
	mult := 2.0
	if p.Ship.Type == "AS" {
		mult = 3.0
	}
	return min(p.Ship.MaxArmies, int(math.Floor(p.Kills*100)/100*mult))
}

// beam (daemon.c:3207), every 0.8 s: one army per pulse
func (g *Game) beam() {
	for _, p := range g.players {
		if p == nil || p.Status != "alive" || p.Beaming == 0 || p.Orbiting < 0 {
			continue
		}
		pl := g.planets[p.Orbiting]
		if p.Beaming == 1 { // up
			if pl.Owner != p.Team || pl.Armies < 5 || p.Armies >= carryCapacity(p) {
				continue
			}
			p.Armies++
			pl.Armies--
		} else { // down
			if p.Armies <= 0 {
				continue
			}
			switch {
			case pl.Owner == p.Team:
				p.Armies--
				pl.Armies++
			case pl.Armies > 0:
				p.Armies--
				pl.Armies--
				p.Kills += 0.02
				if pl.Armies == 0 {
					loser := pl.Owner
					pl.Owner = TeamNone
					g.say("%s destroyed by %s (%s)", pl.Name, p.Name, teamLetter(p.Team))
					if g.checkGenocide(loser, p) {
						return // round over; planet list was rebuilt
					}
				}
			default: // independent planet: first army takes it
				p.Armies--
				pl.Armies = 1
				pl.Owner = p.Team
				p.Kills += 0.25
				g.say("%s taken by %s (%s)", pl.Name, p.Name, teamLetter(p.Team))
			}
		}
	}
}

// PopPlanet (daemon.c:2628): one planet per second, cycling a shuffled order
func (g *Game) popPlanet() {
	pl := g.planets[g.popOrder[g.popIdx]]
	g.popIdx++
	if g.popIdx >= len(g.popOrder) {
		g.popIdx = 0
		rand.Shuffle(len(g.popOrder), func(i, j int) {
			g.popOrder[i], g.popOrder[j] = g.popOrder[j], g.popOrder[i]
		})
	}
	if pl.Armies == 0 {
		return
	}
	if pl.Armies < 4 {
		if rand.Intn(20) == 0 {
			pl.Armies++
		}
		if pl.Flags&PlAgri != 0 {
			pl.Armies++
		}
	}
	if rand.Intn(10) == 0 {
		pl.Armies += rand.Intn(3) + 1
	}
	if pl.Flags&PlAgri != 0 && rand.Intn(5) == 0 {
		pl.Armies++
	}
}

// ---------- t-mode ----------

func (g *Game) tmodeNow() bool {
	var counts [4]int
	for _, p := range g.players {
		if p != nil && p.Team != TeamNone {
			counts[p.Team]++
		}
	}
	teams := 0
	for _, c := range counts {
		if c >= TournNeeded {
			teams++
		}
	}
	return teams >= 2
}

func (g *Game) checkTmode() {
	now := g.tmodeNow()
	if now && !g.tmode {
		g.tmode = true
		g.tmodeLeft = TournTicks
		g.say("T-MODE! 30 minutes on the clock.")
	} else if !now && g.tmode {
		g.endTmode("not enough players")
	}
}

func (g *Game) endTmode(reason string) {
	g.say("T-mode over (%s).", reason)
	g.endRound()
}

// endRound discards stats and rebuilds the galaxy; t-mode restarts on the
// next check if the player counts still qualify
func (g *Game) endRound() {
	g.tmode = false
	g.say("Stats discarded, galaxy reset.")
	g.planets = resetPlanets()
	for _, p := range g.players {
		if p != nil {
			p.Kills = 0
			p.Armies = 0
		}
	}
}

// checkgen (daemon.c:3776): a team that loses its last planet is genocided —
// every ship it has flying is destroyed. Vanilla then plays on toward the
// quadrant-conquer win; here a genocide of a populated team ends the round.
func (g *Game) checkGenocide(loser int, winner *Player) bool {
	if loser < 0 {
		return false
	}
	for _, pl := range g.planets {
		if pl.Owner == loser {
			return false
		}
	}
	populated := false
	for _, p := range g.players {
		if p != nil && p.Team == loser {
			populated = true
			break
		}
	}
	if !populated {
		return false // nobody flies for this empire; not a game ending
	}
	g.say("GENOCIDE! The %s empire has been wiped out by the %ss (%s).",
		teamNames[loser], teamNames[winner.Team], winner.Name)
	for _, p := range g.players {
		if p != nil && p.Team == loser && p.Status == "alive" {
			g.kill(p, -1, "genocide")
		}
	}
	g.endRound()
	return true
}
