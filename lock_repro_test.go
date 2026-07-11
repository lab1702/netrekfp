package main

import (
	"math"
	"testing"
)

// Reproduces: lock-on "doesn't work" when a manual speed was already set.
// Ship at manual warp 9 heading 90 degrees off, planet 12k away.
func TestPlanetLockAtHighWarp(t *testing.T) {
	g := NewGame()
	p := addPlayer(t, g, "s", "F", "CA").player
	pl := g.planets[13] // Regulus
	p.X, p.Y = pl.X+12000, pl.Y
	p.Dir, p.DesDir = math.Pi/2, math.Pi/2 // flying tangentially
	p.Speed, p.DesSpeed = 9, 9             // manual max warp
	p.SubSpeed = 0

	g.Command(p, "lock", 0, 13)
	minD, maxD := 1e18, 0.0
	ticks := 0
	for ; ticks < 900 && p.Orbiting != 13; ticks++ {
		g.Tick()
		d := math.Hypot(p.X-pl.X, p.Y-pl.Y)
		minD = math.Min(minD, d)
		maxD = math.Max(maxD, d)
	}
	t.Logf("ticks=%d orbiting=%d dist min=%.0f max=%.0f speed=%d",
		ticks, p.Orbiting, minD, maxD, p.Speed)
	if p.Orbiting != 13 {
		t.Fatalf("lock at manual warp 9 never reaches orbit (90s): min dist %.0f", minD)
	}
}
