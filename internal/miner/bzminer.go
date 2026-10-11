package miner

import (
	"context"
	"time"
)

// bzminerAPI reads BzMiner's /status document, which lists every device
// with one figure per algorithm it mines.
type bzminerAPI struct{}

type bzminerStatus struct {
	Devices []struct {
		Hashrate []float64 `json:"hashrate"`
		Valid    []int64   `json:"valid_solutions"`
		Rejected []int64   `json:"rejected_solutions"`
		Stale    []int64   `json:"stale_solutions"`
		Power    float64   `json:"power"`
	} `json:"devices"`
}

func (bzminerAPI) Poll(ctx context.Context, port int) (Sample, error) {
	var status bzminerStatus
	if err := getJSON(ctx, port, "/status", &status); err != nil {
		return Sample{}, err
	}
	first := func(list []int64) int64 {
		if len(list) == 0 {
			return 0
		}
		return list[0]
	}
	var total AlgoSample
	sample := Sample{Time: time.Now()}
	for _, d := range status.Devices {
		// the list also holds the devices that do not mine
		if len(d.Hashrate) == 0 || d.Hashrate[0] <= 0 {
			continue
		}
		total.Hashrate += d.Hashrate[0]
		total.Accepted += first(d.Valid)
		total.Rejected += first(d.Rejected)
		total.Invalid += first(d.Stale)
		sample.PowerWatts += d.Power
	}
	sample.Algorithms = map[string]AlgoSample{"": total}
	return sample, nil
}
