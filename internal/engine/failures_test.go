package engine

import (
	"strings"
	"testing"
	"time"

	"hashsteer/internal/selector"
)

// jump moves the clock on without playing the time in between, and has the
// engine look at once.
func (s *sim) jump(d time.Duration) {
	s.now = s.now.Add(d)
	s.next = s.now
}

func TestFailedBenchmarksWaitLongerAndAreGivenUp(t *testing.T) {
	// a miner that starts and never reports a hashrate
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.hashrates["kawpow"] = 0

	// first failure: six hours
	s.advance(8 * time.Minute)
	b := s.bench(kawpowID)
	if b.Status != BenchFailed || b.Failures != 1 || b.FailedUntil == nil || !strings.Contains(b.FailReason, "no hashrate") {
		t.Fatalf("after the first failure: %+v", b)
	}
	if wait := b.FailedUntil.Sub(s.now); wait < 5*time.Hour+50*time.Minute || wait > 6*time.Hour {
		t.Errorf("first wait = %v", wait)
	}
	if len(s.launches) != 1 {
		t.Fatalf("launches = %v", s.launches)
	}

	// second failure: a day
	s.jump(6 * time.Hour)
	s.advance(8 * time.Minute)
	b = s.bench(kawpowID)
	if b.Status != BenchFailed || b.Failures != 2 || len(s.launches) != 2 {
		t.Fatalf("after the second failure: %+v, %d launches", b, len(s.launches))
	}
	if wait := b.FailedUntil.Sub(s.now); wait < 23*time.Hour+50*time.Minute || wait > 24*time.Hour {
		t.Errorf("second wait = %v", wait)
	}
	// it is not tried while it waits
	s.jump(12 * time.Hour)
	s.advance(3 * time.Minute)
	if len(s.launches) != 2 {
		t.Errorf("tried during the wait: %d launches", len(s.launches))
	}

	// third failure: given up
	s.jump(12 * time.Hour)
	s.advance(8 * time.Minute)
	b = s.bench(kawpowID)
	if b.Status != BenchGaveUp || b.Failures != 3 || b.FailedUntil != nil || len(s.launches) != 3 {
		t.Fatalf("after the third failure: %+v, %d launches", b, len(s.launches))
	}
	d := s.engine.Status().Groups[0].Disabled
	if len(d) != 1 || !d[0].GaveUp || d[0].Failures != 3 {
		t.Errorf("disabled = %+v", d)
	}
	if !strings.Contains(strings.Join(s.logs, "\n"), "giving up on it") {
		t.Errorf("logs: %v", s.logs)
	}
	// and stays so, however long it runs
	s.jump(60 * 24 * time.Hour)
	s.advance(3 * time.Minute)
	if b = s.bench(kawpowID); b.Status != BenchGaveUp || len(s.launches) != 3 {
		t.Errorf("two months later: %+v, %d launches", b, len(s.launches))
	}
}

func TestFailuresSurviveARestart(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.hashrates["kawpow"] = 0
	s.advance(8 * time.Minute)
	if b := s.bench(kawpowID); b.Status != BenchFailed || b.Failures != 1 {
		t.Fatalf("before the restart: %+v", b)
	}
	s.engine.stopAll(s.now)

	// a new engine on the same statistics, an hour later
	again := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	again.hashrates["kawpow"] = 0
	again.engine.deps.Store, again.store = s.store, s.store
	again.now = s.now.Add(time.Hour)
	again.next = again.now
	again.advance(3 * time.Minute)
	b := again.bench(kawpowID)
	if b.Status != BenchFailed || b.Failures != 1 || !strings.Contains(b.FailReason, "no hashrate") || len(again.launches) != 0 {
		t.Fatalf("after the restart: %+v, launches %v", b, again.launches)
	}
	// the wait runs from the failure, not from the restart
	if wait := b.FailedUntil.Sub(again.now); wait > 5*time.Hour {
		t.Errorf("wait after the restart = %v", wait)
	}

	// once it is over, the next failure is the second
	again.jump(5 * time.Hour)
	again.advance(8 * time.Minute)
	if b = again.bench(kawpowID); b.Failures != 2 || len(again.launches) != 1 {
		t.Errorf("second failure after the restart: %+v, launches %v", b, again.launches)
	}
}

