package engine

import (
	"strings"
	"testing"
	"time"

	"hashsteer/internal/miner"
	"hashsteer/internal/stats"
)

func (s *sim) bench(hashKey string) BenchmarkStatus {
	s.t.Helper()
	for _, b := range s.engine.Benchmarks() {
		if b.HashKey == hashKey {
			return b
		}
	}
	s.t.Fatalf("no benchmark entry for %s in %d entries", hashKey, len(s.engine.Benchmarks()))
	return BenchmarkStatus{}
}

func TestBenchmarkList(t *testing.T) {
	// zpool offers kawpow only; sha512256d cannot be measured
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	if len(s.engine.Benchmarks()) != 0 {
		t.Error("a list before the first round")
	}
	s.advance(2 * time.Minute)

	list := s.engine.Benchmarks()
	// one entry for each of the miner's algorithms
	if len(list) != 6 {
		t.Fatalf("%d entries: %+v", len(list), list)
	}
	kawpow := s.bench(kawpowID)
	if kawpow.Status != BenchRunning || kawpow.Group != "RTX3060" || kawpow.Miner != "rigel" || kawpow.Version != "1.23.2" ||
		kawpow.Algorithm != "kawpow" || len(kawpow.Devices) != 1 {
		t.Errorf("kawpow = %+v", kawpow)
	}
	if sha := s.bench(shaID); sha.Status != BenchNoOffer || sha.Updated != nil {
		t.Errorf("sha512256d = %+v", sha)
	}

	s.advance(10 * time.Minute)
	kawpow = s.bench(kawpowID)
	if kawpow.Status != BenchOK || kawpow.Hashrate != 25e6 || kawpow.PowerWatts != 150 || kawpow.MeasuredSeconds < 180 || kawpow.Updated == nil {
		t.Errorf("after the benchmark: %+v", kawpow)
	}

	// the rejected share of the last run shows once there was a run
	s.miners[0].exit()
	s.advance(20 * time.Second)
	if kawpow = s.bench(kawpowID); kawpow.RejectedPercent == nil || *kawpow.RejectedPercent != 0 {
		t.Errorf("rejected = %v", kawpow.RejectedPercent)
	}
}

func TestBenchmarkStatesFromConfigurationAndFailures(t *testing.T) {
	s := twoPrices(t, Options{ReservedVRAMMiB: 7000, Miners: map[string]MinerOptions{"rigel": {ExcludeAlgorithms: []string{"sha512256d"}}}})
	s.advance(time.Minute)
	// 12 GB card with 7000 MiB kept free: kawpow's 6144 do not fit
	if b := s.bench(kawpowID); b.Status != BenchNoFit {
		t.Errorf("kawpow = %+v", b)
	}
	if b := s.bench(shaID); b.Status != BenchExcluded {
		t.Errorf("sha512256d = %+v", b)
	}

	s = twoPrices(t, Options{})
	s.advance(time.Minute)
	for i := 0; i < 3; i++ {
		s.miners[len(s.miners)-1].exit()
		s.advance(20 * time.Second)
	}
	if b := s.bench(kawpowID); b.Status != BenchFailed || b.FailedUntil == nil || !b.FailedUntil.After(s.now) {
		t.Errorf("failed kawpow = %+v", b)
	}
}

func TestOutdatedBenchmarkAfterAMinerUpdate(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.advance(10 * time.Minute)
	if b := s.bench(kawpowID); b.Status != BenchOK {
		t.Fatalf("kawpow = %+v", b)
	}

	// the same statistics with a newer miner program
	defs, _ := miner.Builtin()
	newer := *defs["rigel"]
	newer.Version = "1.24.0"
	again := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	again.engine.deps.Store, again.store = s.store, s.store
	again.engine.deps.Definitions = map[string]*miner.Definition{"rigel": &newer}
	again.now, again.next = s.now, s.now
	again.advance(time.Minute)

	b := again.bench(kawpowID)
	if b.Status != BenchOutdated || b.MeasuredVersion != "1.23.2" || b.Version != "1.24.0" || b.Hashrate != 25e6 {
		t.Fatalf("after the update: %+v", b)
	}
	// an outdated benchmark still counts: the miner mines, it is not measured again by itself
	if again.engine.groups[0].run == nil || again.engine.groups[0].run.benchmark {
		t.Error("an outdated benchmark was repeated without being asked")
	}

	again.engine.ResetBenchmarks(ResetOutdated, nil)
	again.next = again.now
	again.advance(time.Minute)
	if b := again.bench(kawpowID); b.Status != BenchRunning || b.MeasuredVersion != "" {
		t.Errorf("after the reset: %+v\n%s", b, strings.Join(again.logs, "\n"))
	}
	// measured again, it belongs to the new version
	again.advance(10 * time.Minute)
	if b := again.bench(kawpowID); b.Status != BenchOK {
		t.Errorf("after the new benchmark: %+v", b)
	}
}

