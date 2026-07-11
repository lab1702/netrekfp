package main

// Bot AI, ported from lab1702/netrek-web (server/bots.go, bot_combat.go,
// bot_navigation.go, bot_weapons.go, bot_planet.go, bot_helpers.go) and
// adapted to this engine's Bronco rules: no plasma, one torp per tick
// (spreads fire as sequential volleys), bombing floors at 5 armies so
// captures finish by beaming onto 1-4 army planets, orbit entry needs
// warp<=2 within 900 units.

import (
	"math"
	"math/rand"
)

// tuning constants from netrek-web ai_constants.go
const (
	botFuelCritical = 400
	botFuelLow      = 600
	botFuelModerate = 1400
	botFuelGood     = 2000

	botRepairSafeDist = 12000.0
	botRecentHitTicks = 30

	sepMinSafe  = 4000.0
	sepIdeal    = 2500.0
	sepCritical = 1200.0

	targetPersistenceBonus = 3000.0
	targetLockTicks        = 30

	planetDefenseRadius = 15000.0
	maxSearch           = 999999.0
)

type combatThreat struct {
	closestTorp  float64
	closestEnemy float64
	nearbyFoes   int
	threatLevel  int
	shieldLevel  int
	evade        bool
	immediate    bool
}

// ---------- brain ----------

func (g *Game) updateBot(p *Player) {
	b := p.Bot
	th := g.botThreats(p)
	g.botShields(p, th)

	if p.Damage > b.PrevDamage {
		b.HitTimer = botRecentHitTicks
	}
	b.PrevDamage = p.Damage
	recentlyHit := b.HitTimer > 0
	if b.HitTimer > 0 {
		b.HitTimer--
	}

	// sequential torp volley continues even during cooldown (engine allows
	// only one torp per tick, so netrek-web's spread becomes a burst)
	if b.VolleyLeft > 0 {
		g.botVolleyShot(p)
	}

	if b.Cooldown > 0 {
		b.Cooldown--
		return
	}

	// stuck-bombing fix (netrek-web bots.go:100): planet flipped or bombed out
	if p.Bombing && p.Orbiting >= 0 {
		pl := g.planets[p.Orbiting]
		if pl.Owner == p.Team || pl.Armies < 5 {
			p.Bombing = false
			b.Cooldown = 5
		}
	}

	// highest priority: a friendly planet under immediate threat
	if pl, enemy, enemyDist := g.threatenedPlanet(p); pl != nil {
		g.botDefendPlanet(p, pl, enemy, enemyDist)
		return
	}
	b.DefenseTarget = -1

	s := p.Ship
	needRepair := p.Damage > s.MaxDamage/2
	needFuel := p.Fuel < s.MaxFuel/3
	critical := p.Damage > s.MaxDamage*3/4

	enemy := g.botNearestEnemy(p)
	enemyDist := maxSearch
	if enemy != nil {
		enemyDist = dist2d(p.X, p.Y, enemy.X, enemy.Y)
	}

	// stay orbiting a friendly planet while healing and safe
	if p.Orbiting >= 0 && g.planets[p.Orbiting].Owner == p.Team {
		if (needRepair || needFuel) && enemyDist > botRepairSafeDist && !recentlyHit {
			p.DesSpeed = 0
			p.ShieldsUp = false
			if needRepair {
				p.RepairMode = true
			}
			b.Cooldown = 20
			return
		}
	}

	// repair in open space when safe
	if needRepair && enemyDist > botRepairSafeDist && !p.RepairMode && p.Speed < 2 && !recentlyHit {
		p.RepairMode = true
		p.DesSpeed = 0
		p.ShieldsUp = false
		b.Cooldown = 30
		return
	}
	if p.RepairMode && (enemyDist < botRepairSafeDist || recentlyHit) {
		p.RepairMode = false
	}

	// resume an interrupted planet approach once defenders are handled
	if b.PlanetApproach >= 0 {
		ap := g.planets[b.PlanetApproach]
		count, minDist, _, _, _ := g.planetDefenders(ap, p.Team)
		if count == 0 || minDist > 10000 {
			d := dist2d(p.X, p.Y, ap.X, ap.Y)
			if d < EntOrbDist {
				b.PlanetApproach = -1
			} else {
				g.botNavigate(p, math.Atan2(ap.Y-p.Y, ap.X-p.X), g.botApproachSpeed(p, d), th)
				b.Cooldown = 5
				return
			}
		}
	}

	// travel to a repair/fuel planet when hurting and not pressed
	if (needRepair || needFuel) && (enemyDist > 15000 || critical) {
		var target *Planet
		if needFuel {
			target = g.botNearestPlanet(p, func(pl *Planet) bool {
				return pl.Owner == p.Team && pl.Flags&PlFuel != 0
			})
		}
		if target == nil && needRepair {
			target = g.botNearestPlanet(p, func(pl *Planet) bool {
				return pl.Owner == p.Team && pl.Flags&PlRepair != 0
			})
		}
		if target != nil {
			if g.botGoOrbit(p, target, th) {
				p.ShieldsUp = false
				if needRepair {
					p.RepairMode = true
				}
				b.Cooldown = 30
			}
			return
		}
	}

	if g.tmode {
		g.botTournament(p, enemy, enemyDist, th)
		return
	}
	g.botFreePlay(p, enemy, enemyDist, th)
}

