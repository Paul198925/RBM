package engine

import (
	"strings"
	"testing"
	"time"
)

// A miner that reports a hashrate while the card stands still and no share
// comes in does not mine. It is stopped, and nothing it said is kept.
func TestAMinerThatOnlyClaimsToMineIsStopped(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.load = map[int]float64{0: 3}
	s.noShares = true

	s.advance(4 * time.Minute)
	if b := s.bench(kawpowID); b.Status != BenchRunning || b.MeasuredSeconds != 0 {
		t.Fatalf("after four minutes: %+v", b)
	}
	s.advance(5 * time.Minute)
	b := s.bench(kawpowID)
	if b.Status != BenchFailed || b.Failures != 1 || b.MeasuredSeconds != 0 || b.Hashrate != 0 ||
		!strings.Contains(b.FailReason, "the devices did nothing") {
		t.Fatalf("after nine minutes: %+v", b)
	}
	if len(s.miners) != 1 || !s.miners[0].stopped {
		t.Errorf("the miner was not stopped: %d launches", len(s.miners))
	}
	if !strings.Contains(strings.Join(s.logs, "\n"), "reports a hashrate, but for") {
		t.Errorf("logs: %v", s.logs)
	}
}

// A hashrate that was stored from such a run is dropped when the miner
// turns out not to work.
func TestAHashrateThatCannotBeTrustedIsDropped(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	// without a reading of the card the miner is believed, as it was
	// before the card was looked at
	s.noShares = true
	s.advance(10 * time.Minute)
	if b := s.bench(kawpowID); b.Status != BenchOK || b.Hashrate == 0 {
		t.Fatalf("believed without a reading: %+v", b)
	}
	s.engine.stopAll(s.now)

	again := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	again.engine.deps.Store, again.store = s.store, s.store
	again.now, again.next = s.now, s.now
	again.load = map[int]float64{0: 3}
	again.noShares = true
	again.advance(9 * time.Minute)
	b := again.bench(kawpowID)
	if b.Status != BenchFailed || b.Hashrate != 0 || b.MeasuredSeconds != 0 {
		t.Fatalf("with a card that stands still: %+v", b)
	}
	if !strings.Contains(strings.Join(again.logs, "\n"), "is dropped, it cannot be trusted") {
		t.Errorf("logs: %v", again.logs)
	}
}

// Shares or a busy card are each proof enough.
func TestAWorkingMinerIsLeftAlone(t *testing.T) {
	for name, set := range map[string]func(*sim){
		"busy card, no shares yet": func(s *sim) { s.load, s.noShares = map[int]float64{0: 99}, true },
		"no reading of the card":   func(s *sim) { s.noShares = true },
		"the card is not listed":   func(s *sim) { s.load, s.noShares = map[int]float64{1: 0}, true },
	} {
		s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
		set(s)
		s.advance(20 * time.Minute)
		if b := s.bench(kawpowID); b.Status != BenchOK || len(s.launches) != 1 {
			t.Errorf("%s: %+v, %d launches", name, b, len(s.launches))
		}
	}
}

// A card that stops working in the middle of a run is caught too, but what
// was measured while it worked stays.
func TestACardThatStopsWorkingIsCaught(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.load = map[int]float64{0: 99}
	s.noShares = true
	s.advance(10 * time.Minute)
	measured := s.bench(kawpowID)
	if measured.Status != BenchOK {
		t.Fatalf("while it worked: %+v", measured)
	}

	s.load = map[int]float64{0: 2}
	s.advance(7 * time.Minute)
	b := s.bench(kawpowID)
	if b.Status != BenchFailed || b.Hashrate == 0 {
		t.Fatalf("after it stopped working: %+v", b)
	}
	// the minutes without work were not measured: only the round that was
	// under way when it stopped, and the one before the last look
	if b.MeasuredSeconds > measured.MeasuredSeconds+120 {
		t.Errorf("measured %v s while it stood still, had %v s", b.MeasuredSeconds, measured.MeasuredSeconds)
	}
}
