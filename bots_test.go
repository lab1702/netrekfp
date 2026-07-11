package main

import "testing"

func TestBotLifecycle(t *testing.T) {
	g := NewGame()
	g.AddBotCmd("F")
	g.AddBotCmd("R")
	counts := func() map[string]int {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.teamCounts()
	}
	if c := counts(); c["F"] != 1 || c["R"] != 1 {
		t.Fatalf("expected 1 bot each on F and R, got %v", c)
	}
	g.RemoveBotCmd("F")
	if c := counts(); c["F"] != 0 {
		t.Fatalf("bot should be removed, got %v", c)
	}
	g.BalanceBots() // R has 1 -> tops up two most-populated teams to 4
	c := counts()
	if c["R"] != 4 {
		t.Fatalf("balance should fill Rom to 4, got %v", c)
	}
	total := 0
	for _, n := range c {
		total += n
	}
	if total != 8 {
		t.Fatalf("balance should yield a 4v4, got %v", c)
	}
	g.Tick() // includes checkTmode at tick%10==0? force it:
	g.mu.Lock()
	g.checkTmode()
	tm := g.tmode
	g.mu.Unlock()
	if !tm {
		t.Fatal("8 bots on 2 teams should trigger t-mode")
	}
	g.ClearBots()
	if c := counts(); len(c) != 0 {
		t.Fatalf("clear should remove all bots, got %v", c)
	}
}

func TestFillBots(t *testing.T) {
	g := NewGame()
	addPlayer(t, g, "h", "F", "CA")
	g.FillBots()
	g.mu.Lock()
	counts := g.teamCounts()
	g.mu.Unlock()
	for _, tm := range teamLetters {
		if counts[tm] != MaxPerTeam-1 {
			t.Fatalf("team %s should be at %d, got %d", tm, MaxPerTeam-1, counts[tm])
		}
	}
	// every team must still accept one human
	for _, tm := range teamLetters {
		c := &Client{send: make(chan []byte, 4)}
		if _, deny := g.Join(c, "late", tm, "CA"); deny != "" {
			t.Fatalf("human should fit on %s after FILL: %s", tm, deny)
		}
	}
	// and now the server is exactly full
	c := &Client{send: make(chan []byte, 4)}
	if _, deny := g.Join(c, "extra", "F", "CA"); deny == "" {
		t.Fatal("server should be full after filling the reserved slots")
	}
	// smoke: a full 124-bot server keeps ticking
	for range 20 {
		g.Tick()
	}
}

// TestBotWar simulates five minutes of 4v4 bot play and checks the bots
// actually fight and play the planet game without wedging the engine.
func TestBotWar(t *testing.T) {
	g := NewGame()
	g.BalanceBots()

	torpsFired, phaserShots := 0, 0
	for i := 0; i < 3000; i++ { // 5 minutes at 10 Hz
		g.Tick()
		g.mu.Lock()
		torpsFired += len(g.torps)
		phaserShots += len(g.phasers)
		g.msgs = g.msgs[:0] // normally drained by broadcast
		g.booms = g.booms[:0]
		g.phasers = g.phasers[:0]
		g.mu.Unlock()
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.tmode && g.tmodeLeft == 0 {
		t.Error("bot 4v4 should have entered t-mode")
	}
	if torpsFired == 0 {
		t.Error("bots never fired a torpedo in 5 minutes")
	}
	kills, bombing := 0.0, 0
	alive := 0
	for _, p := range g.players {
		if p == nil {
			continue
		}
		if p.Status == "alive" {
			alive++
		}
		kills += p.Kills
	}
	for _, pl := range g.planets {
		if pl.Armies != topArmies || pl.Owner == TeamNone {
			bombing++
		}
	}
	if alive == 0 {
		t.Error("all bots dead and none respawned")
	}
	// evidence of the planet game: armies changed somewhere (bombing, beaming,
	// growth) — with 3000 ticks of popPlanet alone this is virtually certain,
	// so a zero means the tick loop wedged
	if bombing == 0 {
		t.Error("galaxy completely untouched after 5 minutes")
	}
	t.Logf("after 5 min: alive=%d kills=%.2f torp-ticks=%d phaser-events=%d touched-planets=%d",
		alive, kills, torpsFired, phaserShots, bombing)
}