// tournament mode: planets win games (netrek-web bots.go:282-522)
func (g *Game) botTournament(p *Player, enemy *Player, enemyDist float64, th combatThreat) {
	b := p.Bot

	// carrying: deliver to the nearest takeable planet (never 3rd space)
	if p.Armies > 0 {
		drop := g.botNearestPlanet(p, func(pl *Planet) bool {
			return pl.Owner == TeamNone ||
				(pl.Owner != p.Team && pl.Armies < 5 && !g.thirdSpace(pl))
		})
		if drop == nil {
			g.botSafeArea(p)
			return
		}
		if g.botGoOrbit(p, drop, th) {
			p.Bombing = false
			p.Beaming = 2
			b.Cooldown = 20
			return
		}
		if enemy != nil && enemyDist < 5000 {
			g.botDefendCarrying(p, enemy, enemyDist)
		}
		return
	}

	// keep bombing a planet we're already working on
	if p.Bombing && p.Orbiting >= 0 {
		pl := g.planets[p.Orbiting]
		if pl.Owner != p.Team && pl.Owner != TeamNone && pl.Armies >= 5 {
			if enemyDist < 2000 && p.Damage > p.Ship.MaxDamage*2/3 {
				g.breakOrbit(p)
			} else {
				b.Cooldown = 10
				return
			}
		}
	}

	// pick an objective
	var target *Planet
	canCarryMore := carryCapacity(p) > p.Armies
	armyPlanet := g.botNearestPlanet(p, func(pl *Planet) bool {
		return pl.Owner == p.Team && pl.Armies > 4
	})
	bombPlanet := g.botNearestPlanet(p, func(pl *Planet) bool {
		return pl.Owner != p.Team && pl.Owner != TeamNone && pl.Armies > 4 &&
			!g.thirdSpace(pl)
	})
	takePlanet := g.botBestTakePlanet(p)
	switch {
	case canCarryMore && armyPlanet != nil:
		target = armyPlanet
	case bombPlanet != nil:
		target = bombPlanet
	case takePlanet != nil && canCarryMore:
		target = takePlanet
	case enemy != nil && enemyDist < 20000:
		g.botEngage(p, enemy, enemyDist, th)
		return
	}
	if target == nil {
		if enemy != nil && enemyDist < 15000 {
			g.botEngage(p, enemy, enemyDist, th)
		} else {
			g.botPatrol(p, th)
		}
		return
	}

	// clear defenders before committing to the planet
	count, minDist, score, closest, carrier := g.planetDefenders(target, p.Team)
	if count > 0 && dist2d(p.X, p.Y, target.X, target.Y) > EntOrbDist {
		if score > 2500 || minDist < 6000 {
			if count >= 3 && g.botAlliesNear(p, 15000) == 0 {
				b.PlanetApproach = -1
				b.Cooldown = 50 // too hot, look elsewhere
				return
			}
			primary := carrier
			if primary == nil {
				primary = closest
			}
			if primary != nil {
				b.PlanetApproach = target.N
				g.breakOrbit(p)
				g.botEngage(p, primary, dist2d(p.X, p.Y, primary.X, primary.Y), th)
				return
			}
		}
	}

	b.PlanetApproach = target.N
	if g.botGoOrbit(p, target, th) {
		b.PlanetApproach = -1
		switch {
		case target.Owner == p.Team:
			if target.Armies > 4 && canCarryMore {
				p.Bombing = false
				p.Beaming = 1
				b.Cooldown = 20
			} else {
				g.breakOrbit(p)
				b.Cooldown = 10
			}
		case target.Armies >= 5:
			p.Bombing = true
			p.Beaming = 0
			b.Cooldown = 10
		case p.Armies > 0:
			p.Bombing = false
			p.Beaming = 2 // kill the last defenders / take it
			b.Cooldown = 10
		default:
			g.breakOrbit(p)
			b.Cooldown = 10
		}
		return
	}
	if enemy != nil && enemyDist < 4000 {
		g.botEngage(p, enemy, enemyDist, th)
	}
}

// non-tournament: roles for practice combat (netrek-web bots.go:524-620)
func (g *Game) botFreePlay(p *Player, enemy *Player, enemyDist float64, th combatThreat) {
	critical := p.Damage > p.Ship.MaxDamage*3/4
	switch g.botRole(p) {
	case 0: // hunter
		if t := g.botBestTarget(p); t != nil {
			d := dist2d(p.X, p.Y, t.X, t.Y)
			if critical && d < 6000 {
				g.botSafeArea(p)
				return
			}
			g.botEngage(p, t, d, th)
			return
		}
	case 1: // defender
		if pl := g.botPlanetToDefend(p); pl != nil {
			d := dist2d(p.X, p.Y, pl.X, pl.Y)
			if d > 5000 {
				g.botNavigate(p, math.Atan2(pl.Y-p.Y, pl.X-p.X), p.Ship.MaxSpeed, th)
			} else {
				g.botNavigate(p, rand.Float64()*2*math.Pi, p.Ship.MaxSpeed*7/10, th)
			}
			return
		}
	case 2: // raider
		if pl := g.botPlanetToRaid(p); pl != nil {
			if g.botGoOrbit(p, pl, th) {
				p.Bombing = true
				p.Bot.Cooldown = 30
			}
			return
		}
	}
	if enemy != nil {
		g.botEngage(p, enemy, enemyDist, th)
		return
	}
	g.botPatrol(p, th)
}

func (g *Game) botRole(p *Player) int {
	owned, hunters, defenders := 0, 0, 0
	for _, pl := range g.planets {
		if pl.Owner == p.Team {
			owned++
		}
	}
	for _, q := range g.players {
		if q == nil || q.Bot == nil || q.Team != p.Team || q.Status != "alive" {
			continue
		}
		if q.Bot.Target >= 0 {
			hunters++
		}
		if q.Bot.DefenseTarget >= 0 {
			defenders++
		}
	}
	control := float64(owned) / float64(len(g.planets))
	switch {
	case control < 0.2:
		if defenders < 2 {
			return 1
		}
		return 2
	case control > 0.6:
		return 0
	case hunters > defenders+1:
		return 1
	case p.Kills >= 2:
		return 2
	default:
		return 0
	}
}

