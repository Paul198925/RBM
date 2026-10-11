package miner

import (
	"slices"
	"strings"
	"testing"
)

// The algorithms zpool.ca listed on 2026-10-11, in the engine's spelling.
var zpoolAlgorithms = strings.Fields(`allium anime argon2d1000 argon2d16000 argon2d4096 argon2d500 balloon bmw512 curve
	equihash144 equihash192 evrprogpow firopow flex ghostrider heavyhash hmq1725 hoohashpepew kawpow
	lyra2z330 m7m megabtx megamec meowpow meraki mike minotaurx neoscryptxaya phihash power2b
	rinhash sccpow scryptn2 sha3256t sha512256d skein2 skydoge soterg timetravel
	verthash verushash whirlpool whirlpoolx2 x16rt x16rv2 x21s x25x yescrypt yescryptr16 yescryptr32 yescryptr8 yespower
	yespoweradvc yespowereqpay yespowerltncg yespowermgpc yespowerr16 yespowersmt yespowersugar yespowertide yespowerurx`)

// What ASICs or FPGAs mine at zpool.ca. No miner program is offered these:
// a graphics card or CPU earns next to nothing there, and measuring them
// only costs time.
var asicAlgorithms = strings.Fields(`equihash groestl lyra2v2 myrgr neoscrypt odocrypt quark qubit scrypt sha256 skein tribus x11 x13`)

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
	for _, name := range asicAlgorithms {
		for _, def := range defs {
			if _, ok := def.Algorithm(name); ok {
				t.Errorf("%s mines %s, which is left to ASICs", def.Name, name)
			}
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

func TestOldTrexMinesWhatTheNewOneDropped(t *testing.T) {
	old, current := builtin1(t, "trex19"), builtin1(t, "trex")
	megamec, ok := old.Algorithm("megamec")
	if !ok {
		t.Fatal("trex19 does not list megamec")
	}
	// the two versions are installed side by side and share no algorithm
	if old.Downloads["windows"].URL == current.Downloads["windows"].URL || old.Version == current.Version {
		t.Errorf("trex19 is the same program as trex")
	}
	for _, a := range old.Algorithms {
		if _, both := current.Algorithm(a.Name); both {
			t.Errorf("both versions mine %s", a.Name)
		}
	}
	args, err := old.BuildArgs(Job{Algorithm: megamec, DeviceIndexes: []int{0, 1}, APIPort: 4500, Host: "megamec.eu.mine.zpool.ca", Port: 4321,
		User: "wallet1", Pass: "rig1,c=BTC"})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	for _, want := range []string{"-a megamec", "-o stratum+tcp://megamec.eu.mine.zpool.ca:4321", "-d 0,1", "--api-bind-http 127.0.0.1:4500", "--api-read-only"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is missing in %q", want, line)
		}
	}
	// an option the old version does not know would keep it from starting
	if strings.Contains(line, "--no-new-block-info") {
		t.Errorf("an option of the new version: %s", line)
	}
}

func TestZpoolNamesAreTranslated(t *testing.T) {
	ccminer := builtin1(t, "ccminer-tpfuemp")
	for name, want := range map[string]string{"neoscryptxaya": "xaya", "sha3256t": "sha3t", "yespowersmt": "yespowersmt"} {
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