func TestAFailureIsForgotten(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.hashrates["kawpow"] = 0
	fail := func() {
		t.Helper()
		s.advance(8 * time.Minute)
		if b := s.bench(kawpowID); b.Status != BenchFailed || b.Failures != 1 {
			t.Fatalf("after the failure: %+v", b)
		}
	}
	stored := func() int {
		notes, err := s.store.Notes(failureNote)
		if err != nil {
			t.Fatal(err)
		}
		return len(notes)
	}

	// by a try the user asks for, which then works
	fail()
	if stored() != 1 {
		t.Fatalf("%d failures stored", stored())
	}
	s.hashrates["kawpow"] = 25e6
	s.engine.Enable(kawpowID)
	s.next = s.now
	s.advance(10 * time.Minute)
	if b := s.bench(kawpowID); b.Status != BenchOK || b.Failures != 0 || stored() != 0 {
		t.Errorf("after a good benchmark: %+v, %d stored", b, stored())
	}

	// by resetting the failed benchmarks
	s = newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.hashrates["kawpow"] = 0
	fail()
	s.engine.ResetBenchmarks(ResetFailed, nil)
	s.next = s.now
	s.advance(time.Minute)
	if stored() != 0 || len(s.launches) != 2 {
		t.Errorf("after the reset: %d stored, launches %v", stored(), s.launches)
	}

	// by resetting the watchdog
	s = newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.hashrates["kawpow"] = 0
	fail()
	s.engine.ResetWatchdog()
	s.next = s.now
	s.advance(time.Minute)
	if stored() != 0 || len(s.launches) != 2 {
		t.Errorf("after the watchdog reset: %d stored, launches %v", stored(), s.launches)
	}
}

func TestANewMinerVersionGetsAFreshStart(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.hashrates["kawpow"] = 0
	// given up with the version before this one
	for i := 0; i < maxBenchmarkFailures; i++ {
		s.engine.noteFailure(kawpowID, "1.0.0", s.now, "reported no hashrate for 5m0s")
	}
	s.engine.failures = nil // as after a restart
	s.advance(2 * time.Minute)
	if len(s.launches) != 1 || !strings.Contains(strings.Join(s.logs, "\n"), "has a new version") {
		t.Errorf("launches %v, logs %v", s.launches, s.logs)
	}
	if notes, _ := s.store.Notes(failureNote); len(notes) != 0 {
		t.Errorf("the old failures are still stored: %v", notes)
	}
}

func TestAMinerThatKeepsExitingInItsBenchmarkCountsAsFailed(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.advance(time.Minute)
	// it exits at once, three times in a row, without ever being measured
	for i := 0; i < maxCrashes; i++ {
		s.miners[len(s.miners)-1].exit()
		s.advance(20 * time.Second)
	}
	b := s.bench(kawpowID)
	if b.Status != BenchFailed || b.Failures != 1 || !strings.Contains(b.FailReason, "exited on its own") {
		t.Fatalf("after three exits: %+v", b)
	}
	// that is a failed benchmark with its long wait, not a bad hour
	if wait := b.FailedUntil.Sub(s.now); wait < 5*time.Hour {
		t.Errorf("wait = %v", wait)
	}
}

func TestAMeasuredMinerThatCrashesOnlySitsOutAnHour(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	s.advance(10 * time.Minute)
	if b := s.bench(kawpowID); b.Status != BenchOK {
		t.Fatalf("benchmark: %+v", b)
	}
	for i := 0; i < maxCrashes; i++ {
		s.miners[len(s.miners)-1].exit()
		s.advance(20 * time.Second)
	}
	d := s.engine.Status().Groups[0].Disabled
	if len(d) != 1 || d[0].Failures != 0 || d[0].GaveUp || d[0].Until.Sub(s.now) > crashPenalty {
		t.Fatalf("disabled = %+v", d)
	}
	if notes, _ := s.store.Notes(failureNote); len(notes) != 0 {
		t.Errorf("a crash while mining was stored as a failed benchmark: %v", notes)
	}
	// its measurement is kept
	if b := s.bench(kawpowID); b.Hashrate != 25e6 {
		t.Errorf("benchmark after the crashes: %+v", b)
	}
}

func TestFailureWaits(t *testing.T) {
	last := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	for count, want := range map[int]time.Duration{1: 6 * time.Hour, 2: 24 * time.Hour} {
		f := failure{Count: count, Last: last}
		if f.gaveUp() || f.until().Sub(last) != want {
			t.Errorf("%d failures: gave up %v, wait %v", count, f.gaveUp(), f.until().Sub(last))
		}
	}
	for _, count := range []int{3, 4, 10} {
		f := failure{Count: count, Last: last}
		if !f.gaveUp() || f.until().Sub(last) < 50*365*24*time.Hour {
			t.Errorf("%d failures: gave up %v, until %v", count, f.gaveUp(), f.until())
		}
	}
	// an estimate is no measurement of this machine
	measured := selector.Candidate{Benchmarked: true}
	if !unmeasured(option{}) || unmeasured(option{Candidate: measured}) || !unmeasured(option{Candidate: measured, estimated: true}) {
		t.Error("unmeasured is wrong")
	}
}
