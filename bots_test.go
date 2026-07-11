package main

import "testing"

func TestBotLifecycle(t *testing.T) {
	g := NewGame()
	g.clientsOnline = 1
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
	g.clientsOnline = 1
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

func TestBotScuttleWhenEmpty(t *testing.T) {
	g := NewGame()
	g.clientsOnline = 1
	g.BalanceBots()
	g.Tick()

	// last human leaves: bots arm their fuses
	g.clientsOnline = 0
	for i := 0; i < 115; i++ { // fuse (100) + explosion (10) + slack
		g.Tick()
	}
	g.mu.Lock()
	remaining := 0
	for _, p := range g.players {
		if p != nil {
			remaining++
		}
	}
	tm := g.tmode
	g.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("empty server should end with no bots, %d remain", remaining)
	}
	if tm {
		t.Fatal("t-mode should end once the bots are gone")
	}

	// a returning human cancels the scuttle
	g.clientsOnline = 1
	g.BalanceBots()
	g.Tick()
	g.clientsOnline = 0
	g.Tick() // fuses armed
	g.clientsOnline = 1
	g.Tick() // canceled
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.players {
		if p != nil && p.Bot != nil && p.Status == "alive" && p.SelfDest != 0 {
			t.Fatal("returning human should cancel bot self destruct")
		}
	}
}

func TestSelfDestruct(t *testing.T) {
	g := NewGame()
	p := addPlayer(t, g, "s", "F", "CA").player
	mate := addPlayer(t, g, "m", "F", "CA").player
	foe := addPlayer(t, g, "e", "R", "CA").player
	p.X, p.Y = 50000, 50000
	mate.X, mate.Y = 50200, 50000 // inside full-damage blast radius
	foe.X, foe.Y = 50000, 50200
	foe.ShieldsUp, mate.ShieldsUp = true, true
	p.Damage = 10 // not pristine: the fuse must actually burn

	g.Command(p, "selfdestruct", 0, 0)
	if p.SelfDest == 0 {
		t.Fatal("self destruct should be armed")
	}
	g.Command(p, "shields", 0, 0) // any action disarms (and doesn't move us)
	if p.SelfDest != 0 {
		t.Fatal("any command should cancel self destruct")
	}
	p.ShieldsUp = false // undo the toggle; stay parked next to the bystanders

	g.Command(p, "selfdestruct", 0, 0)
	for i := 0; i < 115 && p.Status != "dead"; i++ {
		g.Tick()
	}
	if p.Status != "dead" {
		t.Fatalf("fuse should have fired, status=%s", p.Status)
	}
	if foe.Shield == foe.Ship.MaxShield {
		t.Fatal("KQUIT blast should hurt nearby enemies")
	}
	if mate.Shield != mate.Ship.MaxShield || mate.Damage > 0 {
		t.Fatal("KQUIT blast must spare teammates")
	}

	// pristine ship in green alert fires instantly
	q := addPlayer(t, g, "q", "K", "CA").player
	q.X, q.Y = 5000, 5000 // far from everyone
	g.Command(q, "selfdestruct", 0, 0)
	g.Tick()
	if q.Status == "alive" {
		t.Fatal("healthy unthreatened ship should self destruct immediately")
	}
}

// TestBotWar simulates five minutes of 4v4 bot play and checks the bots
// actually fight and play the planet game without wedging the engine.
func TestBotWar(t *testing.T) {
	g := NewGame()
	g.clientsOnline = 1
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