// ---------- combat ----------

func (g *Game) botEngage(p, target *Player, dist float64, th combatThreat) {
	b := p.Bot

	// keep bombing through a distant threat (netrek-web bot_combat.go:13-37)
	if p.Orbiting >= 0 {
		pl := g.planets[p.Orbiting]
		if pl.Owner == p.Team || pl.Armies < 5 ||
			(dist < 2000 && p.Damage > p.Ship.MaxDamage/2) {
			g.breakOrbit(p)
		} else if dist > 4000 {
			p.Bombing = true
			b.Cooldown = 5
			return
		}
	}

	interceptDir := g.botInterceptCourse(p, target)
	if th.evade {
		p.DesDir = g.botDodgeDir(p, interceptDir, th)
		p.DesSpeed = g.botEvasionSpeed(p, th)
	} else {
		dir, speed := g.botManeuver(p, target, dist, interceptDir)
		sx, sy, mag := g.botSeparation(p)
		p.DesDir = blendSep(dir, sx, sy, mag, 300, 0.75)
		if mag > 2 {
			speed = speed * 7 / 10
		}
		p.DesSpeed = speed
	}

	// cloaking tactics for SC/DD (netrek-web bot_combat.go:96)
	if (p.Ship.Type == "SC" || p.Ship.Type == "DD") && p.Fuel > 3000 {
		if g.botShouldCloak(p, dist) {
			p.Cloaked = true
		} else if p.Cloaked && (p.Fuel < 1500 || dist < 1000) {
			p.Cloaked = false
		}
	}
	if p.Cloaked {
		b.Cooldown = 3
		return // can't fire while cloaked
	}

	// torps with lead prediction; spreads when the geometry favors them
	fired := false
	effRange := g.botTorpRange(p, target)
	canReach := g.botCanTorpReach(p, target)
	targetDmg := float64(target.Damage) / float64(target.Ship.MaxDamage)
	if canReach && dist < effRange && p.NTorps < MaxTorps-2 && p.Fuel > 1500 &&
		p.WTemp < p.Ship.MaxWpnTemp-100 {
		switch {
		case targetDmg > 0.7 && dist < effRange*0.6 && p.NTorps < MaxTorps-6 && p.Fuel > 2500:
			g.botStartVolley(p, target, 4) // burst to secure the kill
			b.Cooldown = 2
		case dist > effRange*0.45 && dist < effRange*0.75 && p.NTorps < MaxTorps-4:
			g.botStartVolley(p, target, 3) // mid-range spread for area denial
			b.Cooldown = 5
		default:
			g.botFireTorp(p, target)
			b.Cooldown = 3
		}
		fired = true
	}
	if !fired && canReach && dist < effRange && p.NTorps < MaxTorps-3 && p.Fuel > 1000 {
		// running target: shoot even outside normal criteria
		away := math.Abs(math.Remainder(target.Dir-math.Atan2(p.Y-target.Y, p.X-target.X), 2*math.Pi))
		if away < math.Pi/3 && target.Speed > p.Ship.MaxSpeed/2 {
			g.botFireTorp(p, target)
			b.Cooldown = 4
			fired = true
		}
	}

	// phaser to finish or at knife range (netrek-web bot_combat.go:156-176)
	if !fired {
		rangeMax := float64(PhaseDist * p.Ship.PhaserDamage / 100)
		if dist < rangeMax && p.PhaserBusy == 0 && p.Fuel >= p.Ship.PhaserCost &&
			p.WTemp < p.Ship.MaxWpnTemp-100 {
			dmg := float64(p.Ship.PhaserDamage) * (1 - dist/rangeMax)
			wouldKill := target.Damage+int(dmg) >= target.Ship.MaxDamage
			if wouldKill || targetDmg > 0.5 || dist < 1500 {
				g.firePhaser(p, math.Atan2(target.Y-p.Y, target.X-p.X))
				b.Cooldown = 5
			}
		}
	}

	// target lock bookkeeping
	if b.Target != target.ID {
		b.Target = target.ID
		b.TargetLock = targetLockTicks
		b.TargetValue = 0
	} else if b.TargetLock < 10 {
		b.TargetLock = 10
	}
	if b.Cooldown == 0 {
		b.Cooldown = 2
	}
}

func (g *Game) botManeuver(p, target *Player, dist float64, interceptDir float64) (float64, int) {
	// netrek-web selectCombatManeuver: turn-rate and speed matchups
	dir, speed := interceptDir, g.botCombatSpeed(p, dist)
	myTurn := p.Ship.Turns >> min(uint(max(p.Speed, 1)), 30)
	theirTurn := target.Ship.Turns >> min(uint(max(target.Speed, 1)), 30)
	speedAdv := p.Ship.MaxSpeed - target.Ship.MaxSpeed
	if dist < 3000 {
		if myTurn > theirTurn {
			dir = math.Atan2(target.Y-p.Y, target.X-p.X) + math.Pi/2 // circle-strafe
			speed = p.Ship.MaxSpeed * 7 / 10
		} else if speedAdv > 0 {
			dir = math.Atan2(p.Y-target.Y, p.X-target.X) // boom and zoom
			speed = p.Ship.MaxSpeed
		}
	} else if dist > 6000 && speedAdv < 0 && target.Speed > target.Ship.MaxSpeed/2 {
		dir = interceptDir + math.Pi/8
		speed = p.Ship.MaxSpeed
	}
	return dir, speed
}

