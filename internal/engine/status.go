package engine

import (
	"sort"
	"time"

	"rbm/internal/selector"
)

// Status is a snapshot of what the engine is doing, for the status API.
type Status struct {
	Time time.Time `json:"time"`
	// BTCPrice is the price of one bitcoin in Currency, 0 if unknown or
	// not needed.
	BTCPrice float64       `json:"btc_price,omitempty"`
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

	Miner     string     `json:"miner,omitempty"`
	Algorithm string     `json:"algorithm,omitempty"`
	Pool      string     `json:"pool,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	Hashrate  float64    `json:"hashrate"`
	Power     float64    `json:"power_watts"`
	Accepted  int64      `json:"accepted"`
	Rejected  int64      `json:"rejected"`
	Invalid   int64      `json:"invalid"`
	// ProfitBTC is the expected profit per day of the running miner, from
	// its stored hashrate. 0 while benchmarking.
	ProfitBTC float64 `json:"profit_btc_per_day"`

	Candidates []CandidateStatus `json:"candidates"`
	Disabled   []DisabledStatus  `json:"disabled"`
}

// CandidateStatus is one thing a group could mine.
type CandidateStatus struct {
	ID          string  `json:"id"`
	Miner       string  `json:"miner"`
	Algorithm   string  `json:"algorithm"`
	Pool        string  `json:"pool"`
	Benchmarked bool    `json:"benchmarked"`
	Hashrate    float64 `json:"hashrate"`
	// Price is in BTC per day for one hash per second.
	Price     float64 `json:"price"`
	Power     float64 `json:"power_watts"`
	ProfitBTC float64 `json:"profit_btc_per_day"`
}

// DisabledStatus is a candidate that is taken out after failures.
type DisabledStatus struct {
	ID    string    `json:"id"`
	Until time.Time `json:"until"`
}

// Status returns the latest snapshot. It is safe to call from any goroutine.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

// publish builds a fresh snapshot. Only the engine's own goroutine calls it.
func (e *Engine) publish(now time.Time) {
	status := Status{Time: now, BTCPrice: e.rate, Groups: make([]GroupStatus, 0, len(e.groups))}
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
		}
		if r := g.run; r != nil {
			gs.State = "mining"
			if r.benchmark {
				gs.State = "benchmarking"
			} else {
				gs.ProfitBTC = selector.Profit(r.Candidate, e.params)
			}
			since := r.started
			gs.Miner, gs.Algorithm, gs.Pool, gs.Since = r.def.Name, r.algo.Name, r.offer.Pool, &since
			if last, ok := r.window.Last(); ok {
				live := last.For(r.algo.NameForMiner())
				gs.Hashrate, gs.Power = live.Hashrate, last.PowerWatts
				gs.Accepted, gs.Rejected, gs.Invalid = live.Accepted, live.Rejected, live.Invalid
			}
		}
		for _, o := range g.options {
			cs := CandidateStatus{
				ID:          o.ID,
				Miner:       o.def.Name,
				Algorithm:   o.algo.Name,
				Pool:        o.offer.Pool,
				Benchmarked: o.Benchmarked,
				Hashrate:    o.Hashrate,
				Price:       o.Price,
				Power:       o.PowerWatts,
			}
			if o.Benchmarked {
				cs.ProfitBTC = selector.Profit(o.Candidate, e.params)
			}
			gs.Candidates = append(gs.Candidates, cs)
		}
		for id, until := range g.disabled {
			gs.Disabled = append(gs.Disabled, DisabledStatus{ID: id, Until: until})
		}
		sort.Slice(gs.Disabled, func(i, j int) bool { return gs.Disabled[i].ID < gs.Disabled[j].ID })
		status.Groups = append(status.Groups, gs)
	}

	e.mu.Lock()
	e.status = status
	e.mu.Unlock()
}
