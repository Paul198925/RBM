package miner

import (
	"slices"
	"strings"
	"testing"
)

// The algorithms zpool.ca listed on 2026-10-11, in the engine's spelling.
// megamec is missing on purpose: only versions of miner programs from 2021
// and 2022 mine it.
var zpoolAlgorithms = strings.Fields(`allium anime argon2d1000 argon2d16000 argon2d4096 argon2d500 balloon bmw512 curve
	equihash equihash144 equihash192 evrprogpow firopow flex ghostrider groestl heavyhash hmq1725 hoohashpepew kawpow
	lyra2v2 lyra2z330 m7m megabtx meowpow meraki mike minotaurx myrgr neoscrypt neoscryptxaya odocrypt phihash power2b
	quark qubit rinhash sccpow scrypt scryptn2 sha256 sha3256t sha512256d skein skein2 skydoge soterg timetravel tribus
	verthash verushash whirlpool whirlpoolx2 x11 x13 x16rt x16rv2 x21s x25x yescrypt yescryptr16 yescryptr32 yescryptr8 yespower
	yespoweradvc yespowereqpay yespowerltncg yespowermgpc yespowerr16 yespowersmt yespowersugar yespowertide yespowerurx`)

func TestEveryZpoolAlgorithmHasAMiner(t *testing.T) {
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range zpoolAlgorithms {
		found := false
		for _, def := range defs {
			_, ok := def.Algorithm(name)
			found = found || ok
		}
		if !found {
			t.Errorf("no miner for %s", name)
		}
	}
	// no definition lists an algorithm twice
	for _, def := range defs {
		var seen []string
		for _, a := range def.Algorithms {
			if slices.Contains(seen, a.Name) {
				t.Errorf("%s lists %s twice", def.Name, a.Name)
			}
			seen = append(seen, a.Name)
		}
	}
}

func TestZpoolNamesAreTranslated(t *testing.T) {
	ccminer := builtin1(t, "ccminer-tpfuemp")
	for name, want := range map[string]string{"myrgr": "myr-gr", "neoscryptxaya": "xaya", "odocrypt": "odo", "sha256": "sha256d",
		"sha3256t": "sha3t", "x11": "x11", "yespowersmt": "yespowersmt"} {
		a, ok := ccminer.Algorithm(name)
		if !ok || a.NameForMiner() != want {
			t.Errorf("%s is called %q, want %q", name, a.NameForMiner(), want)
		}
	}

	// cpuminer-opt has one yespower and takes the coin's parameters
	cpu := builtin1(t, "cpuminer-opt")
	advc, ok := cpu.Algorithm("yespoweradvc")
	if !ok {
		t.Fatal("cpuminer-opt does not list yespoweradvc")
	}
	args, err := cpu.BuildArgs(Job{Algorithm: advc, APIPort: 4501, Host: "yespoweradvc.eu.mine.zpool.ca", Port: 6248, User: "w", Pass: "x"})
	if err != nil {
		t.Fatal(err)
	}
	// the key is one argument, spaces and all
	if !slices.Contains(args, "Let the quest begin") || !strings.Contains(strings.Join(args, " "), "-a yespower -o ") {
		t.Errorf("args = %q", args)
	}
	if a, _ := builtin1(t, "srbminer-cpu").Algorithm("argon2d16000"); a.NameForMiner() != "argon2d_16000" {
		t.Errorf("srbminer calls argon2d16000 %q", a.NameForMiner())
	}
}
