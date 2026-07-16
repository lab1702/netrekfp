package main

import "testing"

func orbitAtForCoreTest(t *testing.T, g *Game, p *Player, planet int) {
	t.Helper()
	pl := g.planets[planet]
	p.X, p.Y = pl.X+OrbDist, pl.Y
	p.Speed, p.DesSpeed = 0, 0
	g.enterOrbit(p)
	if p.Orbiting != planet {
		t.Fatalf("player did not enter orbit around planet %d", planet)
	}
}

func armForExplosionTest(p *Player, x, y float64) {
	p.X, p.Y = x, y
	p.ShieldsUp = false
	p.Damage = p.Ship.MaxDamage - 1
}

func TestBombAndBeamRequireLoweredShields(t *testing.T) {
	g := NewGame()
	p := addPlayer(t, g, "attacker", "F", "CA").player
	orbitAtForCoreTest(t, g, p, 10) // enemy homeworld

	g.Command(p, "bomb", 0, 0)
	if !p.Bombing || p.ShieldsUp {
		t.Fatalf("starting a bomb run must lower shields: bombing=%v shields=%v",
			p.Bombing, p.ShieldsUp)
	}
	g.Command(p, "shields", 0, 0)
	if !p.ShieldsUp || p.Bombing {
		t.Fatalf("raising shields must cancel bombing: bombing=%v shields=%v",
			p.Bombing, p.ShieldsUp)
	}

	for _, tc := range []struct {
		cmd  string
		want int
	}{{"beamup", 1}, {"beamdown", 2}} {
		g.Command(p, tc.cmd, 0, 0)
		if p.Beaming != tc.want || p.ShieldsUp {
			t.Fatalf("%s must lower shields: beaming=%d shields=%v",
				tc.cmd, p.Beaming, p.ShieldsUp)
		}
		g.Command(p, "shields", 0, 0)
		if !p.ShieldsUp || p.Beaming != 0 {
			t.Fatalf("raising shields must cancel %s: beaming=%d shields=%v",
				tc.cmd, p.Beaming, p.ShieldsUp)
		}
	}
}

func TestRepairClearsPlanetLockButPreservesOrbit(t *testing.T) {
	t.Run("approach autopilot", func(t *testing.T) {
		g := NewGame()
		p := addPlayer(t, g, "repairer", "F", "CA").player
		p.LockPlanet = 13
		p.DesSpeed = p.Ship.MaxSpeed

		g.Command(p, "repair", 0, 0)
		if !p.RepairMode || p.LockPlanet != -1 || p.DesSpeed != 0 {
			t.Fatalf("repair must stop and clear autopilot: repair=%v lock=%d speed=%d",
				p.RepairMode, p.LockPlanet, p.DesSpeed)
		}
		g.movePlayer(p)
		if p.DesSpeed != 0 {
			t.Fatalf("cleared autopilot must not restore throttle, got warp %d", p.DesSpeed)
		}
	})

	t.Run("orbit repair", func(t *testing.T) {
		g := NewGame()
		p := addPlayer(t, g, "repairer", "F", "CA").player
		orbitAtForCoreTest(t, g, p, 0)

		g.Command(p, "repair", 0, 0)
		if !p.RepairMode || p.Orbiting != 0 {
			t.Fatalf("repair must preserve an established orbit: repair=%v orbit=%d",
				p.RepairMode, p.Orbiting)
		}
	})
}

func TestRejoinPurgesOnlyWrongTeamTorps(t *testing.T) {
	t.Run("different team", func(t *testing.T) {
		g := NewGame()
		c := addPlayer(t, g, "shooter", "F", "CA")
		p := c.player
		g.fireTorp(p, 0)
		if len(g.torps) != 1 || p.NTorps != 1 {
			t.Fatal("test setup did not create a torpedo")
		}

		p.Status = "dead"
		if _, deny := g.Join(c, "shooter", "R", "CA"); deny != "" {
			t.Fatalf("rejoin denied: %s", deny)
		}
		if len(g.torps) != 0 || p.NTorps != 0 {
			t.Fatalf("old-team torpedo survived reteam: torps=%d count=%d",
				len(g.torps), p.NTorps)
		}
	})

	t.Run("same team", func(t *testing.T) {
		g := NewGame()
		c := addPlayer(t, g, "shooter", "F", "CA")
		p := c.player
		g.fireTorp(p, 0)
		var fired *Torp
		for _, torp := range g.torps {
			fired = torp
		}

		p.Status = "dead"
		if _, deny := g.Join(c, "shooter", "F", "CA"); deny != "" {
			t.Fatalf("rejoin denied: %s", deny)
		}
		if len(g.torps) != 1 || p.NTorps != 1 || fired.owner != p {
			t.Fatalf("same-team torpedo ownership was lost: torps=%d count=%d owner=%p player=%p",
				len(g.torps), p.NTorps, fired.owner, p)
		}
	})
}