func (g *Game) botDefendCarrying(p, enemy *Player, dist float64) {
	if dist < 3000 {
		p.DesDir = math.Atan2(p.Y-enemy.Y, p.X-enemy.X)
		p.DesSpeed = p.Ship.MaxSpeed
		if p.NTorps < MaxTorps && p.Fuel > 2000 {
			g.botFireTorp(p, enemy)
		}
	}
}

func (g *Game) botShouldCloak(p *Player, dist float64) bool {
	if dist < 1500 {
		return false
	}
	dmg := float64(p.Damage) / float64(p.Ship.MaxDamage)
	return (dist > 3000 && dist < 7000 && dmg < 0.2) || (dmg > 0.5 && dist > 2000)
}

// botInterceptCourse: navigation lead toward a moving target (netrek-web
// bot_types.go:15, with its unit-scaling bug corrected — see agent notes)
func (g *Game) botInterceptCourse(p, t *Player) float64 {
	d := dist2d(p.X, p.Y, t.X, t.Y)
	if d > 20000 || t.Cloaked || t.Speed < 1 {
		return math.Atan2(t.Y-p.Y, t.X-p.X)
	}
	mySpeed := float64(max(p.Speed, 2) * Warp1)
	ticks := math.Min(d/mySpeed, 15)
	vx, vy := targetVelocity(t)
	return math.Atan2(t.Y+vy*ticks-p.Y, t.X+vx*ticks-p.X)
}

// ---------- weapons ----------

// botAim solves the torpedo intercept quadratic (netrek-web intercept.go:37)
func botAim(px, py float64, t *Player, projSpeed float64) (float64, float64, bool) {
	relX, relY := t.X-px, t.Y-py
	dist := math.Hypot(relX, relY)
	if dist < 1e-6 {
		return 0, 0, false
	}
	vx, vy := targetVelocity(t)
	a := vx*vx + vy*vy - projSpeed*projSpeed
	b := 2 * (relX*vx + relY*vy)
	c := dist * dist
	var tt float64
	if math.Abs(a) < 1e-9 {
		if math.Abs(b) < 1e-9 {
			return math.Atan2(relY, relX), dist / projSpeed, true
		}
		tt = -c / b
	} else {
		disc := b*b - 4*a*c
		if disc < 0 {
			return math.Atan2(relY, relX), 0, false
		}
		r := math.Sqrt(disc)
		t1, t2 := (-b-r)/(2*a), (-b+r)/(2*a)
		tt = t1
		if tt <= 0 || (t2 > 0 && t2 < tt) {
			tt = t2
		}
	}
	if tt <= 0 {
		return math.Atan2(relY, relX), 0, false
	}
	return math.Atan2(relY+vy*tt, relX+vx*tt), tt, true
}

func targetVelocity(t *Player) (float64, float64) {
	if t.Orbiting >= 0 {
		// orbital tangential velocity: 2 direction-units per tick at radius 800
		w := 2 * ByteRad
		return w * OrbDist * math.Cos(t.Dir), w * OrbDist * math.Sin(t.Dir)
	}
	v := float64(t.Speed * Warp1)
	return v * math.Cos(t.Dir), v * math.Sin(t.Dir)
}

func (g *Game) botFireTorp(p, target *Player) {
	dir, _, _ := botAim(p.X, p.Y, target, float64(p.Ship.TorpSpeed*Warp1))
	jitter := (rand.Float64()*2 - 1) * 5 * math.Pi / 180
	g.fireTorp(p, dir+jitter)
}

func (g *Game) botStartVolley(p, target *Player, n int) {
	dir, _, ok := botAim(p.X, p.Y, target, float64(p.Ship.TorpSpeed*Warp1))
	if !ok {
		dir = math.Atan2(target.Y-p.Y, target.X-p.X)
	}
	p.Bot.VolleyLeft = n
	p.Bot.VolleyIdx = 0
	p.Bot.VolleyDir = dir
	g.botVolleyShot(p)
}

func (g *Game) botVolleyShot(p *Player) {
	b := p.Bot
	if p.NTorps >= MaxTorps || p.Fuel < p.Ship.TorpCost || p.WTemp > p.Ship.MaxWpnTemp-100 {
		b.VolleyLeft = 0
		return
	}
	count := b.VolleyIdx + b.VolleyLeft // total spread size stays constant
	offset := float64(b.VolleyIdx-count/2) * math.Pi / 16
	jitter := (rand.Float64()*2 - 1) * 5 * math.Pi / 180
	g.fireTorp(p, b.VolleyDir+offset+jitter)
	b.VolleyIdx++
	b.VolleyLeft--
}

func (g *Game) botTorpRange(p, target *Player) float64 {
	safety := map[string]float64{"SC": .65, "DD": .70, "CA": .75, "BB": .75,
		"AS": .65, "SB": .80}[p.Ship.Type]
	if safety == 0 {
		safety = .85
	}
	base := float64(p.Ship.TorpSpeed*Warp1*p.Ship.TorpFuse) * safety
	ratio := float64(target.Speed) / 12.0 // scout max speed
	if ratio > 0.9 {
		return base * 0.8
	}
	if ratio > 0.75 {
		return base * 0.9
	}
	return base
}

func (g *Game) botCanTorpReach(p, target *Player) bool {
	_, tt, ok := botAim(p.X, p.Y, target, float64(p.Ship.TorpSpeed*Warp1))
	if !ok {
		return dist2d(p.X, p.Y, target.X, target.Y) <
			float64(p.Ship.TorpSpeed*Warp1*p.Ship.TorpFuse)*0.3
	}
	safety := 0.85
	return tt <= float64(p.Ship.TorpFuse)*safety
}

// ---------- threats, dodging, navigation ----------

