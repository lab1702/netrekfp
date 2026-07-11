# netrekfp — first-person 3D Netrek

2026-07-10. Autonomous-session design (user not available for Q&A); decisions below follow classic
Vanilla netrek behavior wherever the request didn't specify.

## Concept

Classic Netrek rules on the classic flat 100000×100000 galaxy, but the player sees a 3D cockpit
view out of the ship instead of a 2D tactical map. A galactic map overlays on demand (`m`).
Planets are 3D spheres that shrink with distance to a dot, then disappear. Universe stays 2D
(all objects on the z=0 plane); only the rendering is 3D.

## Architecture

- **Server: Go**, one binary. Serves the static client over HTTP and a WebSocket at `/ws`.
  Single game-loop goroutine at 10 Hz (netrek's 100 ms update); per-connection reader goroutines
  push inputs into the loop via channel. State lives in memory only — nothing persisted.
- **Client: plain JavaScript + raw WebGL1**, no libraries, no build step. Three files:
  `index.html`, `game.js` (net/input/HUD/galactic map), `gl.js` (3D renderer).
- **Dependency:** `gorilla/websocket` only (stdlib has no websocket).

## Netrek data (verbatim from quozl/netrek-server)

- 40 planets — names, coordinates, flags (HOME/CORE/REPAIR/FUEL), 17 starting armies — from
  `ntserv/planet.c` `virginal[]`. AGRI/extra FUEL/REPAIR flags randomized per-quadrant at galaxy
  reset, as Vanilla does.
- Ship stats for SC, DD, CA, BB, AS, SB, GA from `ntserv/getship.c` (turns, accint/decint,
  maxspeed, fuel, recharge, warpcost, torp/phaser damage-speed-fuse-cost, maxarmies,
  maxshield/maxdamage, repair, weapon/engine temp, mass).
- Movement/combat constants from `include/defs.h`: GWIDTH 100000, WARP1 20, EXPDIST 350,
  DETDIST 1700, PHASEDIST 6000, ORBDIST 800, ORBSPEED 2, MAXTORP 8, update 100 ms.
- Mechanics formulas (turn rate vs speed, accel, fuel, damage falloff, bombing/beaming rates,
  army growth, explosion damage) extracted from `ntserv/daemonII.c` et al.

## Rules in scope

Ship types, course/speed, shields, phasers (falloff to PHASEDIST), torps (max 8, fuse, EXPDIST
proximity + det), damage/shield model, repair (+repair planets), fuel (+fuel planets), engine and
weapon temp, orbit, bombing, army beaming (kills×2 carry cap), planet capture
(enemy→independent→yours), agri growth, cloak (fuel drain, hidden from enemies), det own/enemy
torps, ship explosion splash damage, kill credit, respawn at home area.

**T-mode:** starts when ≥2 teams each have ≥4 players; runs 30 minutes or until fewer than 2
teams have 4+; on end, stats discarded and galaxy reset to the virginal layout.

**Genocide:** taking the last planet of a team that has players wipes that team (all its ships
explode, per Vanilla checkgen) and ends the round immediately — announcement, stats discarded,
galaxy reset. Deviation from Vanilla: stock netrek plays on toward quadrant conquer (VICTORY=3);
here genocide of a populated team is itself the ending. Wiping a playerless team just neutralizes
its planets.

**Caps:** 128 players total, 32 per team (FED/ROM/KLI/ORI), enforced at join.

**Cut (not requested / add later):** plasma torps, tractors/pressors, starbase docking &
refit/transwarp, observers, quadrant-conquer ending, surrender/coup timers, UDP protocol,
per-client visibility culling, bots, persistence of any kind.

## 3D view

- Camera at own ship position, eye slightly above the plane, yawed to ship heading; mouse moves a
  reticle; the reticle's bearing (ray→plane intersection) is the aim/course direction.
- Planets: spheres (radius ~600, so ORBDIST 800 orbits skim the surface), lit, team-colored with
  name label; clamp to a ≥2 px dot when small; alpha-fade out between 18000 and 25000 units.
- Ships: simple colored hull shapes oriented to heading, name labels; torps: glowing points;
  phasers: beam lines that fade; starfield skybox for orientation.
- HUD (DOM overlay): speed/heading, shields, hull, fuel, temps, torps, armies, kills, alert
  status (green/yellow/red by enemy proximity), orbiting-planet panel, message log.
- Galactic map (`m`): full-screen 2D canvas, all planets w/ owner colors + army counts,
  all non-cloaked ships, own heading wedge.

## Controls (netrek-style)

Mouse: **left = torp, middle = phaser, right = set course** (toward reticle). Keys: `0-9` warp,
`=` max warp, `s` shields, `t` torp, `p` phaser, `o` orbit, `b` bomb, `z` beam up, `x` beam down,
`R` repair, `c` cloak, `d` det enemy torps, `m` map, `Esc` quit to team select.

## Protocol (JSON over WS)

- C→S: `join{name,team,ship}`, `course{dir}`, `speed{n}`, `torp{dir}`, `phaser{dir}`, and
  toggles `shields|orbit|bomb|beamup|beamdown|repair|cloak|det|quit`.
- S→C: `welcome{id,planets,ships}`, then 10 Hz `snap{you, players[], torps[], phasers[],
  planets[], tmode{on,left}, msgs[]}` — full state every tick (~6 KB); fine for 128 players on
  loopback/LAN. ponytail: delta/interest culling is the upgrade path if bandwidth matters.

## Testing

`go test`: in-process game-logic checks — t-mode trigger/expiry/reset, phaser falloff, torp hit,
planet capture sequence, join caps. Manual: live browser session against the running server.
