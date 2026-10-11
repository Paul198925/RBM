package selector

import (
	"math"
	"testing"
)

func cand(id string, hashrate, price float64) Candidate {
	return Candidate{ID: id, Hashrate: hashrate, Benchmarked: true, Price: price}
}

func TestProfit(t *testing.T) {
	c := Candidate{Hashrate: 50e6, Price: 1e-13, FeePercent: 1, PowerWatts: 200}
	// 50 MH/s at 1e-13 BTC per H/s and day, minus 1% fee
	if got := Revenue(c); math.Abs(got-4.95e-6) > 1e-15 {
		t.Errorf("revenue = %g", got)
	}
	// 200 W for a day are 4.8 kWh
	p := Params{PowerCostBTCPerKWh: 5e-6}
	if got := PowerCost(c, p); math.Abs(got-2.4e-5) > 1e-15 {
		t.Errorf("power cost = %g", got)
	}
	if got := Profit(c, p); math.Abs(got-(4.95e-6-2.4e-5)) > 1e-15 {
		t.Errorf("profit = %g", got)
	}
}

func TestDecide(t *testing.T) {
	p := Params{HysteresisPercent: 5}
	a := cand("a", 100, 1.00)
	b := cand("b", 100, 1.03)
	bMuchBetter := cand("b", 100, 1.20)
	fresh := Candidate{ID: "c"}

	cases := []struct {
		name          string
		candidates    []Candidate
		running       string
		locked        bool
		params        Params
		wantID        string
		wantBenchmark bool
	}{
		{name: "no candidates", params: p, wantID: ""},
		{name: "best wins from idle", candidates: []Candidate{a, b}, params: p, wantID: "b"},
		{name: "order of input does not matter", candidates: []Candidate{b, a}, params: p, wantID: "b"},
		{name: "small advantage does not replace the running one", candidates: []Candidate{a, b}, running: "a", params: p, wantID: "a"},
		{name: "large advantage does", candidates: []Candidate{a, bMuchBetter}, running: "a", params: p, wantID: "b"},
		{name: "locked keeps the running one", candidates: []Candidate{a, bMuchBetter}, running: "a", locked: true, params: p, wantID: "a"},
		{name: "lock is void if the running one is gone", candidates: []Candidate{bMuchBetter}, running: "a", locked: true, params: p, wantID: "b"},
		{name: "unmeasured goes first", candidates: []Candidate{a, b, fresh}, params: p, wantID: "c", wantBenchmark: true},
		{name: "unmeasured replaces a measured running one", candidates: []Candidate{a, fresh}, running: "a", params: p, wantID: "c", wantBenchmark: true},
		{name: "a running benchmark is finished first", candidates: []Candidate{{ID: "a"}, fresh}, running: "c", params: p, wantID: "c", wantBenchmark: true},
		{name: "locked benchmark", candidates: []Candidate{a, fresh}, running: "c", locked: true, params: p, wantID: "c", wantBenchmark: true},
	}
	for _, tc := range cases {
		got := Decide(tc.candidates, tc.running, tc.locked, tc.params)
		if got.ID != tc.wantID || got.Benchmark != tc.wantBenchmark {
			t.Errorf("%s: got %+v, want id %q benchmark %v", tc.name, got, tc.wantID, tc.wantBenchmark)
		}
		if got.Reason == "" {
			t.Errorf("%s: no reason given", tc.name)
		}
	}
}

func TestDecideWithPowerCost(t *testing.T) {
	// "fast" earns more but burns far more power than "lean"
	fast := Candidate{ID: "fast", Hashrate: 100, Price: 1, Benchmarked: true, PowerWatts: 300}
	lean := Candidate{ID: "lean", Hashrate: 80, Price: 1, Benchmarked: true, PowerWatts: 100}
	both := []Candidate{fast, lean}

	if got := Decide(both, "", false, Params{}); got.ID != "fast" {
		t.Errorf("without power cost: %+v", got)
	}
	// 5 BTC per kWh: fast costs 36, lean 12
	if got := Decide(both, "", false, Params{PowerCostBTCPerKWh: 5}); got.ID != "lean" || math.Abs(got.Profit-68) > 1e-9 {
		t.Errorf("with power cost: %+v", got)
	}

	expensive := Params{PowerCostBTCPerKWh: 50}
	got := Decide(both, "lean", false, expensive)
	if got.ID != "" || got.Profit >= 0 {
		t.Errorf("unprofitable: %+v", got)
	}
	expensive.MineAtLoss = true
	// the smaller loss wins
	if got := Decide(both, "", false, expensive); got.ID != "lean" {
		t.Errorf("mining at a loss: %+v", got)
	}
	// benchmarks run even when mining does not pay: without them nothing is known
	if got := Decide([]Candidate{{ID: "new", PowerWatts: 300}}, "", false, Params{PowerCostBTCPerKWh: 50}); got.ID != "new" || !got.Benchmark {
		t.Errorf("benchmark at a loss: %+v", got)
	}
}

func TestDecideDoesNotChangeItsInput(t *testing.T) {
	in := []Candidate{cand("b", 1, 1), cand("a", 1, 1)}
	Decide(in, "", false, Params{})
	if in[0].ID != "b" {
		t.Error("input was reordered")
	}
}