func (g *Game) botThreats(p *Player) combatThreat {
	th := combatThreat{closestTorp: maxSearch, closestEnemy: maxSearch}
	for _, t := range g.torps {
		if t.Team == p.Team {
			continue
		}
		d := dist2d(p.X, p.Y, t.X, t.Y)
		if d < th.closestTorp {
			th.closestTorp = d
		}
		if d < 3000 {
			th.shieldLevel += 2
		}
		if d < 2000 {
			th.shieldLevel += 5
			th.immediate = true
		}
		if g.botTorpThreatening(p, t) {
			th.evade = true
			th.threatLevel += 4
			if d < 3000 {
				th.shieldLevel += 4
				th.immediate = true
			}
			if d < 2000 {
				th.threatLevel += 3
			} else if d < 4000 {
				th.threatLevel++
			}
		}
	}
	for _, e := range g.players {
		if e == nil || e.Status != "alive" || e.Team == p.Team || e.Cloaked {
			continue
		}
		d := dist2d(p.X, p.Y, e.X, e.Y)
		if d < th.closestEnemy {
			th.closestEnemy = d
		}
		if d < 5000 {
			th.nearbyFoes++
			th.threatLevel++
			facing := math.Abs(math.Remainder(e.Dir-math.Atan2(p.Y-e.Y, p.X-e.X), 2*math.Pi))
			if d < 2000 && facing < math.Pi/6 {
				th.evade = true
				th.threatLevel += 2
			}
		}
		phRange := float64(PhaseDist * e.Ship.PhaserDamage / 100)
		if d < phRange {
			th.shieldLevel += 3
			if d < phRange*0.8 {
				th.shieldLevel += 4
				th.immediate = true
			}
		}
		if d < 1800 {
			th.shieldLevel += 3
			th.immediate = true
		} else if d < 2500 {
			th.shieldLevel += 2
			th.immediate = true
		}
	}
	return th
}

func (g *Game) botTorpThreatening(p *Player, t *Torp) bool {
	d := dist2d(p.X, p.Y, t.X, t.Y)
	if d > 5000 {
		return false
	}
	tv := float64(t.Speed * Warp1)
	tvx, tvy := tv*math.Cos(t.Dir), tv*math.Sin(t.Dir)
	pv := float64(p.Speed * Warp1)
	pvx, pvy := pv*math.Cos(p.Dir), pv*math.Sin(p.Dir)
	for step := 0.0; step < 5; step += 0.2 {
		if math.Hypot((p.X+pvx*step)-(t.X+tvx*step), (p.Y+pvy*step)-(t.Y+tvy*step)) < 800 {
			return true
		}
	}
	heading := math.Abs(math.Remainder(math.Atan2(p.Y-t.Y, p.X-t.X)-t.Dir, 2*math.Pi))
	return (heading < math.Pi/4 && d < 4000) || d < 1500
}

func (g *Game) botShields(p *Player, th combatThreat) {
	if p.Fuel < botFuelCritical {
		p.ShieldsUp = false
		return
	}
	up := false
	switch {
	case th.immediate && p.Fuel > botFuelLow:
		up = true
	case th.shieldLevel >= 6 && p.Fuel > botFuelModerate:
		up = true
	case th.shieldLevel >= 3 && p.Fuel > botFuelGood:
		up = true
	case th.closestTorp < 2000 && p.Fuel > botFuelLow:
		up = true
	case th.closestEnemy < 2500 && p.Fuel > botFuelModerate:
		up = true
	}
	if p.Armies > 0 && (th.closestEnemy < 3500 || th.closestTorp < 3000) && p.Fuel > botFuelLow {
		up = true
	}
	if p.Bot.DefenseTarget >= 0 && (th.closestEnemy < 3000 || th.closestTorp < 2000) && p.Fuel > botFuelLow {
		up = true
	}
	p.ShieldsUp = up
}

// botNavigate: dodge if needed, else blend course with ally separation
func (g *Game) botNavigate(p *Player, dir float64, speed int, th combatThreat) {
	if p.Orbiting >= 0 {
		g.breakOrbit(p)
	}
	if th.evade {
		p.DesDir = g.botDodgeDir(p, dir, th)
		p.DesSpeed = g.botEvasionSpeed(p, th)
		p.Bot.Cooldown = 2
		return
	}
	sx, sy, mag := g.botSeparation(p)
	p.DesDir = blendSep(dir, sx, sy, mag, 300, 0.5)
	p.DesSpeed = speed
	if th.closestTorp < 3000 && p.DesSpeed < p.Ship.MaxSpeed*8/10 {
		p.DesSpeed = min(p.DesSpeed*12/10+1, p.Ship.MaxSpeed)
	}
}

func (g *Game) botDodgeDir(p *Player, want float64, th combatThreat) float64 {
	best, bestScore := p.Dir, -maxSearch
	for i := 0; i < 12; i++ {
		delta := float64(i) * math.Pi / 12
		for _, sign := range []float64{1, -1} {
			if delta == 0 && sign < 0 {
				continue
			}
			dir := want + sign*delta
			score := -g.botTorpDanger(p, dir)*10 -
				math.Abs(math.Remainder(dir-want, 2*math.Pi))*100
			// clearance from walls and planet defense zones
			probeX := p.X + 5000*math.Cos(dir)
			probeY := p.Y + 5000*math.Sin(dir)
			clr := math.Min(math.Min(probeX, GWidth-probeX), math.Min(probeY, GWidth-probeY))
			for _, pl := range g.planets {
				if pl.Owner != p.Team && pl.Owner != TeamNone &&
					dist2d(probeX, probeY, pl.X, pl.Y) < 2000 {
					clr -= 2000 - dist2d(probeX, probeY, pl.X, pl.Y)
				}
			}
			if clr < 3000 {
				score -= (3000 - clr) * 2
			}
			if score > bestScore {
				best, bestScore = dir, score
			}
		}
	}
	return best
}

