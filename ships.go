package main

// Ship stats verbatim from netrek-server ntserv/getship.c (Vanilla, non-chaos).
// Plasma, tractor and dimension fields omitted — out of scope.

type ShipStats struct {
	Type         string
	Turns        int // turn-rate accumulator units (see subdir formula in game.go)
	AccInt       int // acceleration per update, in subspeed units
	DecInt       int
	TorpDamage   int
	TorpSpeed    int // warp
	TorpFuse     int // updates
	PhaserFuse   int // updates of weapon recharge per shot
	PhaserDamage int
	MaxSpeed     int
	Repair       int // subdamage repair units per update
	MaxFuel      int
	DetCost      int
	TorpCost     int
	PhaserCost   int
	WarpCost     int // fuel per update per warp
	CloakCost    int // fuel per update
	Recharge     int // fuel subunits per update
	MaxArmies    int
	MaxShield    int
	MaxDamage    int
	WpnCool      int
	EgnCool      int
	MaxWpnTemp   int
	MaxEgnTemp   int
	Mass         int
}

var shipTypes = map[string]*ShipStats{
	"SC": {Type: "SC", Turns: 570000, AccInt: 200, DecInt: 270, TorpDamage: 25,
		TorpSpeed: 16, TorpFuse: 16, PhaserFuse: 10, PhaserDamage: 75, MaxSpeed: 12,
		Repair: 80, MaxFuel: 5000, DetCost: 100, TorpCost: 175, PhaserCost: 525,
		WarpCost: 2, CloakCost: 17, Recharge: 8, MaxArmies: 2, MaxShield: 75,
		MaxDamage: 75, WpnCool: 3, EgnCool: 8, MaxWpnTemp: 1000, MaxEgnTemp: 1000,
		Mass: 1500},
	"DD": {Type: "DD", Turns: 310000, AccInt: 200, DecInt: 300, TorpDamage: 30,
		TorpSpeed: 14, TorpFuse: 30, PhaserFuse: 10, PhaserDamage: 85, MaxSpeed: 10,
		Repair: 100, MaxFuel: 7000, DetCost: 100, TorpCost: 210, PhaserCost: 595,
		WarpCost: 3, CloakCost: 21, Recharge: 11, MaxArmies: 5, MaxShield: 85,
		MaxDamage: 85, WpnCool: 2, EgnCool: 7, MaxWpnTemp: 1000, MaxEgnTemp: 1000,
		Mass: 1800},
	"CA": {Type: "CA", Turns: 170000, AccInt: 150, DecInt: 200, TorpDamage: 40,
		TorpSpeed: 12, TorpFuse: 40, PhaserFuse: 10, PhaserDamage: 100, MaxSpeed: 9,
		Repair: 110, MaxFuel: 10000, DetCost: 100, TorpCost: 280, PhaserCost: 700,
		WarpCost: 4, CloakCost: 26, Recharge: 12, MaxArmies: 10, MaxShield: 100,
		MaxDamage: 100, WpnCool: 2, EgnCool: 6, MaxWpnTemp: 1000, MaxEgnTemp: 1000,
		Mass: 2000},
	"BB": {Type: "BB", Turns: 75000, AccInt: 80, DecInt: 180, TorpDamage: 40,
		TorpSpeed: 12, TorpFuse: 40, PhaserFuse: 10, PhaserDamage: 105, MaxSpeed: 8,
		Repair: 125, MaxFuel: 14000, DetCost: 100, TorpCost: 360, PhaserCost: 1050,
		WarpCost: 6, CloakCost: 30, Recharge: 14, MaxArmies: 6, MaxShield: 130,
		MaxDamage: 130, WpnCool: 3, EgnCool: 6, MaxWpnTemp: 1000, MaxEgnTemp: 1000,
		Mass: 2300},
	"AS": {Type: "AS", Turns: 120000, AccInt: 100, DecInt: 200, TorpDamage: 30,
		TorpSpeed: 16, TorpFuse: 30, PhaserFuse: 10, PhaserDamage: 80, MaxSpeed: 8,
		Repair: 120, MaxFuel: 6000, DetCost: 100, TorpCost: 270, PhaserCost: 560,
		WarpCost: 3, CloakCost: 17, Recharge: 10, MaxArmies: 20, MaxShield: 80,
		MaxDamage: 200, WpnCool: 2, EgnCool: 6, MaxWpnTemp: 1000, MaxEgnTemp: 1200,
		Mass: 2300},
	"SB": {Type: "SB", Turns: 50000, AccInt: 100, DecInt: 200, TorpDamage: 30,
		TorpSpeed: 14, TorpFuse: 30, PhaserFuse: 4, PhaserDamage: 120, MaxSpeed: 2,
		Repair: 140, MaxFuel: 60000, DetCost: 100, TorpCost: 300, PhaserCost: 960,
		WarpCost: 10, CloakCost: 75, Recharge: 35, MaxArmies: 25, MaxShield: 500,
		MaxDamage: 600, WpnCool: 4, EgnCool: 4, MaxWpnTemp: 1300, MaxEgnTemp: 1000,
		Mass: 5000},
	"GA": {Type: "GA", Turns: 192500, AccInt: 150, DecInt: 240, TorpDamage: 40,
		TorpSpeed: 13, TorpFuse: 35, PhaserFuse: 10, PhaserDamage: 100, MaxSpeed: 9,
		Repair: 112, MaxFuel: 12000, DetCost: 100, TorpCost: 280, PhaserCost: 700,
		WarpCost: 4, CloakCost: 26, Recharge: 13, MaxArmies: 5, MaxShield: 140,
		MaxDamage: 120, WpnCool: 2, EgnCool: 6, MaxWpnTemp: 1000, MaxEgnTemp: 1000,
		Mass: 2050},
}
