package engine

import (
	"strings"
	"testing"
	"time"

	"hashsteer/internal/stats"
)

// A pool that switches coins makes the miner stand still while it rebuilds
// its DAG, so the intervals of a benchmark differ a lot. They all count.
func TestBenchmarkFinishesDespiteUnevenIntervals(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.advance(time.Minute)
	for i := 0; i < 8; i++ {
		// a good minute, then one with the miner mostly rebuilding
		s.miners[0].hashrate = []float64{25e6, 9e6}[i%2]
		s.advance(time.Minute)
	}
	if s.engine.groups[0].run.benchmark {
		t.Fatalf("still benchmarking after nine minutes:\n%s", strings.Join(s.logs, "\n"))
	}
	st, ok, _ := s.store.Load(kawpowID)
	// the average is what the miner really delivered, pauses included
	if !ok || st.Duration < 3*time.Minute || st.Average(stats.Week) < 12e6 || st.Average(stats.Week) > 22e6 {
		t.Errorf("stat = %+v", st)
	}
	if s.logged("was rejected") {
		t.Errorf("samples were rejected during the benchmark:\n%s", strings.Join(s.logs, "\n"))
	}
}

// What a benchmark has measured is kept when the program stops; the next
// start continues instead of starting over.
func TestBenchmarkContinuesAfterARestart(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	// stopped after 3 1/2 minutes: 90 s of warm-up, 2 minutes measured
	s.advance(3*time.Minute + 30*time.Second)
	st, ok, _ := s.store.Load(kawpowID)
	if !ok || st.Duration != 90*time.Second {
		t.Fatalf("measured before the restart: %v (%v)", st.Duration, ok)
	}
	s.engine.stopAll(s.now)

	// a new engine on the same statistics
	again := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	again.engine.deps.Store = s.store
	again.store = s.store
	again.now, again.next = s.now, s.now
	again.advance(time.Minute)
	c := again.engine.Status().Groups[0].Candidates[0]
	// stopping stored the half minute that had not been stored yet
	if c.Benchmarked || c.MeasuredSeconds != 120 || again.engine.Status().BenchmarkSeconds != 180 {
		t.Errorf("progress after the restart: %+v", c)
	}

	// 90 s were missing: after the warm-up and two more rounds it is done,
	// well before a benchmark from scratch would be
	again.advance(3*time.Minute + 30*time.Second)
	if again.engine.groups[0].run.benchmark {
		t.Errorf("not finished:\n%s", strings.Join(again.logs, "\n"))
	}
}
