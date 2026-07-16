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
	g.mu.Lock()
	beforeCancel := 0
	for _, p := range g.players {
		if p != nil && p.Bot != nil && p.Status == "alive" {
			beforeCancel++
		}
	}
	g.mu.Unlock()
	g.clientsOnline = 0
	g.Tick() // fuses armed
	g.mu.Lock()
	armedAlive := 0
	for _, p := range g.players {
		if p != nil && p.Bot != nil && p.Status == "alive" && p.SelfDest != 0 {
			armedAlive++
		}
	}
	g.mu.Unlock()
	if armedAlive != beforeCancel {
		t.Fatalf("arming scuttle should leave all %d bots alive for the fuse, got %d", beforeCancel, armedAlive)
	}
	g.clientsOnline = 1
	g.Tick() // canceled
	g.mu.Lock()
	defer g.mu.Unlock()
	afterCancel := 0
	for _, p := range g.players {
		if p == nil || p.Bot == nil {
			continue
		}
		if p.Status != "alive" {
			t.Fatalf("returning human should preserve bot %d, status=%s", p.ID, p.Status)
		}
		if p.SelfDest != 0 {
			t.Fatal("returning human should cancel bot self destruct")
		}
		afterCancel++
	}
	if afterCancel != beforeCancel {
		t.Fatalf("returning human should preserve all %d bots, got %d", beforeCancel, afterCancel)
	}
}

func TestScuttledBotPurgesTorps(t *testing.T) {
	g := NewGame()
	g.clientsOnline = 0
	if !g.addBot(TeamFed) {
		t.Fatal("failed to add bot")
	}
	var p *Player
	for _, q := range g.players {
		if q != nil && q.Bot != nil {
			p = q
			break
		}
	}
	if p == nil {
		t.Fatal("bot missing after add")
	}
	g.fireTorp(p, 0)
	if len(g.torps) != 1 || p.NTorps != 1 {
		t.Fatalf("test setup should create one torp, torps=%d count=%d", len(g.torps), p.NTorps)
	}

	p.Status = "dead"
	g.botsScuttling = true
	g.updateBots()
	if g.players[p.ID] != nil {
		t.Fatal("dead scuttling bot should release its slot")
	}
	if len(g.torps) != 0 || p.NTorps != 0 {
		t.Fatalf("scuttled bot torps should be purged, torps=%d count=%d", len(g.torps), p.NTorps)
	}
}

func TestBotAddedWhileEmptyScuttleIsActiveGetsArmed(t *testing.T) {
	g := NewGame()
	g.clientsOnline = 0
	g.botsScuttling = true // stale/active empty-server teardown state
	if !g.addBot(TeamFed) {
		t.Fatal("failed to add bot")
	}
	bot := g.players[0]
	if bot.SelfDest != 0 {
		t.Fatal("new bot should begin with no self-destruct fuse")
	}

	g.Tick()
	if !g.botsScuttling || bot.SelfDest == 0 || bot.Status != "alive" {
		t.Fatalf("new empty-server bot was not armed safely: scuttling=%v fuse=%d status=%s",
			g.botsScuttling, bot.SelfDest, bot.Status)
	}
}

func TestBotCloakFuelAndRangeTransitions(t *testing.T) {
	tests := []struct {
		name      string
		fuel      int
		dist      float64
		cloaked   bool
		wantCloak bool
	}{
		{name: "low fuel exits cloak", fuel: 1499, dist: 4000, cloaked: true},
		{name: "knife range exits cloak", fuel: 4000, dist: 999, cloaked: true},
		{name: "moderate fuel cannot enter cloak", fuel: 3000, dist: 4000},
		{name: "high fuel enters cloak", fuel: 3001, dist: 4000, wantCloak: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGame()
			p := &Player{
				ID: 0, Name: "scout", Team: TeamFed, Ship: shipTypes["SC"], Bot: newBotState(),
				Status: "alive", Orbiting: -1, Fuel: tt.fuel, Cloaked: tt.cloaked,
				X: 50000, Y: 50000, LastTorpTick: -1,
			}
			target := &Player{
				ID: 1, Name: "target", Team: TeamRom, Ship: shipTypes["CA"],
				Status: "alive", Orbiting: -1, Fuel: shipTypes["CA"].MaxFuel,
				X: 50000 + tt.dist, Y: 50000, LastTorpTick: -1,
			}
			g.players[p.ID], g.players[target.ID] = p, target
			g.botEngage(p, target, tt.dist, combatThreat{})
			if p.Cloaked != tt.wantCloak {
				t.Fatalf("cloaked=%v, want %v (fuel=%d dist=%.0f)", p.Cloaked, tt.wantCloak, tt.fuel, tt.dist)
			}
		})
	}
}

