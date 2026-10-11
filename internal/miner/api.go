package miner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Sample is one reading of a running miner's statistics.
type Sample struct {
	Time time.Time
	// Algorithms is keyed by the algorithm name as the miner spells it.
	Algorithms map[string]AlgoSample
	// PowerWatts is the power draw the miner reports, 0 if it does not.
	PowerWatts float64
}

// For returns the figures for an algorithm. Miners that mine one algorithm
// and do not name it in their statistics report it under the empty name.
func (s Sample) For(algorithm string) AlgoSample {
	if a, ok := s.Algorithms[algorithm]; ok {
		return a
	}
	return s.Algorithms[""]
}

type AlgoSample struct {
	Hashrate float64 // hashes per second
	Accepted int64
	Rejected int64
	Invalid  int64
}

// API reads the statistics of a miner program that listens on a local port.
type API interface {
	Poll(ctx context.Context, port int) (Sample, error)
}

// NewAPI returns the adapter for the API kind named in a definition.
func NewAPI(kind string) (API, error) {
	switch kind {
	case "rigel":
		return rigelAPI{}, nil
	case "xmrig":
		return xmrigAPI{path: "/2/summary"}, nil
	case "wildrig":
		// WildRig answers in XMRig's format, at the root of its API port
		return xmrigAPI{path: "/"}, nil
	case "ccminer":
		return ccminerAPI{}, nil
	case "srbminer":
		return srbminerAPI{}, nil
	}
	return nil, fmt.Errorf("unknown miner api %q", kind)
}

var apiClient = &http.Client{Timeout: 10 * time.Second}

// getJSON fetches a document from a miner's local HTTP API.
func getJSON(ctx context.Context, port int, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		return err
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("miner api: status %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

// rigelAPI reads Rigel's /stat document.
type rigelAPI struct{}

type rigelStat struct {
	Hashrate     map[string]float64 `json:"hashrate"`
	PowerUsage   float64            `json:"power_usage"`
	SolutionStat map[string]struct {
		Accepted int64 `json:"accepted"`
		Rejected int64 `json:"rejected"`
		Invalid  int64 `json:"invalid"`
	} `json:"solution_stat"`
}

func (rigelAPI) Poll(ctx context.Context, port int) (Sample, error) {
	var stat rigelStat
	if err := getJSON(ctx, port, "/stat", &stat); err != nil {
		return Sample{}, err
	}
	sample := Sample{
		Time:       time.Now(),
		Algorithms: make(map[string]AlgoSample, len(stat.Hashrate)),
		PowerWatts: stat.PowerUsage,
	}
	for name, hashrate := range stat.Hashrate {
		shares := stat.SolutionStat[name]
		sample.Algorithms[name] = AlgoSample{
			Hashrate: hashrate,
			Accepted: shares.Accepted,
			Rejected: shares.Rejected,
			Invalid:  shares.Invalid,
		}
	}
	return sample, nil
}

// xmrigAPI reads the summary document of XMRig and of miners that copied
// its API.
type xmrigAPI struct {
	path string
}

type xmrigSummary struct {
	Hashrate struct {
		// averages over 10 seconds, 1 minute and 15 minutes; null until
		// the miner has run that long
		Total []*float64 `json:"total"`
	} `json:"hashrate"`
	Results struct {
		SharesGood  float64 `json:"shares_good"`
		SharesTotal float64 `json:"shares_total"`
	} `json:"results"`
}

func (x xmrigAPI) Poll(ctx context.Context, port int) (Sample, error) {
	var summary xmrigSummary
	if err := getJSON(ctx, port, x.path, &summary); err != nil {
		return Sample{}, err
	}
	var hashrate float64
	for _, total := range summary.Hashrate.Total {
		if total != nil && *total > 0 {
			hashrate = *total
			break
		}
	}
	return Sample{
		Time: time.Now(),
		Algorithms: map[string]AlgoSample{"": {
			Hashrate: hashrate,
			Accepted: int64(summary.Results.SharesGood),
			Rejected: int64(summary.Results.SharesTotal - summary.Results.SharesGood),
		}},
	}, nil
}

// Window collects the samples of one miner run.
type Window struct {
	samples []Sample
}

func (w *Window) Add(s Sample) { w.samples = append(w.samples, s) }

// Last returns the most recent sample.
func (w *Window) Last() (Sample, bool) {
	if len(w.samples) == 0 {
		return Sample{}, false
	}
	return w.samples[len(w.samples)-1], true
}

// Average returns the mean hashrate and power draw of the samples taken
// after since, and how many there were. Samples in which the miner did not
// report the algorithm count as zero hashrate.
func (w *Window) Average(algorithm string, since time.Time) (hashrate, power float64, n int) {
	for _, s := range w.samples {
		if !s.Time.After(since) {
			continue
		}
		hashrate += s.For(algorithm).Hashrate
		power += s.PowerWatts
		n++
	}
	if n == 0 {
		return 0, 0, 0
	}
	return hashrate / float64(n), power / float64(n), n
}

// Prune drops the samples taken before t.
func (w *Window) Prune(t time.Time) {
	i := 0
	for i < len(w.samples) && w.samples[i].Time.Before(t) {
		i++
	}
	w.samples = append(w.samples[:0], w.samples[i:]...)
}