func TestTorpOwnershipDoesNotFollowReusedSlot(t *testing.T) {
	g := NewGame()
	old := addPlayer(t, g, "old", "F", "CA").player
	g.fireTorp(old, 0)
	var torp *Torp
	for _, candidate := range g.torps {
		torp = candidate
	}

	// Simulate a slot being reused while delayed projectile state still exists.
	// The projectile must remain tied to the original Player object, not its ID.
	replacement := &Player{ID: old.ID, Name: "replacement", Team: TeamRom, Ship: shipTypes["CA"]}
	g.players[old.ID] = replacement
	g.spawn(replacement)
	if replacement.NTorps != 0 {
		t.Fatalf("replacement inherited %d torpedoes from the reused slot", replacement.NTorps)
	}
	torp.X, torp.Y = replacement.X, replacement.Y
	g.explodeTorp(torp)

	if old.NTorps != 0 {
		t.Fatalf("original owner count was not released: %d", old.NTorps)
	}
	if replacement.NTorps != 0 {
		t.Fatalf("replacement torpedo count changed: %d", replacement.NTorps)
	}
	if replacement.Shield == replacement.Ship.MaxShield {
		t.Fatal("replacement was incorrectly immune because it reused the owner slot")
	}
}

func TestTorpKillCreditDoesNotFollowReusedSlot(t *testing.T) {
	g := NewGame()
	old := addPlayer(t, g, "old shooter", "F", "CA").player
	victim := addPlayer(t, g, "victim", "R", "CA").player
	g.fireTorp(old, 0)
	var torp *Torp
	for _, candidate := range g.torps {
		torp = candidate
	}

	replacement := &Player{ID: old.ID, Name: "replacement", Team: TeamFed, Ship: shipTypes["CA"]}
	g.players[old.ID] = replacement
	g.spawn(replacement)
	torp.X, torp.Y = 50000, 50000
	armForExplosionTest(victim, torp.X, torp.Y)
	g.explodeTorp(torp)

	if victim.WhoDead != old {
		t.Fatalf("torpedo credit followed reused slot: got %p want original %p",
			victim.WhoDead, old)
	}
	if old.Kills == 0 {
		t.Fatal("original torpedo owner did not receive kill credit")
	}
	if replacement.Kills != 0 {
		t.Fatalf("replacement received old torpedo credit: %.2f", replacement.Kills)
	}
}

func TestExplosionCreditDoesNotFollowReusedSlot(t *testing.T) {
	g := NewGame()
	oldClient := addPlayer(t, g, "old-killer", "F", "CA")
	old := oldClient.player
	exploding := addPlayer(t, g, "exploding", "R", "CA").player
	bystander := addPlayer(t, g, "bystander", "O", "CA").player

	g.kill(exploding, old, old.Team, "phaser")
	initialOldKills := old.Kills
	g.Leave(oldClient)
	replacement := addPlayer(t, g, "replacement", "F", "CA").player
	if replacement.ID != old.ID {
		t.Fatalf("test setup did not reuse slot %d; got %d", old.ID, replacement.ID)
	}
	exploding.X, exploding.Y = 50000, 50000
	armForExplosionTest(bystander, exploding.X, exploding.Y)
	g.blowup(exploding)

	if bystander.WhoDead != old {
		t.Fatalf("splash should retain original killer identity: got %p want %p",
			bystander.WhoDead, old)
	}
	if old.Kills <= initialOldKills {
		t.Fatal("original killer did not receive the delayed splash credit")
	}
	if replacement.Kills != 0 {
		t.Fatalf("replacement inherited delayed kill credit: %.2f", replacement.Kills)
	}
}