func TestBotObjectiveActionsLowerShields(t *testing.T) {
	botAt := func(g *Game, team, planet int) *Player {
		pl := g.planets[planet]
		p := &Player{
			ID: 0, Name: "objective bot", Team: team, Ship: shipTypes["CA"], Bot: newBotState(),
			Status: "alive", Orbiting: pl.N, X: pl.X, Y: pl.Y,
			Fuel: shipTypes["CA"].MaxFuel, Kills: 1, ShieldsUp: true, RepairMode: true,
			LastTorpTick: -1,
		}
		g.players[p.ID] = p
		return p
	}
	assertCommon := func(t *testing.T, p *Player) {
		t.Helper()
		if p.ShieldsUp {
			t.Fatal("bot objective action must lower shields")
		}
		if p.RepairMode {
			t.Fatal("bot objective action must cancel repair mode")
		}
	}

	t.Run("beam armies up", func(t *testing.T) {
		g := NewGame()
		p := botAt(g, TeamFed, TeamFed*10)
		g.botTournament(p, nil, maxSearch, combatThreat{})
		if p.Beaming != 1 || p.Bombing {
			t.Fatalf("expected beam-up objective, beaming=%d bombing=%v", p.Beaming, p.Bombing)
		}
		assertCommon(t, p)
	})

	t.Run("beam armies down", func(t *testing.T) {
		g := NewGame()
		pl := g.planets[TeamFed*10]
		pl.Owner = TeamNone
		p := botAt(g, TeamFed, pl.N)
		p.Armies = 1
		g.botTournament(p, nil, maxSearch, combatThreat{})
		if p.Beaming != 2 || p.Bombing {
			t.Fatalf("expected beam-down objective, beaming=%d bombing=%v", p.Beaming, p.Bombing)
		}
		assertCommon(t, p)
	})

	t.Run("start tournament bombing", func(t *testing.T) {
		g := NewGame()
		for _, pl := range g.planets {
			pl.Armies = 4
		}
		pl := g.planets[TeamRom*10]
		pl.Armies = 10
		p := botAt(g, TeamFed, pl.N)
		p.Kills = 0
		p.Beaming = 1
		g.botTournament(p, nil, maxSearch, combatThreat{})
		if !p.Bombing || p.Beaming != 0 {
			t.Fatalf("expected bombing objective, bombing=%v beaming=%d", p.Bombing, p.Beaming)
		}
		assertCommon(t, p)
	})

	t.Run("continue bombing through distant combat", func(t *testing.T) {
		g := NewGame()
		pl := g.planets[TeamRom*10]
		p := botAt(g, TeamFed, pl.N)
		p.Beaming = 1
		target := &Player{
			ID: 1, Name: "target", Team: TeamRom, Ship: shipTypes["CA"], Status: "alive",
			Orbiting: -1, X: p.X + 5000, Y: p.Y, Fuel: shipTypes["CA"].MaxFuel,
		}
		g.players[target.ID] = target
		g.botEngage(p, target, 5000, combatThreat{})
		if !p.Bombing || p.Beaming != 0 {
			t.Fatalf("expected continued bombing, bombing=%v beaming=%d", p.Bombing, p.Beaming)
		}
		assertCommon(t, p)
	})

	for _, tc := range []struct {
		name    string
		bombing bool
		beaming int
	}{
		{name: "persistent bombing under threat", bombing: true},
		{name: "persistent beaming under threat", beaming: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGame()
			p := botAt(g, TeamFed, TeamRom*10)
			p.Bombing, p.Beaming = tc.bombing, tc.beaming
			p.RepairMode = false
			p.Bot.Cooldown = 5
			foe := &Player{
				ID: 1, Name: "threat", Team: TeamRom, Ship: shipTypes["CA"], Status: "alive",
				Orbiting: -1, X: p.X + 1000, Y: p.Y, Fuel: shipTypes["CA"].MaxFuel,
			}
			g.players[foe.ID] = foe

			g.updateBot(p)
			if p.ShieldsUp {
				t.Fatal("bot raised shields while an objective action remained active")
			}
			if p.Bombing != tc.bombing || p.Beaming != tc.beaming {
				t.Fatalf("cooldown tick changed objective action: bombing=%v beaming=%d",
					p.Bombing, p.Beaming)
			}
		})
	}
}

