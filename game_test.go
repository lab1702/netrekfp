package main

import (
	"math"
	"testing"
)

func addPlayer(t *testing.T, g *Game, name, team, ship string) *Client {
	t.Helper()
	c := &Client{send: make(chan []byte, 4)}
	p, deny := g.Join(c, name, team, ship)
	if deny != "" {
		t.Fatalf("join %s/%s denied: %s", team, ship, deny)
	}
	if p.Status != "alive" {
		t.Fatalf("joined player not alive")
	}
	return c
}

func TestJoinCaps(t *testing.T) {
	g := NewGame()
	for i := 0; i < MaxPerTeam; i++ {
		addPlayer(t, g, "f", "F", "CA")
	}
	c := &Client{send: make(chan []byte, 4)}
	if _, deny := g.Join(c, "extra", "F", "CA"); deny == "" {
		t.Fatal("33rd player on a team should be denied")
	}
	addPlayer(t, g, "sb1", "R", "SB")
	c2 := &Client{send: make(chan []byte, 4)}
	if _, deny := g.Join(c2, "sb2", "R", "SB"); deny == "" {
		t.Fatal("second starbase on a team should be denied")
	}
}

func TestTmodeLifecycle(t *testing.T) {
	g := NewGame()
	var last *Client
	for i := 0; i < 4; i++ {
		addPlayer(t, g, "f", "F", "CA")
		last = addPlayer(t, g, "r", "R", "CA")
	}
	g.checkTmode()
	if !g.tmode || g.tmodeLeft != TournTicks {
		t.Fatalf("tmode should start with 4v4, got on=%v left=%d", g.tmode, g.tmodeLeft)
	}

	// timer expiry discards stats and resets the galaxy
	g.tmodeLeft = 1
	g.players[0].Kills = 5
	g.planets[0].Armies = 1
	g.Tick()
	if g.tmode {
		t.Fatal("tmode should end when timer runs out")
	}
	if g.players[0].Kills != 0 {
		t.Fatal("stats should be discarded at t-mode end")
	}
	if g.planets[0].Armies != topArmies {
		t.Fatal("galaxy should reset at t-mode end")
	}

	// re-enters t-mode (still 4v4), then a team dropping below 4 ends it
	g.checkTmode()
	if !g.tmode {
		t.Fatal("tmode should restart with 4v4")
	}
	g.Leave(last)
	g.checkTmode()
	if g.tmode {
		t.Fatal("tmode should end when a team drops below 4")
	}
}

func TestPhaserFalloff(t *testing.T) {
	g := NewGame()
	shooter := addPlayer(t, g, "s", "F", "CA").player
	victim := addPlayer(t, g, "v", "R", "CA").player
	shooter.X, shooter.Y = 50000, 50000
	victim.X, victim.Y = 53000, 50000 // 3000 = half of CA phaser range
	victim.ShieldsUp = true
	g.firePhaser(shooter, 0)
	got := victim.Ship.MaxShield - victim.Shield
	if got != 50 {
		t.Fatalf("phaser at half range should do 50, did %d", got)
	}
	if shooter.PhaserBusy != shooter.Ship.PhaserFuse {
		t.Fatal("phaser should be recharging")
	}
	g.firePhaser(shooter, 0)
	if victim.Ship.MaxShield-victim.Shield != 50 {
		t.Fatal("phaser should not fire while recharging")
	}
}

func TestTorpDamage(t *testing.T) {
	g := NewGame()
	shooter := addPlayer(t, g, "s", "F", "CA").player
	victim := addPlayer(t, g, "v", "R", "CA").player
	shooter.X, shooter.Y = 50000, 50000
	victim.X, victim.Y = 50000+float64(shooter.Ship.TorpSpeed*Warp1)+100, 50000
	victim.ShieldsUp = true
	g.fireTorp(shooter, 0)
	if shooter.NTorps != 1 {
		t.Fatal("torp should be in flight")
	}
	g.moveTorps() // moves within ExpDist of victim -> explodes for full damage
	if shooter.NTorps != 0 {
		t.Fatal("torp should have exploded")
	}
	got := victim.Ship.MaxShield - victim.Shield
	if got != shooter.Ship.TorpDamage {
		t.Fatalf("point-blank torp should do %d, did %d", shooter.Ship.TorpDamage, got)
	}
}

func TestPlanetCapture(t *testing.T) {
	g := NewGame()
	p := addPlayer(t, g, "a", "F", "AS").player
	pl := g.planets[10] // Romulus
	pl.Armies = 1
	p.X, p.Y = pl.X+OrbDist, pl.Y
	p.Speed = 0
	p.Kills = 2 // carry capacity
	p.Armies = 2
	g.enterOrbit(p)
	if p.Orbiting != pl.N {
		t.Fatal("should be orbiting Romulus")
	}
	p.Beaming = 2
	g.beam() // kills the last army -> planet independent
	if pl.Owner != TeamNone || pl.Armies != 0 {
		t.Fatalf("planet should go independent, owner=%d armies=%d", pl.Owner, pl.Armies)
	}
	g.beam() // first army onto independent planet takes it
	if pl.Owner != TeamFed || pl.Armies != 1 {
		t.Fatalf("planet should be Fed with 1 army, owner=%d armies=%d", pl.Owner, pl.Armies)
	}
	if math.Abs(p.Kills-(2+0.02+0.25)) > 1e-9 {
		t.Fatalf("kill credit wrong: %f", p.Kills)
	}
}

func TestTurnRateSlowsWithSpeed(t *testing.T) {
	g := NewGame()
	p := addPlayer(t, g, "s", "F", "CA").player
	p.X, p.Y = 50000, 50000
	turnAmount := func(speed int) float64 {
		p.Speed, p.DesSpeed = speed, speed
		p.Dir, p.SubDir = 0, 0
		p.DesDir = math.Pi
		before := p.Dir
		g.movePlayer(p)
		return math.Abs(p.Dir - before)
	}
	slow, fast := turnAmount(2), turnAmount(9)
	if slow <= fast {
		t.Fatalf("turning should be slower at high warp: warp2=%f warp9=%f", slow, fast)
	}
}