func TestExplosionCreditStopsWhenKillerChangesTeam(t *testing.T) {
	g := NewGame()
	killerClient := addPlayer(t, g, "killer", "F", "CA")
	killer := killerClient.player
	exploding := addPlayer(t, g, "exploding", "R", "CA").player
	bystander := addPlayer(t, g, "bystander", "O", "CA").player

	g.kill(exploding, killer, killer.Team, "phaser")
	g.Command(killer, "quit", 0, 0)
	if _, deny := g.Join(killerClient, "killer", "K", "CA"); deny != "" {
		t.Fatalf("reteam denied: %s", deny)
	}
	exploding.X, exploding.Y = 50000, 50000
	armForExplosionTest(bystander, exploding.X, exploding.Y)
	g.blowup(exploding)

	if bystander.WhoDead != exploding {
		t.Fatalf("re-teamed killer retained stale splash credit: got %p want exploding %p",
			bystander.WhoDead, exploding)
	}
	if killer.Kills != 0 {
		t.Fatalf("re-teamed killer received old-team credit: %.2f", killer.Kills)
	}
	if exploding.Kills == 0 {
		t.Fatal("exploding ship should receive credit after the original killer changes team")
	}
}

func TestVanillaExplosionCreditExceptions(t *testing.T) {
	t.Run("original killer", func(t *testing.T) {
		g := NewGame()
		killer := addPlayer(t, g, "killer", "F", "CA").player
		exploding := addPlayer(t, g, "exploding", "R", "CA").player
		exploding.X, exploding.Y = 50000, 50000
		armForExplosionTest(killer, exploding.X, exploding.Y)

		g.kill(exploding, killer, killer.Team, "torp")
		g.blowup(exploding)
		if killer.WhoDead != exploding {
			t.Fatalf("exploding ship must get credit when its blast kills its killer: got %p want %p",
				killer.WhoDead, exploding)
		}
	})

	t.Run("original killer teammate", func(t *testing.T) {
		g := NewGame()
		killer := addPlayer(t, g, "killer", "F", "CA").player
		mate := addPlayer(t, g, "mate", "F", "CA").player
		exploding := addPlayer(t, g, "exploding", "R", "CA").player
		exploding.X, exploding.Y = 50000, 50000
		armForExplosionTest(mate, exploding.X, exploding.Y)

		g.kill(exploding, killer, killer.Team, "phaser")
		g.blowup(exploding)
		if mate.WhoDead != exploding {
			t.Fatalf("exploding ship must get credit for a killer teammate: got %p want %p",
				mate.WhoDead, exploding)
		}
	})
}

func TestEnvironmentalExplosionCreditBelongsToExplodingShip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		why      string
		selfKill bool
	}{{"planet", "planet fire", false}, {"self destruct", "self destruct", true}, {"genocide", "genocide", false}} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGame()
			exploding := addPlayer(t, g, "exploding", "R", "CA").player
			bystander := addPlayer(t, g, "bystander", "F", "CA").player
			exploding.X, exploding.Y = 50000, 50000
			armForExplosionTest(bystander, exploding.X, exploding.Y)
			exploding.SelfKill = tc.selfKill

			g.kill(exploding, nil, TeamNone, tc.why)
			g.blowup(exploding)
			if bystander.WhoDead != exploding {
				t.Fatalf("%s splash was not credited to exploding ship: got %p want %p",
					tc.name, bystander.WhoDead, exploding)
			}
		})
	}
}

func TestBotScuttleDoesNotUseGreenAlertShortcut(t *testing.T) {
	g := NewGame()
	if !g.addBot(TeamFed) {
		t.Fatal("could not add test bot")
	}
	bot := g.players[0]
	g.clientsOnline = 0
	g.Tick()
	if !g.botsScuttling || bot.SelfDest == 0 || bot.Status != "alive" {
		t.Fatalf("bot should remain alive with an armed fuse: scuttling=%v fuse=%d status=%s",
			g.botsScuttling, bot.SelfDest, bot.Status)
	}

	addPlayer(t, g, "returning human", "R", "CA")
	g.Tick()
	if g.botsScuttling || bot.SelfDest != 0 || bot.Status != "alive" {
		t.Fatalf("returning human should cancel live bot scuttle: scuttling=%v fuse=%d status=%s",
			g.botsScuttling, bot.SelfDest, bot.Status)
	}
}
