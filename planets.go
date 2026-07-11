package main

import "math/rand"

// Planet layout verbatim from netrek-server ntserv/planet.c virginal[],
// resource randomization from pl_reset() in the same file.

const (
	TeamFed = iota
	TeamRom
	TeamKli
	TeamOri
	TeamNone = -1
)

var teamLetters = [4]string{"F", "R", "K", "O"}

const (
	PlRepair = 1 << iota
	PlFuel
	PlAgri
	PlHome
	PlCore
)

type Planet struct {
	N      int
	Name   string
	X, Y   float64
	Owner  int
	Armies int
	Flags  int
}

type virginPlanet struct {
	name  string
	x, y  float64
	owner int
	flags int
}

const homeFlags = PlHome | PlCore | PlRepair | PlFuel

var virginal = [40]virginPlanet{
	{"Earth", 20000, 80000, TeamFed, homeFlags},
	{"Rigel", 10000, 60000, TeamFed, 0},
	{"Canopus", 25000, 60000, TeamFed, 0},
	{"Beta Crucis", 44000, 81000, TeamFed, 0},
	{"Organia", 39000, 55000, TeamFed, 0},
	{"Deneb", 30000, 90000, TeamFed, PlCore},
	{"Ceti Alpha V", 45000, 66000, TeamFed, 0},
	{"Altair", 11000, 75000, TeamFed, PlCore},
	{"Vega", 8000, 93000, TeamFed, PlCore},
	{"Alpha Centauri", 32000, 74000, TeamFed, 0},
	{"Romulus", 20000, 20000, TeamRom, homeFlags},
	{"Eridani", 45000, 7000, TeamRom, 0},
	{"Aldeberan", 4000, 12000, TeamRom, PlCore},
	{"Regulus", 42000, 44000, TeamRom, 0},
	{"Capella", 13000, 45000, TeamRom, 0},
	{"Tauri", 28000, 8000, TeamRom, PlCore},
	{"Draconis", 28000, 23000, TeamRom, PlCore},
	{"Sirius", 40000, 25000, TeamRom, 0},
	{"Indi", 25000, 44000, TeamRom, 0},
	{"Hydrae", 8000, 29000, TeamRom, 0},
	{"Klingus", 80000, 20000, TeamKli, homeFlags},
	{"Pliedes V", 70000, 40000, TeamKli, 0},
	{"Andromeda", 60000, 10000, TeamKli, 0},
	{"Lalande", 56400, 38200, TeamKli, 0},
	{"Praxis", 91120, 9320, TeamKli, PlCore},
	{"Lyrae", 89960, 31760, TeamKli, 0},
	{"Scorpii", 70720, 26320, TeamKli, PlCore},
	{"Mira", 83600, 45400, TeamKli, 0},
	{"Cygni", 54600, 22600, TeamKli, 0},
	{"Achernar", 73080, 6640, TeamKli, PlCore},
	{"Orion", 80000, 80000, TeamOri, homeFlags},
	{"Cassiopeia", 91200, 56600, TeamOri, 0},
	{"El Nath", 70800, 54200, TeamOri, 0},
	{"Spica", 57400, 62600, TeamOri, 0},
	{"Procyon", 72720, 70880, TeamOri, PlCore},
	{"Polaris", 61400, 77000, TeamOri, 0},
	{"Arcturus", 55600, 89000, TeamOri, 0},
	{"Ursae Majoris", 91000, 94000, TeamOri, PlCore},
	{"Herculis", 70000, 93000, TeamOri, PlCore},
	{"Antares", 86920, 68920, TeamOri, PlCore},
}

// per-quadrant planet groups from planet.c
var corePlanets = [4][4]int{
	{7, 9, 5, 8}, {12, 19, 15, 16}, {24, 29, 25, 26}, {34, 39, 38, 37},
}
var frontPlanets = [4][5]int{
	{1, 2, 4, 6, 3}, {14, 18, 13, 17, 11}, {22, 28, 23, 21, 27}, {31, 32, 33, 35, 36},
}

const topArmies = 30 // initial army count, ntserv/data.c

// resetPlanets rebuilds the galaxy: virginal layout, 30 armies each, and the
// Vanilla pl_reset() randomized AGRI/REPAIR/FUEL distribution per quadrant.
func resetPlanets() []*Planet {
	pls := make([]*Planet, 40)
	for i, v := range virginal {
		pls[i] = &Planet{N: i, Name: v.name, X: v.x, Y: v.y, Owner: v.owner,
			Armies: topArmies, Flags: v.flags}
	}
	for i := 0; i < 4; i++ {
		core, front := corePlanets[i], frontPlanets[i]
		pls[core[rand.Intn(4)]].Flags |= PlAgri
		if rand.Intn(2) == 1 {
			pls[front[rand.Intn(2)]].Flags |= PlAgri
			pls[front[rand.Intn(3)+2]].Flags |= PlRepair
			for j := 0; j < 2; j++ {
				k := rand.Intn(3)
				for pls[front[k+2]].Flags&PlFuel != 0 {
					k = (k + 1) % 3
				}
				pls[front[k+2]].Flags |= PlFuel
			}
		} else {
			pls[front[rand.Intn(2)+3]].Flags |= PlAgri
			pls[front[rand.Intn(3)]].Flags |= PlRepair
			for j := 0; j < 2; j++ {
				k := rand.Intn(3)
				for pls[front[k]].Flags&PlFuel != 0 {
					k = (k + 1) % 3
				}
				pls[front[k]].Flags |= PlFuel
			}
		}
		pls[core[rand.Intn(4)]].Flags |= PlRepair
		for j := 0; j < 2; j++ {
			k := rand.Intn(4)
			for pls[core[k]].Flags&PlFuel != 0 {
				k = (k + 1) % 4
			}
			pls[core[k]].Flags |= PlFuel
		}
	}
	return pls
}
