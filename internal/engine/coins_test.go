package engine

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"rbm/internal/miner"
)

func (s *sim) wantTargets(want ...string) {
	s.t.Helper()
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(s.targets, want) {
		s.t.Fatalf("targets = %v, want %v\nlog:\n%s", s.targets, want, strings.Join(s.logs, "\n"))
	}
}

// coinSim mines kawpow, already benchmarked at 100 H/s, with the port
// paying 1.00 and two coins on offer.
func coinSim(t *testing.T, opts Options) *sim {
	t.Helper()
	s := newSim(t, opts, map[string]float64{"kawpow": 1.00e-10})
	s.source.coins = map[string]map[string]float64{"kawpow": {"AAA": 1.20e-10, "BBB": 0.80e-10}}
	s.seed(kawpowID, 100)
	s.hashrates = map[string]float64{"kawpow": 100}
	return s
}

func TestBestCoinIsMined(t *testing.T) {
	s := coinSim(t, Options{})
	s.advance(10 * time.Minute)
	s.wantTargets("rigel/kawpow@fakepool:AAA")

	// the status names the coin and lists all three ways to mine kawpow
	g := s.engine.Status().Groups[0]
	if g.Coin != "AAA" || g.Running != kawpowID+"@fakepool:AAA" || len(g.Candidates) != 3 {
		t.Errorf("status = %+v", g)
	}
	for _, c := range g.Candidates {
		if c.HashKey != kawpowID || !c.Benchmarked || c.Hashrate != 100 {
			t.Errorf("candidate = %+v", c)
		}
	}
}

func TestCoinSwitchNeedsAClearAdvantage(t *testing.T) {
	s := coinSim(t, Options{})
	s.advance(10 * time.Minute)

	// BBB overtakes AAA by 3%: not worth a restart
	s.source.coins["kawpow"]["BBB"] = 1.236e-10
	s.advance(6 * time.Hour)
	s.wantTargets("rigel/kawpow@fakepool:AAA")

	// by 25% it is, and the same miner restarts for the other coin
	s.source.coins["kawpow"]["BBB"] = 1.50e-10
	s.advance(6 * time.Hour)
	s.wantTargets("rigel/kawpow@fakepool:AAA", "rigel/kawpow@fakepool:BBB")
	// one benchmark serves both: nothing was measured again
	if s.logged("benchmarking") {
		t.Errorf("a coin switch caused a benchmark:\n%s", strings.Join(s.logs, "\n"))
	}
}

func TestCoinThatVanishesIsLeft(t *testing.T) {
	s := coinSim(t, Options{})
	s.advance(10 * time.Minute)
	s.wantTargets("rigel/kawpow@fakepool:AAA")

	// the pool stops offering the coin: fall back to the next best
	delete(s.source.coins["kawpow"], "AAA")
	s.advance(10 * time.Minute)
	s.wantTargets("rigel/kawpow@fakepool:AAA", "rigel/kawpow@fakepool")
}

func TestUnmeasuredIsBenchmarkedOnceWhereItPaysBest(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1.00e-13})
	s.source.coins = map[string]map[string]float64{"kawpow": {"AAA": 1.20e-13, "BBB": 0.80e-13}}
	s.advance(30 * time.Minute)
	// benchmarked on the best offer, and then simply kept there
	s.wantTargets("rigel/kawpow@fakepool:AAA")
	if s.engine.groups[0].run.benchmark {
		t.Error("still benchmarking")
	}
}

