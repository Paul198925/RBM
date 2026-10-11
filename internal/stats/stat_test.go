package stats

import (
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

func TestFirstSampleInitialisesAllWindows(t *testing.T) {
	var s Stat
	if !s.Empty() {
		t.Fatal("zero value must be empty")
	}
	if got := s.Update(100, time.Minute, t0, 0.1); got != Accepted {
		t.Fatalf("outcome = %v", got)
	}
	for w := Min10; w < numWindows; w++ {
		if s.Average(w) != 100 || s.Fluctuation(w) != 0 {
			t.Errorf("window %d: avg %v fluct %v", w, s.Average(w), s.Fluctuation(w))
		}
	}
	if s.Duration != time.Minute || !s.Updated.Equal(t0) || s.Live != 100 {
		t.Errorf("state = %+v", s)
	}
}

func TestYoungStatIsRunningMean(t *testing.T) {
	var s Stat
	for i, v := range []float64{100, 200, 300} {
		s.Update(v, time.Minute, t0.Add(time.Duration(i)*time.Minute), 0)
	}
	// three minutes of data: every window still holds the plain mean
	for w := Min10; w < numWindows; w++ {
		if !near(s.Average(w), 200) {
			t.Errorf("window %d: avg %v, want 200", w, s.Average(w))
		}
	}
}

func TestOldStatWeighsBySpan(t *testing.T) {
	var s Stat
	s.Update(100, 8*24*time.Hour, t0, 0)
	s.Update(200, time.Hour, t0.Add(time.Hour), 0)

	want := map[Window]float64{
		Min10: 200,
		Hour:  200,
		Day:   100 + 100.0/24,
		Week:  100 + 100.0/168,
	}
	for w, v := range want {
		if !near(s.Average(w), v) {
			t.Errorf("window %d: avg %v, want %v", w, s.Average(w), v)
		}
	}
	// the sample was 100% off the old average
	if !near(s.Fluctuation(Week), 1.0/168) {
		t.Errorf("week fluctuation = %v", s.Fluctuation(Week))
	}
	if s.Live != 200 {
		t.Errorf("live = %v", s.Live)
	}
}

func TestOutOfBandIsRejected(t *testing.T) {
	var s Stat
	s.Update(100, 8*24*time.Hour, t0, 0.1)
	before := s

	// band is 90..120 for a tolerance of 10%
	for _, v := range []float64{89, 121} {
		c := before
		if got := c.Update(v, time.Minute, t0.Add(time.Minute), 0.1); got != Rejected {
			t.Errorf("value %v: outcome %v", v, got)
		}
		if c.Avg != before.Avg || !c.Updated.Equal(before.Updated) || c.Duration != before.Duration {
			t.Errorf("value %v: rejected sample changed the stat", v)
		}
		if c.Failed != failStep {
			t.Errorf("value %v: failed = %d", v, c.Failed)
		}
	}
	for _, v := range []float64{91, 119} {
		c := before
		if got := c.Update(v, time.Minute, t0.Add(time.Minute), 0.1); got != Accepted {
			t.Errorf("value %v: outcome %v", v, got)
		}
	}
}

func TestZeroIsRejectedWithoutStreak(t *testing.T) {
	var s Stat
	s.Update(100, 8*24*time.Hour, t0, 0.1)
	for i := 0; i < 5; i++ {
		if got := s.Update(0, time.Minute, t0.Add(time.Minute), 0.1); got != Rejected {
			t.Fatalf("outcome = %v", got)
		}
	}
	if s.FailedStreak != 0 || s.Failed != failMax || s.Avg[Week] != 100 {
		t.Errorf("state = %+v", s)
	}
}

func TestAgreeingRejectsResetTheStat(t *testing.T) {
	var s Stat
	s.Update(100, 8*24*time.Hour, t0, 0.1)

	got := []Outcome{
		s.Update(50, time.Minute, t0.Add(1*time.Minute), 0.1),
		s.Update(52, time.Minute, t0.Add(2*time.Minute), 0.1),
		s.Update(51, time.Minute, t0.Add(3*time.Minute), 0.1),
	}
	want := []Outcome{Rejected, Rejected, Reset}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("outcomes = %v, want %v", got, want)
		}
	}
	if s.Avg[Week] != 51 || s.Duration != time.Minute || s.Failed != 0 || s.FailedStreak != 0 {
		t.Errorf("state after reset = %+v", s)
	}
}

func TestDisagreeingRejectsDoNotReset(t *testing.T) {
	var s Stat
	s.Update(100, 8*24*time.Hour, t0, 0.1)
	for i, v := range []float64{50, 20, 50, 20, 50} {
		if got := s.Update(v, time.Minute, t0.Add(time.Duration(i+1)*time.Minute), 0.1); got != Rejected {
			t.Fatalf("value %v: outcome %v", v, got)
		}
	}
	if s.Avg[Week] != 100 || s.FailedStreak != 1 {
		t.Errorf("state = %+v", s)
	}
}

func TestAcceptedSampleDecaysFailed(t *testing.T) {
	var s Stat
	s.Update(100, 8*24*time.Hour, t0, 0.1)
	s.Update(50, time.Minute, t0, 0.1)
	if s.Failed != failStep {
		t.Fatalf("failed = %d", s.Failed)
	}
	s.Update(100, time.Minute, t0, 0.1)
	if s.Failed != failStep-1 || s.FailedValue != 0 || s.FailedStreak != 0 {
		t.Errorf("state = %+v", s)
	}
}

func TestNoToleranceAcceptsEverything(t *testing.T) {
	var s Stat
	s.Update(100, 8*24*time.Hour, t0, 0)
	if got := s.Update(100000, time.Minute, t0, 0); got != Accepted {
		t.Errorf("outcome = %v", got)
	}
}
