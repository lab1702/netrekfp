# netrekfp

Netrek from the cockpit: classic Netrek rules on the classic flat 100000×100000 galaxy, but
instead of a 2D tactical view you look out of your ship at 3D planets and ships floating in
space. Planets are huge up close, shrink to a dot as you fly away, and vanish past ~25k units.
Press `m` for the galactic map.

Planet names/positions, ship stats, and combat formulas are taken verbatim from the Vanilla
netrek server source (`quozl/netrek-server`).

## Run

```
go run .
```

or with Docker:

```
docker compose up -d
```

then open http://localhost:9701 (WebGL required). Up to 128 players, 32 per team.

**Bots:** the join screen has bot controls — `+F/+R/+K/+O` add a bot to a team, `−` removes one,
`BALANCE` tops up the two most-populated teams to 4v4 (T-mode-ready in one click), `CLEAR` removes
all bots. Bot AI is modeled on [lab1702/netrek-web](https://github.com/lab1702/netrek-web): threat
assessment, torpedo dodging, lead-aimed torps and spreads, target scoring, planet defense, and a
full T-mode planet game (bomb, pick up, take). Bots count toward T-mode player counts.

## Rules

Standard Bronco netrek at 10 Hz: SC/DD/CA/BB/AS/SB/GA ship classes, phasers with range falloff,
max 8 torps with proximity fuses, shields, weapon/engine overheat, repair mode, fuel/repair/agri
planets, orbit-bomb-beam army play, planet capture (enemy → independent → yours), cloak, det.

**T-mode** starts when ≥2 teams each have ≥4 players and runs 30 minutes, or until fewer than
2 teams have 4+. **Genocide** ends the round early: take a populated team's last planet and its
remaining ships are destroyed, the win is announced, and the galaxy resets (T-mode restarts if
enough players remain). Either way, stats are discarded when a round ends. No database, nothing
persists.

## Controls

| Input | Action |
|---|---|
| right-click | set course toward pointer |
| left-click / `t` | fire torpedo toward pointer |
| middle-click / `p` | fire phaser toward pointer |
| `0`–`9`, `=` | warp speed (= is max) |
| `s` | shields |
| `o` | orbit (warp ≤ 2, near planet) |
| `b` | bomb (orbiting enemy planet) |
| `z` / `x` | beam armies up / down |
| `c` | cloak |
| `d` | det enemy torps |
| `R` | repair mode |
| `m` | galactic map |
| Esc | quit ship |

You need kills to carry armies (2 per kill, 3 per kill in an Assault ship).
