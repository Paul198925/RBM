package estimate

import (
	"strings"
	"testing"
)

const sample = `
estimates:
  - device: GeForce RTX 3060
    miner: rigel
    version: 1.23.2
    algorithm: kawpow
    hashrate: 19800000
    power_watts: 156
  - device: GeForce RTX 3060
    miner: wildrig
    algorithm: anime
    hashrate: 28400000
`

func TestParseAndLookup(t *testing.T) {
	table, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if table.Len() != 2 {
		t.Fatalf("%d entries", table.Len())
	}
	e, ok := table.Lookup(" geforce rtx 3060 ", "rigel", "kawpow")
	if !ok || e.Hashrate != 19.8e6 || e.PowerWatts != 156 || e.Version != "1.23.2" {
		t.Errorf("entry = %+v, %v", e, ok)
	}
	for _, miss := range [][3]string{{"GeForce RTX 3070", "rigel", "kawpow"}, {"GeForce RTX 3060", "gminer", "kawpow"}, {"GeForce RTX 3060", "rigel", "ethash"}} {
		if _, ok := table.Lookup(miss[0], miss[1], miss[2]); ok {
			t.Errorf("%v has an estimate", miss)
		}
	}
	// no table, no estimates
	var none *Table
	if _, ok := none.Lookup("GeForce RTX 3060", "rigel", "kawpow"); ok || none.Len() != 0 {
		t.Error("a nil table has estimates")
	}
}

func TestParseRejects(t *testing.T) {
	for name, text := range map[string]string{
		"no device":   "estimates: [{miner: rigel, algorithm: kawpow, hashrate: 1}]",
		"no hashrate": "estimates: [{device: X, miner: rigel, algorithm: kawpow}]",
		"twice":       "estimates: [{device: X, miner: rigel, algorithm: kawpow, hashrate: 1}, {device: x, miner: rigel, algorithm: kawpow, hashrate: 2}]",
		"no yaml":     "estimates: [",
	} {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestBuiltinIsValid(t *testing.T) {
	if _, err := Builtin(); err != nil {
		t.Fatal(err)
	}
}

func TestMarshalRoundsAndSorts(t *testing.T) {
	out, err := Marshal([]Entry{
		{Device: "GeForce RTX 3060", Miner: "wildrig", Algorithm: "anime", Hashrate: 28412345.6},
		{Device: "GeForce RTX 3060", Miner: "rigel", Version: "1.23.2", Algorithm: "kawpow", Hashrate: 19834567, PowerWatts: 155.6},
		{Device: "GeForce RTX 2080 SUPER", Miner: "xmrig", Algorithm: "ghostrider", Hashrate: 2153.7},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	// three digits are all a figure from elsewhere is worth
	for _, want := range []string{"hashrate: 2.84e+07", "hashrate: 1.98e+07", "power_watts: 156", "hashrate: 2150"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is missing in:\n%s", want, text)
		}
	}
	if strings.Index(text, "2080") > strings.Index(text, "3060") || strings.Index(text, "rigel") > strings.Index(text, "wildrig") {
		t.Errorf("not sorted:\n%s", text)
	}
	// what is written can be read again
	table, err := Parse(out)
	if err != nil || table.Len() != 3 {
		t.Errorf("read again: %v, %v", table.Len(), err)
	}
}

func TestBuiltinHasSaneFigures(t *testing.T) {
	table, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if table.Len() == 0 {
		t.Fatal("no estimates come with the program")
	}
	e, ok := table.Lookup("GeForce RTX 3060", "rigel", "kawpow")
	// a card of this kind does some 20 MH/s on KawPoW; a figure far off
	// would be one in the wrong unit
	if !ok || e.Hashrate < 10e6 || e.Hashrate > 40e6 || e.PowerWatts < 50 || e.PowerWatts > 250 {
		t.Errorf("RTX 3060 with rigel on kawpow: %+v, %v", e, ok)
	}
}

func TestWith(t *testing.T) {
	base, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	merged := base.With([]Entry{
		{Device: "geforce rtx 3060", Miner: "rigel", Algorithm: "kawpow", Hashrate: 21e6},
		{Device: "GeForce RTX 4070", Miner: "rigel", Algorithm: "kawpow", Hashrate: 30e6},
		{Device: "GeForce RTX 4070", Miner: "rigel", Algorithm: "ethash"},
	})
	// the newer figure wins, what it does not cover stays, nonsense is left out
	if e, _ := merged.Lookup("GeForce RTX 3060", "rigel", "kawpow"); e.Hashrate != 21e6 {
		t.Errorf("kawpow = %+v", e)
	}
	if _, ok := merged.Lookup("GeForce RTX 3060", "wildrig", "anime"); !ok || merged.Len() != 3 {
		t.Errorf("%d entries", merged.Len())
	}
	// the table it was made from is as it was
	if e, _ := base.Lookup("GeForce RTX 3060", "rigel", "kawpow"); e.Hashrate != 19.8e6 || base.Len() != 2 {
		t.Errorf("the base changed: %+v", e)
	}
	var none *Table
	if none.With(nil).Len() != 0 || none.With([]Entry{{Device: "X", Miner: "m", Algorithm: "a", Hashrate: 1}}).Len() != 1 {
		t.Error("a nil table cannot be added to")
	}
}

func TestEntries(t *testing.T) {
	base, _ := Parse([]byte(sample))
	entries := base.With([]Entry{{Device: "GeForce RTX 2080 SUPER", Miner: "rigel", Algorithm: "kawpow", Hashrate: 20.9e6, Reports: 4}}).Entries()
	if len(entries) != 3 || entries[0].Device != "GeForce RTX 2080 SUPER" || entries[0].Reports != 4 || entries[1].Miner != "rigel" || entries[2].Miner != "wildrig" {
		t.Errorf("entries = %+v", entries)
	}
	var none *Table
	if none.Entries() != nil {
		t.Error("a nil table has entries")
	}
}
