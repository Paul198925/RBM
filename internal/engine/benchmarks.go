package engine

import (
	"sort"
	"time"

	"hashsteer/internal/device"
	"hashsteer/internal/estimate"
	"hashsteer/internal/miner"
	"hashsteer/internal/stats"
)

// The states a benchmark can be in.
const (
	BenchOK       = "ok"           // measured
	BenchEstimate = "estimated"    // not measured here; a figure from another machine stands in
	BenchOutdated = "outdated"     // measured with another version of the miner program
	BenchRunning  = "benchmarking" // being measured right now
	BenchPending  = "pending"      // not measured yet, or not long enough
	BenchFailed   = "failed"       // taken out after failures
	BenchGaveUp   = "gave up"      // failed so often that it is not tried again by itself
	BenchNoOffer  = "no offer"     // no pool pays for the algorithm, so it cannot be measured
	BenchNoFit    = "no fit"       // the devices lack the memory for it
	BenchExcluded = "excluded"     // switched off in the configuration
)

// BenchmarkStatus is what is known about one miner program on one algorithm
// with one device group.
type BenchmarkStatus struct {
	HashKey   string   `json:"hash_key"`
	Group     string   `json:"group"`
	Devices   []string `json:"devices"`
	Miner     string   `json:"miner"`
	Version   string   `json:"version"`
	Algorithm string   `json:"algorithm"`
	Status    string   `json:"status"`
	// Hashrate and PowerWatts are the stored averages, 0 without a
	// measurement.
	Hashrate        float64 `json:"hashrate"`
	PowerWatts      float64 `json:"power_watts"`
	MeasuredSeconds float64 `json:"measured_seconds"`
	// Updated is when the hashrate was last measured.
	Updated *time.Time `json:"updated,omitempty"`
	// RejectedPercent is from the last run, nil if it had no shares.
	RejectedPercent *float64 `json:"rejected_percent,omitempty"`
	// MeasuredVersion is the miner version of the measurement, if it is
	// not the current one.
	MeasuredVersion string `json:"measured_version,omitempty"`
	// FailedUntil is when a failed one is tried again.
	FailedUntil *time.Time `json:"failed_until,omitempty"`
	// FailReason says how a failed one failed, and what may be missing.
	FailReason string `json:"fail_reason,omitempty"`
	// Failures is how many benchmarks of it came to nothing.
	Failures int `json:"failures,omitempty"`
	// Log is the newest log file of this combination. The engine leaves
	// it empty.
	Log string `json:"log,omitempty"`
}

const versionNote = "version/"

// Benchmarks returns the benchmark list as of the last round. It is safe to
// call from any goroutine.
func (e *Engine) Benchmarks() []BenchmarkStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.benchmarks
}

// listBenchmarks builds the benchmark list: every miner program that exists
// for a group's devices, on every algorithm it knows.
func (e *Engine) listBenchmarks(now time.Time) error {
	if e.versions == nil {
		versions, err := e.deps.Store.Notes(versionNote)
		if err != nil {
			return err
		}
		e.versions = versions
	}
	lastRuns, err := e.deps.Store.LastRuns()
	if err != nil {
		return err
	}

	list := []BenchmarkStatus{}
	for _, g := range e.groups {
		for _, name := range miner.Names(e.deps.Definitions) {
			def := e.deps.Definitions[name]
			if def.Vendor != g.vendor || !def.Available() {
				continue
			}
			for _, a := range def.Algorithms {
				hashKey := stats.HashrateKey(def.Name, g.deviceIDs(), a.Name)
				b := BenchmarkStatus{
					HashKey:   hashKey,
					Group:     g.name,
					Devices:   g.deviceIDs(),
					Miner:     def.Name,
					Version:   def.Version,
					Algorithm: a.Name,
					Status:    BenchPending,
				}

				hashrate, measured, err := e.deps.Store.Load(hashKey)
				if err != nil {
					return err
				}
				if measured {
					b.Hashrate = hashrate.Average(stats.Week)
					b.MeasuredSeconds = hashrate.Duration.Seconds()
					updated := hashrate.Updated
					b.Updated = &updated
				}
				if power, ok, err := e.deps.Store.Load(powerKeyFor(hashKey)); err != nil {
					return err
				} else if ok {
					b.PowerWatts = power.Average(stats.Week)
				}
				if run, ok := lastRuns[stats.RunKey(g.name, def.Name, a.Name)]; ok && run.Accepted+run.Rejected > 0 {
					percent := 100 * float64(run.Rejected) / float64(run.Accepted+run.Rejected)
					b.RejectedPercent = &percent
				}

				done := measured && hashrate.Duration >= e.opts.BenchmarkTime
				guess, guessed := e.estimate(g, def, a)
				until, failed := g.disabled[hashKey]
				switch {
				case !e.allowed(def, a.Name):
					b.Status = BenchExcluded
				case !e.fits(g, a):
					b.Status = BenchNoFit
				case e.isBroken(def.Name):
					b.Status = BenchFailed
					b.FailReason = "the miner program cannot run on this machine: " + e.broken[def.Name].Reason
				case failed && e.failures[hashKey].gaveUp():
					b.Status = BenchGaveUp
					b.FailReason = g.reasons[hashKey]
					b.Failures = e.failures[hashKey].Count
				case failed:
					b.Status = BenchFailed
					b.FailedUntil = &until
					b.FailReason = g.reasons[hashKey]
					b.Failures = e.failures[hashKey].Count
				case g.run != nil && g.run.hashKey == hashKey && g.run.benchmark:
					b.Status = BenchRunning
				case done && e.versions[hashKey] != "" && e.versions[hashKey] != def.Version:
					b.Status = BenchOutdated
					b.MeasuredVersion = e.versions[hashKey]
				case done:
					b.Status = BenchOK
				case guessed:
					b.Status = BenchEstimate
					b.Hashrate = guess.Hashrate
					if b.PowerWatts == 0 {
						b.PowerWatts = guess.PowerWatts
					}
				case len(e.offersFor(a.Name, now)) == 0:
					b.Status = BenchNoOffer
				}
				list = append(list, b)
			}
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Miner != list[j].Miner {
			return list[i].Miner < list[j].Miner
		}
		if list[i].Algorithm != list[j].Algorithm {
			return list[i].Algorithm < list[j].Algorithm
		}
		return list[i].Group < list[j].Group
	})

	e.mu.Lock()
	e.benchmarks = list
	e.mu.Unlock()
	return nil
}

