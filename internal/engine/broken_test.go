package engine

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fatalMiner is a miner program that cannot start: it has ended by the
// time it is looked at, and says what it lacks.
type fatalMiner struct {
	*fakeMiner
	why string
}

func (f fatalMiner) Fatal() (string, string) {
	if f.why == "" {
		return "", ""
	}
	return f.why, "libcurl.so.4"
}

// A miner program that lacks a library fails the same way on every
// algorithm. It is taken out at once and as a whole, not algorithm by
// algorithm with three tries each.
func TestAMinerThatCannotStartIsTakenOutAsAWhole(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13, "sha512256d": 1e-15})
	const why = "the library libcurl.so.4 is missing. Install it with the package manager of your system."
	launch := s.engine.deps.Launch
	broken := true
	s.engine.deps.Launch = func(ctx context.Context, spec LaunchSpec) (Miner, error) {
		m, err := launch(ctx, spec)
		if err != nil || !broken {
			return m, err
		}
		fake := m.(*fakeMiner)
		fake.exit()
		return fatalMiner{fake, why}, nil
	}

	s.advance(5 * time.Minute)
	if len(s.launches) != 1 {
		t.Fatalf("it was started %d times: %v", len(s.launches), s.launches)
	}
	status := s.engine.Status()
	if len(status.Broken) != 1 || status.Broken[0].Miner != "rigel" || status.Broken[0].Reason != why || status.Broken[0].Needs != "libcurl.so.4" {
		t.Fatalf("broken = %+v", status.Broken)
	}
	if status.Groups[0].State != "idle" || len(status.Groups[0].Disabled) != 0 {
		t.Errorf("group = %+v", status.Groups[0])
	}
	// every algorithm of it says why, and none of them counts as a failed benchmark
	failed := 0
	for _, b := range s.engine.Benchmarks() {
		if b.Miner != "rigel" || b.Group != status.Groups[0].Name {
			continue
		}
		if b.Status == BenchFailed && strings.Contains(b.FailReason, "libcurl.so.4") && b.Failures == 0 && b.FailedUntil == nil {
			failed++
		} else if b.Status != BenchNoOffer && b.Status != BenchNoFit && b.Status != BenchExcluded {
			t.Errorf("benchmark %s: %+v", b.Algorithm, b)
		}
	}
	if failed < 2 {
		t.Errorf("%d algorithms say why the miner is out", failed)
	}
	if !strings.Contains(strings.Join(s.logs, "\n"), "rigel cannot run on this machine: the library libcurl.so.4 is missing.") {
		t.Errorf("logs: %v", s.logs)
	}

	// it stays out, however long
	s.jump(48 * time.Hour)
	s.advance(3 * time.Minute)
	if len(s.launches) != 1 {
		t.Errorf("tried again by itself: %v", s.launches)
	}

	// the library is installed and the miner let back in: it gets its chance
	broken = false
	s.engine.Mend("rigel")
	s.advance(10 * time.Minute)
	if len(s.launches) < 2 || len(s.engine.Status().Broken) != 0 {
		t.Errorf("after the reset: launches %v, broken %+v", s.launches, s.engine.Status().Broken)
	}
}

// A miner that ends without saying that it lacks something is counted as
// before, algorithm by algorithm.
func TestAMinerThatJustExitsIsNotTakenOutAsAWhole(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13})
	launch := s.engine.deps.Launch
	s.engine.deps.Launch = func(ctx context.Context, spec LaunchSpec) (Miner, error) {
		m, err := launch(ctx, spec)
		if err == nil {
			m.(*fakeMiner).exit()
			return fatalMiner{m.(*fakeMiner), ""}, nil
		}
		return m, err
	}
	s.advance(5 * time.Minute)
	if len(s.engine.Status().Broken) != 0 || len(s.launches) != 3 {
		t.Errorf("broken %+v, launches %v", s.engine.Status().Broken, s.launches)
	}
}
