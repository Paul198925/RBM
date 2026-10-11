// Package selector decides what a group of devices should mine. It only
// computes: everything it needs comes in as arguments, nothing is started,
// stored or fetched here.
package selector

import "sort"

// Candidate is one way a device group could mine: a miner program on an
// algorithm at a pool.
type Candidate struct {
	ID string
	// Hashrate is the measured speed in hashes per second. It means
	// nothing while Benchmarked is false.
	Hashrate    float64
	Benchmarked bool
	// Price is what the pool pays, in BTC per day for one hash per second.
	Price float64
	// FeePercent is the developer fee of the miner program.
	FeePercent float64
	PowerWatts float64
}

// Params are the settings of the decision.
type Params struct {
	// PowerCostBTCPerKWh is 0 if electricity is not to be counted.
	PowerCostBTCPerKWh float64
	// HysteresisPercent is how much better another candidate must be
	// before the running one is given up.
	HysteresisPercent float64
	// MineAtLoss keeps mining when even the best candidate costs more
	// electricity than it earns.
	MineAtLoss bool
}

// Decision is what the group should do next.
type Decision struct {
	// ID is the candidate to run, empty to stay idle.
	ID string
	// Benchmark is set if the candidate runs to be measured.
	Benchmark bool
	// Profit is the expected profit in BTC per day, 0 for a benchmark.
	Profit float64
	Reason string
}

// Revenue returns what c earns in BTC per day, after the miner's fee.
func Revenue(c Candidate) float64 {
	return c.Hashrate * c.Price * (1 - c.FeePercent/100)
}

// PowerCost returns what c's electricity costs in BTC per day.
func PowerCost(c Candidate, p Params) float64 {
	return c.PowerWatts * 24 / 1000 * p.PowerCostBTCPerKWh
}

// Profit returns revenue minus electricity in BTC per day.
func Profit(c Candidate, p Params) float64 {
	return Revenue(c) - PowerCost(c, p)
}

// Decide picks the candidate for one device group. running is the ID of the
// candidate that runs now, empty if none; locked says it must not be
// replaced yet (it is being benchmarked or has only just started).
func Decide(candidates []Candidate, running string, locked bool, p Params) Decision {
	sorted := append([]Candidate(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	var current *Candidate
	for i := range sorted {
		if sorted[i].ID == running {
			current = &sorted[i]
		}
	}

	if current != nil && locked {
		d := Decision{ID: current.ID, Benchmark: !current.Benchmarked, Reason: "keeps running, not to be replaced yet"}
		if current.Benchmarked {
			d.Profit = Profit(*current, p)
		}
		return d
	}

	// nothing can be compared before it has been measured
	if current != nil && !current.Benchmarked {
		return Decision{ID: current.ID, Benchmark: true, Reason: "benchmark in progress"}
	}
	for _, c := range sorted {
		if !c.Benchmarked {
			return Decision{ID: c.ID, Benchmark: true, Reason: "needs a benchmark"}
		}
	}

	if len(sorted) == 0 {
		return Decision{Reason: "nothing to mine: no pool offers an algorithm a miner supports"}
	}
	best := sorted[0]
	bestScore := score(best, running, p)
	for _, c := range sorted[1:] {
		if s := score(c, running, p); s > bestScore {
			best, bestScore = c, s
		}
	}

	profit := Profit(best, p)
	if profit <= 0 && !p.MineAtLoss {
		return Decision{Profit: profit, Reason: "not profitable: electricity costs more than mining earns"}
	}
	reason := "most profitable"
	if best.ID == running {
		reason = "still the most profitable"
	}
	return Decision{ID: best.ID, Profit: profit, Reason: reason}
}

// score is the profit used for the comparison: the running candidate gets a
// bonus, so that a small advantage of another one does not cause a restart.
func score(c Candidate, running string, p Params) float64 {
	profit := Profit(c, p)
	if c.ID != running {
		return profit
	}
	if profit >= 0 {
		return profit * (1 + p.HysteresisPercent/100)
	}
	return profit * (1 - p.HysteresisPercent/100)
}
