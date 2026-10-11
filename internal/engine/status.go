package engine

import (
	"sort"
	"time"

	"hashsteer/internal/selector"
)

// Status is a snapshot of what the engine is doing, for the status API.
type Status struct {
	Time time.Time `json:"time"`
	// BTCPrice is the price of one bitcoin in Currency, 0 if unknown or
	// not needed.
	BTCPrice float64 `json:"btc_price,omitempty"`
	// BenchmarkSeconds is the measured mining time a hashrate needs
	// before it is trusted.
	BenchmarkSeconds float64 `json:"benchmark_seconds"`
	// Paused is set while mining is paused by the user.
	Paused   bool          `json:"paused"`
	Currency string        `json:"currency,omitempty"`
	Groups   []GroupStatus `json:"groups"`
}

// GroupStatus describes one device group.
type GroupStatus struct {
	Name    string   `json:"name"`
	Devices []string `json:"devices"`
	// State is "idle", "benchmarking" or "mining".
	State  string `json:"state"`
	Reason string `json:"reason"`

	Miner     string `json:"miner,omitempty"`
	Algorithm string `json:"algorithm,omitempty"`
	Pool      string `json:"pool,omitempty"`
	// Coin is the coin being mined, empty where the pool picks it.
	Coin string `json:"coin,omitempty"`
	// Running is the ID of the running candidate.
	Running  string     `json:"running,omitempty"`
	Since    *time.Time `json:"since,omitempty"`
	Hashrate float64    `json:"hashrate"`
	Power    float64    `json:"power_watts"`
	Accepted int64      `json:"accepted"`
	Rejected int64      `json:"rejected"`
	Invalid  int64      `json:"invalid"`
	// ProfitBTC is the expected profit per day of the running miner, from
	// its stored hashrate. 0 while benchmarking.
	ProfitBTC float64 `json:"profit_btc_per_day"`

	Candidates []CandidateStatus `json:"candidates"`
	Disabled   []DisabledStatus  `json:"disabled"`
	// Crashes lists what failed lately without being taken out yet.
	Crashes []CrashStatus `json:"crashes"`
}

// CrashStatus counts the recent failures of a miner on an algorithm.
type CrashStatus struct {
	HashKey string    `json:"hash_key"`
	Count   int       `json:"count"`
	Last    time.Time `json:"last"`
}

// CandidateStatus is one thing a group could mine.
type CandidateStatus struct {
	ID string `json:"id"`
	// HashKey names miner, algorithm and devices: what a benchmark and a
	// failure belong to, whatever the pool or coin.
	HashKey     string `json:"hash_key"`
	Miner       string `json:"miner"`
	Algorithm   string `json:"algorithm"`
	Pool        string `json:"pool"`
	Coin        string `json:"coin,omitempty"`
	Benchmarked bool   `json:"benchmarked"`
	// Estimated says the hashrate was measured elsewhere, not here.
	Estimated bool `json:"estimated"`
	// MeasuredSeconds is the mining time the hashrate rests on so far.
	MeasuredSeconds float64 `json:"measured_seconds"`
	Hashrate        float64 `json:"hashrate"`
	// Price is in BTC per day for one hash per second.
	Price      float64 `json:"price"`
	FeePercent float64 `json:"fee_percent"`
	Power      float64 `json:"power_watts"`
	// RevenueBTC is what it earns per day after the miner's fee,
	// ProfitBTC what is left after electricity.
	RevenueBTC float64 `json:"revenue_btc_per_day"`
	ProfitBTC  float64 `json:"profit_btc_per_day"`
}

// DisabledStatus is a miner on an algorithm that is taken out after failures.
type DisabledStatus struct {
	HashKey string    `json:"hash_key"`
	Until   time.Time `json:"until"`
	// Reason says how it failed the last time.
	Reason string `json:"reason,omitempty"`
}

// Status returns the latest snapshot. It is safe to call from any goroutine.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

// publish builds a fresh snapshot. Only the engine's own goroutine calls it.
func (e *Engine) publish(now time.Time) {
	status := Status{Time: now, BTCPrice: e.rate, BenchmarkSeconds: e.opts.BenchmarkTime.Seconds(), Paused: e.paused,
		Groups: make([]GroupStatus, 0, len(e.groups))}
	if e.rate > 0 {
		status.Currency = e.opts.PowerCurrency
	}
	for _, g := range e.groups {
		gs := GroupStatus{
			Name:       g.name,
			Devices:    g.deviceIDs(),
			State:      "idle",
			Reason:     g.decision.Reason,
			Candidates: make([]CandidateStatus, 0, len(g.options)),
			Disabled:   make([]DisabledStatus, 0, len(g.disabled)),
			Crashes:    make([]CrashStatus, 0, len(g.crashes)),
		}
		for id, times := range g.crashes {
			if len(times) > 0 {
				gs.Crashes = append(gs.Crashes, CrashStatus{HashKey: id, Count: len(times), Last: times[len(times)-1]})
			}
		}
		sort.Slice(gs.Crashes, func(i, j int) bool { return gs.Crashes[i].HashKey < gs.Crashes[j].HashKey })
		if r := g.run; r != nil {
			gs.State = "mining"
			if r.benchmark {
				gs.State = "benchmarking"
			} else {
				gs.ProfitBTC = selector.Profit(r.Candidate, e.params)
			}
			since := r.started
			gs.Miner, gs.Algorithm, gs.Pool, gs.Coin = r.def.Name, r.algo.Name, r.offer.Pool, r.offer.Coin
			gs.Running, gs.Since = r.ID, &since
			if last, ok := r.window.Last(); ok {
				live := last.For(r.algo.NameForMiner())
				gs.Hashrate, gs.Power = live.Hashrate, last.PowerWatts
				gs.Accepted, gs.Rejected, gs.Invalid = live.Accepted, live.Rejected, live.Invalid
			}
		}
		for _, o := range g.options {
			cs := CandidateStatus{
				ID:              o.ID,
				HashKey:         o.hashKey,
				Miner:           o.def.Name,
				Algorithm:       o.algo.Name,
				Pool:            o.offer.Pool,
				Coin:            o.offer.Coin,
				Benchmarked:     o.Benchmarked,
				Estimated:       o.estimated,
				MeasuredSeconds: o.measured.Seconds(),
				Hashrate:        o.Hashrate,
				Price:           o.Price,
				FeePercent:      o.FeePercent,
				Power:           o.PowerWatts,
			}
			if o.Benchmarked {
				cs.RevenueBTC = selector.Revenue(o.Candidate)
				cs.ProfitBTC = selector.Profit(o.Candidate, e.params)
			}
			gs.Candidates = append(gs.Candidates, cs)
		}
		for id, until := range g.disabled {
			gs.Disabled = append(gs.Disabled, DisabledStatus{HashKey: id, Until: until, Reason: g.reasons[id]})
		}
		sort.Slice(gs.Disabled, func(i, j int) bool { return gs.Disabled[i].HashKey < gs.Disabled[j].HashKey })
		status.Groups = append(status.Groups, gs)
	}

	e.mu.Lock()
	e.status = status
	e.mu.Unlock()
}
