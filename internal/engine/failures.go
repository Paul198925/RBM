package engine

import (
	"encoding/json"
	"time"
)

// Benchmarks that came to nothing are remembered across restarts. Each
// failure makes the wait before the next try longer, and after
// maxBenchmarkFailures the combination is given up: it is not tried again
// by itself until the miner program has a new version or the user asks.
const (
	failureNote          = "failure/"
	maxBenchmarkFailures = 3
)

// benchmarkPenalties are the waits after the first and the second failure;
// the third is followed by giving up.
var benchmarkPenalties = []time.Duration{6 * time.Hour, 24 * time.Hour}

// failure is what is remembered about a miner on an algorithm that could
// not be measured.
type failure struct {
	Count int `json:"count"`
	// Last is when it failed the last time.
	Last   time.Time `json:"last"`
	Reason string    `json:"reason"`
	// Version is the miner program's version it failed with: another
	// version gets a fresh start.
	Version string `json:"version"`
}

// gaveUp reports whether it is no longer tried by itself.
func (f failure) gaveUp() bool { return f.Count >= maxBenchmarkFailures }

// until is when it may be tried again. For one that is given up this is
// never, told as a time far away.
func (f failure) until() time.Time {
	if f.gaveUp() {
		return f.Last.AddDate(100, 0, 0)
	}
	return f.Last.Add(benchmarkPenalties[max(f.Count, 1)-1])
}

// loadFailures reads the remembered failures once.
func (e *Engine) loadFailures() error {
	if e.failures != nil {
		return nil
	}
	notes, err := e.deps.Store.Notes(failureNote)
	if err != nil {
		return err
	}
	e.failures = map[string]failure{}
	for hashKey, text := range notes {
		var f failure
		// a note that cannot be read is no failure
		if json.Unmarshal([]byte(text), &f) == nil && f.Count > 0 {
			e.failures[hashKey] = f
		}
	}
	return nil
}

// noteFailure counts one more failed benchmark and remembers it.
func (e *Engine) noteFailure(hashKey, version string, now time.Time, reason string) failure {
	f := e.failures[hashKey]
	if f.Version != version {
		f = failure{}
	}
	f.Count++
	f.Last, f.Reason, f.Version = now, reason, version
	if e.failures == nil {
		e.failures = map[string]failure{}
	}
	e.failures[hashKey] = f
	if text, err := json.Marshal(f); err == nil {
		if err := e.deps.Store.SetNote(failureNote+hashKey, string(text)); err != nil {
			e.deps.Logf("remembering the failure of %s: %v", hashKey, err)
		}
	}
	return f
}

// forgetFailure gives a miner on an algorithm a fresh start.
func (e *Engine) forgetFailure(hashKey string) {
	if _, ok := e.failures[hashKey]; !ok {
		return
	}
	delete(e.failures, hashKey)
	if err := e.deps.Store.SetNote(failureNote+hashKey, ""); err != nil {
		e.deps.Logf("forgetting the failure of %s: %v", hashKey, err)
	}
}

// recallFailure takes a miner on an algorithm out again that failed before
// the program was started, as long as its wait is not over. A failure with
// another version of the miner program no longer counts.
func (e *Engine) recallFailure(g *group, hashKey, version string, now time.Time) {
	f, ok := e.failures[hashKey]
	if !ok {
		return
	}
	if f.Version != version {
		e.deps.Logf("%s: %s has a new version (%s), its %d failed benchmarks no longer count", g.name, hashKey, version, f.Count)
		e.forgetFailure(hashKey)
		return
	}
	if _, out := g.disabled[hashKey]; out || !now.Before(f.until()) {
		return
	}
	g.disabled[hashKey] = f.until()
	if g.reasons[hashKey] == "" {
		g.reasons[hashKey] = f.Reason
	}
}
