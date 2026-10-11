package miner

import (
	"context"
	"time"
)

// trexAPI reads T-Rex's /summary document.
type trexAPI struct{}

type trexSummary struct {
	// Hashrate is in hashes per second, over all cards.
	Hashrate float64 `json:"hashrate"`
	Accepted int64   `json:"accepted_count"`
	Rejected int64   `json:"rejected_count"`
	Invalid  int64   `json:"invalid_count"`
	GPUs     []struct {
		Power float64 `json:"power"`
	} `json:"gpus"`
}

func (trexAPI) Poll(ctx context.Context, port int) (Sample, error) {
	var summary trexSummary
	if err := getJSON(ctx, port, "/summary", &summary); err != nil {
		return Sample{}, err
	}
	sample := Sample{Time: time.Now(), Algorithms: map[string]AlgoSample{"": {
		Hashrate: summary.Hashrate,
		Accepted: summary.Accepted,
		Rejected: summary.Rejected,
		Invalid:  summary.Invalid,
	}}}
	for _, gpu := range summary.GPUs {
		sample.PowerWatts += gpu.Power
	}
	return sample, nil
}
