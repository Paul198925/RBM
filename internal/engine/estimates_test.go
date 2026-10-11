package engine

import (
	"testing"
	"time"

	"hashsteer/internal/device"
	"hashsteer/internal/estimate"
	"hashsteer/internal/miner"
)

func estimates(t *testing.T, text string) *estimate.Table {
	t.Helper()
	table, err := estimate.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func TestEstimatesStandInUntilMeasured(t *testing.T) {
	// others measured this card at 20 MH/s on kawpow; here it does 25
	table := estimates(t, `
estimates:
  - {device: GeForce RTX 3060, miner: rigel, algorithm: kawpow, hashrate: 20000000, power_watts: 140}
  - {device: GeForce RTX 3060, miner: rigel, algorithm: sha512256d, hashrate: 400000000, power_watts: 130}
`)
	s := newSim(t, Options{Estimates: table}, map[string]float64{"kawpow": 1e-13, "sha512256d": 1e-15})
	s.advance(2 * time.Minute)

	// it mines what the estimates call best, without a benchmark first
	g := s.engine.Status().Groups[0]
	if s.running() != "kawpow" || g.State != "mining" {
		t.Fatalf("running %q, state %s, logs %v", s.running(), g.State, s.logs)
	}
	var kawpow CandidateStatus
	for _, c := range g.Candidates {
		if c.ID == g.Running {
			kawpow = c
		}
	}
	if !kawpow.Benchmarked || !kawpow.Estimated || kawpow.Hashrate != 20e6 || kawpow.Power != 140 {
		t.Errorf("candidate = %+v", kawpow)
	}
	if b := s.bench(kawpowID); b.Status != BenchEstimate || b.Hashrate != 20e6 {
		t.Errorf("benchmark entry = %+v", b)
	}

	// what is mined is measured, and the measurement takes over
	s.advance(10 * time.Minute)
	g = s.engine.Status().Groups[0]
	for _, c := range g.Candidates {
		if c.ID == g.Running && (c.Estimated || !c.Benchmarked || c.Hashrate != 25e6) {
			t.Errorf("after measuring: %+v", c)
		}
	}
	if b := s.bench(kawpowID); b.Status != BenchOK || b.Hashrate != 25e6 {
		t.Errorf("benchmark entry = %+v", b)
	}
	// the other algorithm was never worth a start, so it keeps its estimate
	if len(s.launches) != 1 {
		t.Errorf("launches = %v", s.launches)
	}
	if b := s.bench(shaID); b.Status != BenchEstimate || b.Hashrate != 400e6 || b.PowerWatts != 130 {
		t.Errorf("sha512256d = %+v", b)
	}
}

func TestWithoutAnEstimateItIsBenchmarked(t *testing.T) {
	// an estimate for another card helps nothing
	table := estimates(t, `estimates: [{device: GeForce RTX 3070, miner: rigel, algorithm: kawpow, hashrate: 30000000}]`)
	s := newSim(t, Options{Estimates: table}, map[string]float64{"kawpow": 1e-13})
	s.advance(2 * time.Minute)
	if g := s.engine.Status().Groups[0]; g.State != "benchmarking" {
		t.Errorf("state = %s", g.State)
	}
	if b := s.bench(kawpowID); b.Status != BenchRunning {
		t.Errorf("benchmark entry = %+v", b)
	}
}

func TestEstimatesCountPerDevice(t *testing.T) {
	g := &group{devices: []device.Device{{Model: "GeForce RTX 3060"}, {Model: "GeForce RTX 3060"}}}
	e := &Engine{opts: Options{Estimates: estimates(t, `estimates: [{device: GeForce RTX 3060, miner: rigel, algorithm: kawpow, hashrate: 20000000, power_watts: 140}]`)}}
	entry, ok := e.estimate(g, &miner.Definition{Name: "rigel"}, miner.Algorithm{Name: "kawpow"})
	if !ok || entry.Hashrate != 40e6 || entry.PowerWatts != 280 {
		t.Errorf("two cards: %+v, %v", entry, ok)
	}
	if _, ok := (&Engine{}).estimate(g, &miner.Definition{Name: "rigel"}, miner.Algorithm{Name: "kawpow"}); ok {
		t.Error("an estimate without a table")
	}
}

func TestExportEstimates(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.advance(10 * time.Minute)
	defs, err := miner.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	devices := []device.Device{
		{ID: "GPU#00", Kind: device.GPU, Vendor: "NVIDIA", Model: "GeForce RTX 3060", Index: 0, MemoryMiB: 12288},
		{ID: "CPU#00", Kind: device.CPU, Threads: 20},
	}
	entries, err := ExportEstimates(s.store, defs, devices, 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// only what was measured to the end is passed on
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	e := entries[0]
	if e.Device != "GeForce RTX 3060" || e.Miner != "rigel" || e.Algorithm != "kawpow" || e.Hashrate != 25e6 || e.PowerWatts != 150 || e.Version != "1.23.2" {
		t.Errorf("entry = %+v", e)
	}
	// and it reads back as an estimate
	out, err := estimate.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	table, err := estimate.Parse(out)
	if got, ok := table.Lookup("GeForce RTX 3060", "rigel", "kawpow"); err != nil || !ok || got.Hashrate != 25e6 {
		t.Errorf("read back: %+v, %v, %v", got, ok, err)
	}
	// a measurement that is too short is not
	if short, _ := ExportEstimates(s.store, defs, devices, time.Hour); len(short) != 0 {
		t.Errorf("with an hour asked for: %+v", short)
	}
}