func TestResetBenchmarkClasses(t *testing.T) {
	s := twoPrices(t, Options{})
	s.advance(time.Minute)
	// kawpow keeps crashing and is taken out, sha512256d takes over
	for i := 0; i < 3; i++ {
		s.miners[len(s.miners)-1].exit()
		s.advance(20 * time.Second)
	}
	if s.bench(kawpowID).Status != BenchFailed || s.running() != "sha512256d" {
		t.Fatalf("setup: kawpow %s, running %q", s.bench(kawpowID).Status, s.running())
	}

	// "failed" lets it back in and measures it again; the other one is untouched
	s.engine.ResetBenchmarks(ResetFailed, nil)
	s.next = s.now
	s.advance(time.Minute)
	if b := s.bench(kawpowID); b.Status != BenchPending {
		t.Errorf("right after the reset: %+v", b)
	}
	// the miner that just started is left its minimum run time first
	s.advance(5 * time.Minute)
	if b := s.bench(kawpowID); b.Status != BenchRunning || len(s.engine.groups[0].disabled) != 0 {
		t.Errorf("after resetting the failed: %+v\n%s", b, strings.Join(s.logs, "\n"))
	}
	if _, ok, _ := s.store.Load(shaID); !ok {
		t.Error("the working benchmark was reset as well")
	}
	if _, ok, _ := s.store.Load(kawpowID); ok {
		t.Error("the failed benchmark kept its figures")
	}

	// "all" forgets everything that was measured
	s.advance(10 * time.Minute)
	s.engine.ResetBenchmarks(ResetAll, nil)
	s.next = s.now
	s.advance(time.Second)
	for _, key := range []string{kawpowID, shaID} {
		if _, ok, _ := s.store.Load(key); ok {
			t.Errorf("%s survived the reset of all", key)
		}
	}
	if !s.logged("2 benchmarks were reset") {
		t.Errorf("log:\n%s", strings.Join(s.logs, "\n"))
	}

	// several chosen ones at once, and nothing to do is said so
	s.seed(kawpowID, 100)
	s.seed(shaID, 100)
	s.engine.ResetBenchmarks("", []string{kawpowID, shaID})
	s.next = s.now
	s.advance(time.Second)
	if _, ok, _ := s.store.Load(shaID); ok {
		t.Error("a chosen benchmark survived")
	}
	s.engine.ResetBenchmarks(ResetOutdated, nil)
	s.next = s.now
	s.advance(time.Second)
	if !s.logged("no benchmark to reset") {
		t.Errorf("log:\n%s", strings.Join(s.logs, "\n"))
	}
}

func TestStoreNotesAndLastRuns(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	if err := s.store.SetNote("version/a", "1.0"); err != nil {
		t.Fatal(err)
	}
	s.store.SetNote("version/b", "2.0")
	s.store.SetNote("other/c", "x")
	s.store.SetNote("version/a", "1.1")
	s.store.SetNote("version/b", "")
	notes, err := s.store.Notes("version/")
	if err != nil || len(notes) != 1 || notes["a"] != "1.1" {
		t.Errorf("notes = %v, %v", notes, err)
	}

	base := s.now
	for i, algorithm := range []string{"kawpow", "kawpow", "sha512256d"} {
		s.store.AddRun(stats.Run{Group: "RTX3060", Miner: "rigel", Algorithm: algorithm, Started: base, Ended: base.Add(time.Minute),
			End: stats.EndSwitched, Accepted: int64(i + 1)})
	}
	last, err := s.store.LastRuns()
	if err != nil || len(last) != 2 || last[stats.RunKey("RTX3060", "rigel", "kawpow")].Accepted != 2 {
		t.Errorf("last runs = %+v, %v", last, err)
	}
}