func (g *Game) botTorpDanger(p *Player, dir float64) float64 {
	danger := 0.0
	v := float64(max(p.Speed, 2) * Warp1)
	vx, vy := v*math.Cos(dir), v*math.Sin(dir)
	for _, t := range g.torps {
		if t.Team == p.Team || dist2d(p.X, p.Y, t.X, t.Y) > 6000 {
			continue
		}
		tv := float64(t.Speed * Warp1)
		tvx, tvy := tv*math.Cos(t.Dir), tv*math.Sin(t.Dir)
		for step := 0.0; step <= 3; step += 0.5 {
			sep := math.Hypot((p.X+vx*step)-(t.X+tvx*step), (p.Y+vy*step)-(t.Y+tvy*step))
			if sep < 700 {
				danger += (700 - sep) / 100
			}
		}
	}
	return danger
}

func (g *Game) botEvasionSpeed(p *Player, th combatThreat) int {
	if th.threatLevel > 5 {
		return p.Ship.MaxSpeed
	}
	if th.threatLevel > 2 {
		return p.Ship.MaxSpeed * (6 + rand.Intn(5)) / 10
	}
	return g.botCombatSpeed(p, 3000)
}

func (g *Game) botApproachSpeed(p *Player, dist float64) int {
	if dist < 1500 {
		return OrbSpeed // arrive slow enough to orbit
	}
	v := int(math.Sqrt((dist - 200) * float64(p.Ship.DecInt) / 11500))
	return max(2, min(v, p.Ship.MaxSpeed))
}

func (g *Game) botCombatSpeed(p *Player, dist float64) int {
	m := p.Ship.MaxSpeed
	switch {
	case dist > 6000:
		return m
	case dist > 3000:
		return m * 6 / 10
	case dist > 1500:
		return m * 4 / 10
	default:
		return max(m*3/10, 2)
	}
}

func (g *Game) botSeparation(p *Player) (float64, float64, float64) {
	var x, y float64
	count := 0
	for _, a := range g.players {
		if a == nil || a == p || a.Team != p.Team || a.Status != "alive" || a.Orbiting >= 0 {
			continue
		}
		d := dist2d(p.X, p.Y, a.X, a.Y)
		if d <= 0 || d >= sepMinSafe {
			continue
		}
		var strength float64
		switch {
		case d < sepCritical:
			strength = 5 * (sepCritical - d) / sepCritical
		case d < sepIdeal:
			strength = 2 * (sepIdeal - d) / sepIdeal
		default:
			strength = 0.8 * (sepMinSafe - d) / sepMinSafe
		}
		if a.Bot != nil && p.Bot != nil && a.Bot.Target >= 0 && a.Bot.Target == p.Bot.Target {
			strength *= 1.8
		}
		dmg := float64(a.Damage) / float64(a.Ship.MaxDamage)
		if dmg > 0.5 {
			strength *= 2
		} else if dmg > 0.3 {
			strength *= 1.5
		}
		x += (p.X - a.X) / d * strength
		y += (p.Y - a.Y) / d * strength
		count++
	}
	if count == 0 {
		return 0, 0, 0
	}
	scale := math.Min(1+float64(count)*0.3, 3)
	x, y = x*scale, y*scale
	mag := math.Hypot(x, y)
	if mag > 0 {
		x, y = x/mag, y/mag
	}
	return x, y, mag
}

func blendSep(base, sx, sy, mag, divisor, maxW float64) float64 {
	if mag <= 0 {
		return base
	}
	w := math.Min(mag/divisor, maxW)
	return math.Atan2(math.Sin(base)*(1-w)+sy*w, math.Cos(base)*(1-w)+sx*w)
}

// botGoOrbit navigates to a planet and orbits on arrival; true once orbiting.
func (g *Game) botGoOrbit(p *Player, pl *Planet, th combatThreat) bool {
	if p.Orbiting == pl.N {
		return true
	}
	d := dist2d(p.X, p.Y, pl.X, pl.Y)
	if d <= EntOrbDist && p.Speed <= OrbSpeed {
		g.enterOrbit(p)
		return p.Orbiting == pl.N
	}
	g.botNavigate(p, math.Atan2(pl.Y-p.Y, pl.X-p.X), g.botApproachSpeed(p, d), th)
	return false
}

// ---------- planets ----------

func (g *Game) botNearestPlanet(p *Player, ok func(*Planet) bool) *Planet {
	var best *Planet
	bestD := maxSearch
	for _, pl := range g.planets {
		if !ok(pl) {
			continue
		}
		if d := dist2d(p.X, p.Y, pl.X, pl.Y); d < bestD {
			best, bestD = pl, d
		}
	}
	return best
}

func (g *Game) botBestTakePlanet(p *Player) *Planet {
	var best *Planet
	bestScore := -maxSearch
	for _, pl := range g.planets {
		if pl.Owner == p.Team || g.thirdSpace(pl) {
			continue
		}
		d := dist2d(p.X, p.Y, pl.X, pl.Y)
		if d > 30000 {
			continue
		}
		score := 15000 / math.Max(d, 1)
		if p.Armies > pl.Armies {
			score += 3000
		} else if p.Armies == 0 && pl.Armies < 5 {
			score += 2000 - float64(pl.Armies)*200
		}
		if pl.Flags&PlAgri != 0 {
			score += 2000
		}
		count, _, defScore, _, _ := g.planetDefenders(pl, p.Team)
		score -= defScore * 0.8
		allies := g.botAlliesNear(p, 10000)
		if count >= 2 && allies == 0 {
			score -= 5000
		}
		score += float64(allies) * 300
		if score > bestScore {
			best, bestScore = pl, score
		}
	}
	return best
}