func TestBotStopsThirdSpaceBombing(t *testing.T) {
	setup := func(t *testing.T) (*Game, *Player) {
		t.Helper()
		g := NewGame()
		g.tmode = true
		if !g.addBot(TeamFed) {
			t.Fatal("failed to add bot")
		}
		var p *Player
		for _, q := range g.players {
			if q != nil && q.Bot != nil {
				p = q
				break
			}
		}
		pl := g.planets[TeamKli*10]
		p.X, p.Y = pl.X, pl.Y
		p.Speed, p.DesSpeed = 0, 0
		p.Orbiting = pl.N
		p.Bombing = true
		return g, p
	}

	t.Run("update exits forbidden bombing", func(t *testing.T) {
		g, p := setup(t)
		g.updateBot(p)
		if p.Bombing {
			t.Fatal("bot should stop bombing a third-space planet")
		}
	})

	t.Run("tournament logic does not stay bombing", func(t *testing.T) {
		g, p := setup(t)
		g.botTournament(p, nil, maxSearch, combatThreat{})
		if p.Bombing {
			t.Fatal("bot should not remain in the tournament bombing loop on a third-space planet")
		}
	})

	t.Run("distant combat does not restart forbidden bombing", func(t *testing.T) {
		g, p := setup(t)
		target := &Player{
			ID: 1, Name: "target", Team: TeamRom, Ship: shipTypes["CA"], Status: "alive",
			Orbiting: -1, X: p.X + 5000, Y: p.Y, Fuel: shipTypes["CA"].MaxFuel,
		}
		g.players[target.ID] = target

		g.botEngage(p, target, 5000, combatThreat{})
		if p.Bombing || p.Orbiting >= 0 {
			t.Fatalf("combat path restarted third-space bombing: bombing=%v orbit=%d",
				p.Bombing, p.Orbiting)
		}
	})
}

func TestBotRolesUseExplicitAssignments(t *testing.T) {
	g := NewGame()
	for _, pl := range g.planets {
		pl.Owner = TeamNone
	}

	bots := make([]*Player, 0, 3)
	for len(bots) < 3 {
		if !g.addBot(TeamFed) {
			t.Fatal("failed to add role-test bot")
		}
		bots = bots[:0]
		for _, p := range g.players {
			if p != nil && p.Bot != nil && p.Team == TeamFed {
				bots = append(bots, p)
			}
		}
	}
	target := &Player{
		ID: 3, Name: "target", Team: TeamRom, Ship: shipTypes["CA"], Status: "alive",
		Orbiting: -1, Fuel: shipTypes["CA"].MaxFuel, X: 50000, Y: 50000,
	}
	g.players[target.ID] = target
	for _, p := range bots {
		p.Bot.Target = target.ID // stale combat state must not define the strategic role
		p.Bot.DefenseTarget = -1
	}
	bots[0].Bot.Role = botRoleDefender
	bots[1].Bot.Role = botRoleDefender
	bots[2].Bot.Role = botRoleUnset

	if got := g.botRole(bots[2]); got != botRoleRaider {
		t.Fatalf("two explicit defenders in weak space should make the next bot a raider, got %v", got)
	}
	g.botFreePlay(bots[2], target, 10000, combatThreat{})
	if bots[2].Bot.Role != botRoleRaider {
		t.Fatalf("free-play assignment should persist raider role through combat targeting, got %v", bots[2].Bot.Role)
	}

	pl := g.planets[0]
	pl.Owner = TeamFed
	g.botDefendPlanet(bots[2], pl, target, 5000)
	if bots[2].Bot.Role != botRoleDefender {
		t.Fatalf("immediate planet defense should persist defender role, got %v", bots[2].Bot.Role)
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
