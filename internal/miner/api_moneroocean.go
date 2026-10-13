package miner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// moneroOceanStatsURL is where the pool says what each of its coins pays.
// A variable for tests.
var moneroOceanStatsURL = "https://api.moneroocean.stream/pool/stats"

// how long the pool's figures are used before they are asked for again
const moneroOceanStatsFor = 5 * time.Minute

// moneroOceanAPI reads XMRig as MoneroOcean builds it. That miner switches
// between algorithms as the pool tells it, and its hashrate means something
// else on each. The pool pays every hash by what its coin is worth, so the
// hashrate is told in the one unit that can be compared and priced: hashes
// of Monero's algorithm that earn the same.
type moneroOceanAPI struct{}

type moneroOceanSummary struct {
	Algo     string `json:"algo"`
	Hashrate struct {
		Total []*float64 `json:"total"`
	} `json:"hashrate"`
	Connection struct {
		// Uptime is 0 while the miner is not at the pool: when it starts,
		// and while it measures itself on every algorithm.
		Uptime float64 `json:"uptime"`
		Algo   string  `json:"algo"`
	} `json:"connection"`
	Results struct {
		SharesGood  float64 `json:"shares_good"`
		SharesTotal float64 `json:"shares_total"`
	} `json:"results"`
}

func (moneroOceanAPI) Poll(ctx context.Context, port int) (Sample, error) {
	var summary moneroOceanSummary
	if err := getJSON(ctx, port, "/2/summary", &summary); err != nil {
		return Sample{}, err
	}
	sample := AlgoSample{
		Accepted: int64(summary.Results.SharesGood),
		Rejected: int64(summary.Results.SharesTotal - summary.Results.SharesGood),
	}
	var raw float64
	for _, total := range summary.Hashrate.Total {
		if total != nil && *total > 0 {
			raw = *total
			break
		}
	}
	algo := summary.Connection.Algo
	if algo == "" {
		algo = summary.Algo
	}
	// what it does while it is not at the pool earns nothing
	if raw > 0 && summary.Connection.Uptime > 0 && algo != "" {
		worth, err := moneroOceanWorth.of(ctx, algo)
		if err != nil {
			return Sample{}, err
		}
		sample.Hashrate = raw * worth
	}
	return Sample{Time: time.Now(), Algorithms: map[string]AlgoSample{"": sample}}, nil
}

// moneroOceanRates remembers what a hash of each algorithm is worth at the
// pool, in hashes of Monero's algorithm.
type moneroOceanRates struct {
	mu      sync.Mutex
	worth   map[string]float64
	fetched time.Time
}

var moneroOceanWorth = &moneroOceanRates{}

const moneroAlgorithm = "rx/0"

// of returns what one hash of algo is worth. Figures that could not be
// renewed are used on: an old ratio is better than a miner that seems to
// stand still. Without any, only Monero's own algorithm has a worth.
func (r *moneroOceanRates) of(ctx context.Context, algo string) (float64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var failed error
	if r.worth == nil || time.Since(r.fetched) >= moneroOceanStatsFor {
		worth, err := fetchMoneroOceanWorth(ctx)
		if err == nil {
			r.worth = worth
		}
		failed = err
		// a failure is not tried again with every look at the miner
		r.fetched = time.Now()
	}
	if w, ok := r.worth[algo]; ok {
		return w, nil
	}
	if algo == moneroAlgorithm {
		return 1, nil
	}
	if failed != nil {
		return 0, fmt.Errorf("what %s is worth at moneroocean: %w", algo, failed)
	}
	if r.worth == nil {
		return 0, fmt.Errorf("what %s is worth at moneroocean is not known", algo)
	}
	// the pool has no coin for it that pays: it earns nothing
	return 0, nil
}

func fetchMoneroOceanWorth(ctx context.Context) (map[string]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, moneroOceanStatsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hashsteer")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	var stats struct {
		Pool struct {
			Coins map[string]struct {
				Symbol string  `json:"symbol"`
				Algo   string  `json:"algo"`
				Active bool    `json:"active"`
				Profit float64 `json:"profit"`
			} `json:"coins"`
		} `json:"pool_statistics"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&stats); err != nil {
		return nil, err
	}
	// the pool gives a miner the coin of an algorithm that pays best
	best := map[string]float64{}
	monero := 0.0
	for _, c := range stats.Pool.Coins {
		if !c.Active || c.Profit <= 0 {
			continue
		}
		best[c.Algo] = max(best[c.Algo], c.Profit)
		if c.Symbol == "XMR" && c.Algo == moneroAlgorithm {
			monero = c.Profit
		}
	}
	if monero <= 0 {
		return nil, errors.New("the pool names no earning for Monero")
	}
	for algo, profit := range best {
		best[algo] = profit / monero
	}
	return best, nil
}