func (g *Game) botPlanetToDefend(p *Player) *Planet {
	var best *Planet
	bestScore := 0.0
	for _, pl := range g.planets {
		if pl.Owner != p.Team {
			continue
		}
		threat := 0.0
		for _, e := range g.players {
			if e == nil || e.Status != "alive" || e.Team == p.Team || e.Cloaked {
				continue
			}
			d := dist2d(e.X, e.Y, pl.X, pl.Y)
			if d < 10000 {
				threat += (10000 - d) / 1000
				if e.Armies > 0 {
					threat += 5
				}
			}
		}
		if threat <= 0 {
			continue
		}
		score := threat*1000 - dist2d(p.X, p.Y, pl.X, pl.Y)/10
		if pl.Flags&PlAgri != 0 {
			score += 500
		}
		if pl.Flags&PlRepair != 0 {
			score += 300
		}
		if score > bestScore {
			best, bestScore = pl, score
		}
	}
	return best
}

func (g *Game) botPlanetToRaid(p *Player) *Planet {
	var best *Planet
	bestScore := -maxSearch
	for _, pl := range g.planets {
		if pl.Owner == p.Team || pl.Owner == TeamNone || pl.Armies < 5 || g.thirdSpace(pl) {
			continue
		}
		d := dist2d(p.X, p.Y, pl.X, pl.Y)
		if d > 20000 {
			continue
		}
		count, _, _, _, _ := g.planetDefenders(pl, p.Team)
		if count > 0 {
			continue
		}
		score := 10000/math.Max(d, 1) + float64(pl.Armies)*500
		if score > bestScore {
			best, bestScore = pl, score
		}
	}
	return best
}

// planetDefenders: enemies of `team` near the planet (netrek-web bot_planet.go:158)
func (g *Game) planetDefenders(pl *Planet, team int) (int, float64, float64, *Player, *Player) {
	count, minDist := 0, maxSearch
	var closest, carrier *Player
	for _, e := range g.players {
		if e == nil || e.Status != "alive" || e.Team == team || e.Cloaked {
			continue
		}
		d := dist2d(e.X, e.Y, pl.X, pl.Y)
		if d > 10000 {
			continue
		}
		count++
		if d < minDist {
			minDist, closest = d, e
		}
		if e.Armies > 0 && carrier == nil {
			carrier = e
		}
	}
	score := float64(count) * 1000
	if count > 0 {
		score += (10000 - minDist) * 0.15
	}
	if carrier != nil {
		score += 2000
	}
	return count, minDist, score, closest, carrier
}

// threatenedPlanet: most-threatened friendly planet near the bot
func (g *Game) threatenedPlanet(p *Player) (*Planet, *Player, float64) {
	var bestPl *Planet
	var bestEnemy *Player
	bestScore := 0.0
	for _, pl := range g.planets {
		if pl.Owner != p.Team || dist2d(p.X, p.Y, pl.X, pl.Y) > planetDefenseRadius {
			continue
		}
		score := 0.0
		var closest *Player
		closestD := maxSearch
		for _, e := range g.players {
			if e == nil || e.Status != "alive" || e.Team == p.Team || e.Cloaked {
				continue
			}
			d := dist2d(e.X, e.Y, pl.X, pl.Y)
			var s float64
			switch {
			case d < 5000:
				s = (5000 - d) * 0.1
			case e.Speed > 1 && d < 12000 &&
				math.Abs(math.Remainder(e.Dir-math.Atan2(pl.Y-e.Y, pl.X-e.X), 2*math.Pi)) < math.Pi/4:
				s = (12000 - d) * 0.05
			default:
				continue
			}
			if e.Armies > 0 {
				s += float64(e.Armies) * 2
			}
			score += s
			if d < closestD {
				closestD, closest = d, e
			}
		}
		if score > bestScore && closest != nil {
			bestPl, bestEnemy, bestScore = pl, closest, score
		}
	}
	if bestPl == nil {
		return nil, nil, 0
	}
	return bestPl, bestEnemy, dist2d(p.X, p.Y, bestEnemy.X, bestEnemy.Y)
}

func (g *Game) botDefendPlanet(p *Player, pl *Planet, enemy *Player, dist float64) {
	b := p.Bot
	b.DefenseTarget = pl.N
	g.breakOrbit(p)
	p.Orbiting = -1

	// meet the attacker between him and the planet
	optIntercept := 4000.0
	if dist < 6000 {
		optIntercept = 3500
	}
	toPlanet := math.Atan2(pl.Y-enemy.Y, pl.X-enemy.X)
	ix := enemy.X + math.Cos(toPlanet)*optIntercept*0.7
	iy := enemy.Y + math.Sin(toPlanet)*optIntercept*0.7
	th := g.botThreats(p)
	if dist2d(p.X, p.Y, ix, iy) > 1500 || dist > 6000 {
		speed := p.Ship.MaxSpeed
		if dist <= 6000 {
			speed = g.botCombatSpeed(p, dist)
		}
		g.botNavigate(p, math.Atan2(iy-p.Y, ix-p.X), speed, th)
	} else {
		if dist < 2000 {
			p.DesDir = math.Atan2(enemy.Y-p.Y, enemy.X-p.X) + math.Pi/2
		} else {
			p.DesDir = math.Atan2(enemy.Y-p.Y, enemy.X-p.X)
		}
		p.DesSpeed = g.botCombatSpeed(p, dist)
	}

	// defense weapons (netrek-web bot_weapons.go:414)
	if g.botCanTorpReach(p, enemy) && dist < g.botTorpRange(p, enemy) &&
		p.NTorps < 7 && p.Fuel > 1500 {
		g.botFireTorp(p, enemy)
		b.Cooldown = 4
	} else if dist < float64(PhaseDist*p.Ship.PhaserDamage/100) && p.Fuel > 1000 &&
		p.PhaserBusy == 0 {
		g.firePhaser(p, math.Atan2(enemy.Y-p.Y, enemy.X-p.X))
		b.Cooldown = 8
	}
	if b.Cooldown == 0 {
		b.Cooldown = 2
	}
}