func TestMinerAndAlgorithmFilters(t *testing.T) {
	prices := map[string]float64{"kawpow": 2e-10, "sha512256d": 1e-10}
	seed := func(s *sim) {
		s.seed(kawpowID, 100)
		s.seed(shaID, 100)
		s.hashrates = map[string]float64{"kawpow": 100, "sha512256d": 100}
	}
	cases := map[string]struct {
		opts Options
		want []string
	}{
		"nothing filtered":   {Options{}, []string{"kawpow"}},
		"algorithm excluded": {Options{ExcludeAlgorithms: []string{"kawpow"}}, []string{"sha512256d"}},
		"only one algorithm": {Options{OnlyAlgorithms: []string{"sha512256d"}}, []string{"sha512256d"}},
		"excluded for the miner": {Options{Miners: map[string]MinerOptions{
			"rigel": {ExcludeAlgorithms: []string{"kawpow"}}}}, []string{"sha512256d"}},
		"miner disabled": {Options{Miners: map[string]MinerOptions{"rigel": {Disabled: true}}}, nil},
	}
	for name, c := range cases {
		s := newSim(t, c.opts, prices)
		seed(s)
		s.advance(10 * time.Minute)
		if !reflect.DeepEqual(s.launches, c.want) {
			t.Errorf("%s: launches = %v, want %v", name, s.launches, c.want)
		}
	}
}

func TestExtraArgsReachTheMiner(t *testing.T) {
	s := coinSim(t, Options{Miners: map[string]MinerOptions{"rigel": {ExtraArgs: []string{"--temp-limit", "tc[80-85]"}}}})
	s.advance(time.Minute)
	if len(s.extraArgs) != 1 || !reflect.DeepEqual(s.extraArgs[0], []string{"--temp-limit", "tc[80-85]"}) {
		t.Errorf("extra args = %v", s.extraArgs)
	}
}

func TestResetBenchmarkMeasuresAgain(t *testing.T) {
	s := coinSim(t, Options{})
	s.advance(10 * time.Minute)
	s.hashrates["kawpow"] = 80
	s.miners[0].hashrate = 80

	s.engine.ResetBenchmark(kawpowID)
	// the command is picked up by the next round
	s.next = s.now
	s.advance(time.Minute)
	if !s.logged("benchmark of "+kawpowID+" was reset") || !s.engine.groups[0].run.benchmark {
		t.Fatalf("not benchmarking again:\n%s", strings.Join(s.logs, "\n"))
	}
	// the running miner does the measuring, nothing is restarted
	s.advance(15 * time.Minute)
	s.wantTargets("rigel/kawpow@fakepool:AAA")
	st, ok, _ := s.store.Load(kawpowID)
	if !ok || st.Average(0) != 80 || s.engine.groups[0].run.benchmark {
		t.Errorf("stat after the new benchmark: %+v", st)
	}
}

func TestEnableLiftsAPenalty(t *testing.T) {
	s := coinSim(t, Options{})
	s.advance(time.Minute)
	for i := 0; i < 3; i++ {
		s.miners[len(s.miners)-1].exit()
		s.advance(20 * time.Second)
	}
	if s.running() != "" || len(s.engine.Status().Groups[0].Disabled) != 1 {
		t.Fatalf("not taken out:\n%s", strings.Join(s.logs, "\n"))
	}

	s.engine.Enable(kawpowID)
	s.next = s.now
	s.advance(time.Minute)
	if s.running() != "kawpow" || len(s.engine.Status().Groups[0].Disabled) != 0 {
		t.Errorf("not running again:\n%s", strings.Join(s.logs, "\n"))
	}
}

func TestOffersSnapshot(t *testing.T) {
	s := coinSim(t, Options{})
	if len(s.engine.Offers()) != 0 {
		t.Error("offers before the first round")
	}
	s.advance(time.Minute)
	if got := len(s.engine.Offers()); got != 3 {
		t.Errorf("%d offers", got)
	}
}

func TestWildrigAndRigelCompeteOnTheSameCoin(t *testing.T) {
	s := coinSim(t, Options{})
	defs, _ := miner.Builtin()
	s.engine.deps.Definitions = map[string]*miner.Definition{"rigel": defs["rigel"], "wildrig": defs["wildrig"]}
	// wildrig is the faster one here, and has the lower fee on kawpow
	s.seed("hashrate/wildrig/GPU#00/kawpow", 120)
	s.advance(10 * time.Minute)
	s.wantTargets("wildrig/kawpow@fakepool:AAA")
}