// noteVersion remembers which version of the miner program a measurement
// was made with.
func (e *Engine) noteVersion(hashKey, version string) error {
	if e.versions[hashKey] == version {
		return nil
	}
	if err := e.deps.Store.SetNote(versionNote+hashKey, version); err != nil {
		return err
	}
	if e.versions == nil {
		e.versions = map[string]string{}
	}
	e.versions[hashKey] = version
	return nil
}

// Which benchmarks ResetBenchmarks is to forget.
const (
	ResetFailed   = "failed"
	ResetOutdated = "outdated"
	ResetAll      = "all"
)

// ResetBenchmarks forgets what was measured, so that it is measured again:
// for the given hash keys, or for a whole class of benchmarks. A failed one
// is also let back in at once. It is safe to call from any goroutine.
func (e *Engine) ResetBenchmarks(which string, hashKeys []string) {
	e.do(func() {
		keys := hashKeys
		if which != "" {
			keys = nil
			for _, b := range e.Benchmarks() {
				switch {
				case which == ResetFailed && (b.Status == BenchFailed || b.Status == BenchGaveUp),
					which == ResetOutdated && b.Status == BenchOutdated,
					which == ResetAll && (b.MeasuredSeconds > 0 || b.Status == BenchFailed || b.Status == BenchGaveUp):
					keys = append(keys, b.HashKey)
				}
			}
		}
		for _, key := range keys {
			for _, stored := range []string{key, powerKeyFor(key)} {
				if err := e.deps.Store.Delete(stored); err != nil {
					e.deps.Logf("reset %s: %v", stored, err)
					return
				}
			}
			if err := e.deps.Store.SetNote(versionNote+key, ""); err != nil {
				e.deps.Logf("reset %s: %v", key, err)
				return
			}
			delete(e.versions, key)
			e.forgetFailure(key)
			for _, g := range e.groups {
				delete(g.disabled, key)
				delete(g.crashes, key)
			}
		}
		switch len(keys) {
		case 0:
			e.deps.Logf("no benchmark to reset")
		case 1:
			e.deps.Logf("benchmark of %s was reset", keys[0])
		default:
			e.deps.Logf("%d benchmarks were reset", len(keys))
		}
	})
}

// ExportEstimates returns what this machine has measured, per device, in
// the form estimates are shared in. Only finished measurements count.
func ExportEstimates(store *stats.Store, definitions map[string]*miner.Definition, devices []device.Device, benchmarkTime time.Duration) ([]estimate.Entry, error) {
	versions, err := store.Notes(versionNote)
	if err != nil {
		return nil, err
	}
	var entries []estimate.Entry
	for _, g := range groupDevices(devices, true) {
		n := float64(len(g.devices))
		for _, name := range miner.Names(definitions) {
			def := definitions[name]
			if def.Vendor != g.vendor {
				continue
			}
			for _, a := range def.Algorithms {
				hashKey := stats.HashrateKey(def.Name, g.deviceIDs(), a.Name)
				hashrate, ok, err := store.Load(hashKey)
				if err != nil {
					return nil, err
				}
				if !ok || hashrate.Duration < benchmarkTime || hashrate.Average(stats.Week) <= 0 {
					continue
				}
				entry := estimate.Entry{
					Device:    g.devices[0].Model,
					Miner:     def.Name,
					Version:   versions[hashKey],
					Algorithm: a.Name,
					Hashrate:  hashrate.Average(stats.Week) / n,
				}
				if entry.Version == "" {
					entry.Version = def.Version
				}
				// a CPU's draw is assumed, not measured
				if power, ok, err := store.Load(powerKeyFor(hashKey)); err != nil {
					return nil, err
				} else if ok && g.vendor != miner.VendorCPU {
					entry.PowerWatts = power.Average(stats.Week) / n
				}
				entries = append(entries, entry)
			}
		}
	}
	return entries, nil
}