// ---------- targets, patrol ----------

func (g *Game) botNearestEnemy(p *Player) *Player {
	var best *Player
	bestD := maxSearch
	for _, e := range g.players {
		if e == nil || e.Status != "alive" || e.Team == p.Team || e.Cloaked {
			continue
		}
		if d := dist2d(p.X, p.Y, e.X, e.Y); d < bestD {
			best, bestD = e, d
		}
	}
	return best
}

func (g *Game) botBestTarget(p *Player) *Player {
	b := p.Bot
	var best *Player
	bestScore := -maxSearch
	locked := false
	if b.Target >= 0 && b.TargetLock > 0 {
		b.TargetLock--
		cur := g.players[b.Target]
		if cur != nil && cur.Status == "alive" && cur.Team != p.Team && !cur.Cloaked &&
			dist2d(p.X, p.Y, cur.X, cur.Y) < 30000 {
			best = cur
			bestScore = g.botTargetScore(p, cur) + targetPersistenceBonus
			locked = true
		} else {
			b.Target = -1
			b.TargetLock = 0
		}
	}
	for _, e := range g.players {
		if e == nil || e.Status != "alive" || e.Team == p.Team || e.Cloaked || e == best {
			continue
		}
		if dist2d(p.X, p.Y, e.X, e.Y) > 25000 {
			continue
		}
		score := g.botTargetScore(p, e)
		better := score > bestScore
		if locked {
			better = score > bestScore+math.Abs(bestScore)*0.2
		}
		if better {
			best, bestScore = e, score
		}
	}
	if best != nil && b.Target != best.ID {
		b.Target = best.ID
		b.TargetLock = targetLockTicks
		b.TargetValue = bestScore
	}
	return best
}

func (g *Game) botTargetScore(p, t *Player) float64 {
	d := math.Max(dist2d(p.X, p.Y, t.X, t.Y), 1)
	score := 20000 / d
	dmg := float64(t.Damage) / float64(t.Ship.MaxDamage)
	switch {
	case dmg > 0.8:
		score += 8000
	case dmg > 0.5:
		score += dmg * 5000
	default:
		score += dmg * 3000
	}
	if t.Armies > 0 {
		score += 10000 + float64(t.Armies)*1500
	}
	if diff := p.Ship.MaxSpeed - t.Ship.MaxSpeed; diff > 0 {
		score += float64(diff) * 300
	}
	isolated := true
	for _, a := range g.players {
		if a != nil && a != t && a.Team == t.Team && a.Status == "alive" &&
			dist2d(a.X, a.Y, t.X, t.Y) < 5000 {
			isolated = false
			break
		}
	}
	if isolated {
		score += 2000
	}
	return score
}

func (g *Game) botAlliesNear(p *Player, radius float64) int {
	n := 0
	for _, a := range g.players {
		if a != nil && a != p && a.Team == p.Team && a.Status == "alive" &&
			dist2d(p.X, p.Y, a.X, a.Y) < radius {
			n++
		}
	}
	return n
}

func (g *Game) botPatrol(p *Player, th combatThreat) {
	b := p.Bot
	if b.GoalX == 0 && b.GoalY == 0 || dist2d(p.X, p.Y, b.GoalX, b.GoalY) < 3000 {
		// new destination: our space when weak, frontline planet otherwise
		owned := 0
		for _, pl := range g.planets {
			if pl.Owner == p.Team {
				owned++
			}
		}
		var base *Planet
		if float64(owned)/float64(len(g.planets)) < 0.3 {
			base = g.planets[p.Team*10] // home planet
		} else {
			candidates := []*Planet{}
			for _, pl := range g.planets {
				if pl.Owner == p.Team {
					candidates = append(candidates, pl)
				}
			}
			if len(candidates) > 0 {
				base = candidates[rand.Intn(len(candidates))]
			} else {
				base = g.planets[p.Team*10]
			}
		}
		b.GoalX = math.Max(5000, math.Min(GWidth-5000, base.X+float64(rand.Intn(15000)-7500)))
		b.GoalY = math.Max(5000, math.Min(GWidth-5000, base.Y+float64(rand.Intn(15000)-7500)))
	}
	g.botNavigate(p, math.Atan2(b.GoalY-p.Y, b.GoalX-p.X), p.Ship.MaxSpeed*8/10, th)
	b.Cooldown = 10
}

func (g *Game) botSafeArea(p *Player) {
	var cx, cy float64
	n := 0
	for _, pl := range g.planets {
		if pl.Owner == p.Team {
			cx += pl.X
			cy += pl.Y
			n++
		}
	}
	b := p.Bot
	if n == 0 {
		p.DesSpeed = 0
		b.Cooldown = 20
		return
	}
	angle := float64(p.ID) * 0.5
	cx = cx/float64(n) + 3000*math.Cos(angle)
	cy = cy/float64(n) + 3000*math.Sin(angle)
	if dist2d(p.X, p.Y, cx, cy) > 1000 {
		th := g.botThreats(p)
		g.botNavigate(p, math.Atan2(cy-p.Y, cx-p.X), p.Ship.MaxSpeed/2, th)
	} else {
		p.DesSpeed = 2
		p.DesDir = p.Dir + 0.1
	}
	b.Cooldown = 10
}
