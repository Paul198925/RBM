// Package stats keeps time-weighted moving averages of measured values
// (miner hashrates, pool prices) and decides whether a new sample is plausible.
package stats

import (
	"math"
	"time"
)

// Window selects one of the moving averages of a Stat.
type Window int

const (
	Min10 Window = iota
	Hour
	Day
	Week
	numWindows
)

var windowLen = [numWindows]time.Duration{
	Min10: 10 * time.Minute,
	Hour:  time.Hour,
	Day:   24 * time.Hour,
	Week:  7 * 24 * time.Hour,
}

// Outcome reports what Update did with a sample.
type Outcome int

const (
	// Accepted: the sample went into the averages.
	Accepted Outcome = iota
	// Rejected: the sample was outside the tolerance band and left out.
	Rejected
	// Reset: enough rejected samples agreed with each other, the stat
	// restarted from the current sample.
	Reset
)

func (o Outcome) String() string {
	switch o {
	case Accepted:
		return "accepted"
	case Rejected:
		return "rejected"
	case Reset:
		return "reset"
	}
	return "unknown"
}

const (
	failStep   = 10 // added to Failed per rejected sample
	failMax    = 30
	healStreak = 3 // agreeing rejected samples that restart a stat
	tiny       = 1e-20
)

// Stat is the persistent state of one measured value. The zero value is an
// empty stat.
type Stat struct {
	Live     float64             `json:"live"`
	Avg      [numWindows]float64 `json:"avg"`
	Fluct    [numWindows]float64 `json:"fluct"`
	Duration time.Duration       `json:"duration"`
	Updated  time.Time           `json:"updated"`

	// Failed widens the tolerance band after rejected samples and decays
	// with accepted ones.
	Failed       int     `json:"failed"`
	FailedValue  float64 `json:"failed_value"`
	FailedStreak int     `json:"failed_streak"`
}

// Empty reports whether the stat has never accepted a sample.
func (s *Stat) Empty() bool { return s.Duration <= 0 }

// Average returns the moving average over w.
func (s *Stat) Average(w Window) float64 { return s.Avg[w] }

// Fluctuation returns the moving average of the relative deviation of the
// samples from the average over w, between 0 and 1.
func (s *Stat) Fluctuation(w Window) float64 { return s.Fluct[w] }

// Update feeds one sample that covers span. With tolerance > 0 the sample
// must lie inside a band around the week average, otherwise it is rejected
// and Updated keeps its old value; tolerance <= 0 accepts every sample.
func (s *Stat) Update(value float64, span time.Duration, now time.Time, tolerance float64) Outcome {
	if span <= 0 {
		span = time.Second
	}
	if s.Empty() {
		s.start(value, span, now)
		return Accepted
	}

	if tolerance > 0 {
		lo, hi := s.band(tolerance)
		if hi == 0 && value > 0 {
			hi = value
		}
		if value < lo || value > hi {
			return s.reject(value, span, now, tolerance)
		}
	}

	for w := range s.Avg {
		// while the stat is younger than the window this is a plain running
		// mean, afterwards the sample weighs as much as the part of the
		// window it covers
		weight := math.Min(float64(span)/math.Min(float64(s.Duration+span), float64(windowLen[w])), 1)
		dev := math.Min(math.Abs(value-s.Avg[w])/math.Max(math.Abs(s.Avg[w]), tiny), 1)
		s.Fluct[w] += weight * (dev - s.Fluct[w])
		s.Avg[w] += weight * (value - s.Avg[w])
	}
	s.Live = value
	s.Duration += span
	s.Updated = now
	s.Failed = max(s.Failed-1, 0)
	s.FailedValue = 0
	s.FailedStreak = 0
	return Accepted
}

func (s *Stat) start(value float64, span time.Duration, now time.Time) {
	*s = Stat{Live: value, Duration: span, Updated: now}
	for w := range s.Avg {
		s.Avg[w] = value
	}
}

// band returns the accepted range around the week average. It grows with the
// observed fluctuation and with the number of recently rejected samples.
func (s *Stat) band(tolerance float64) (lo, hi float64) {
	week, fluct, failed := s.Avg[Week], s.Fluct[Week], float64(s.Failed)
	down := math.Min(math.Max(2*fluct, tolerance+failed/100), 0.9)
	up := math.Min(math.Max(2*fluct, tolerance+math.Max(3*failed, 10)/100), 2)
	return week * (1 - down), week * (1 + up)
}

func (s *Stat) reject(value float64, span time.Duration, now time.Time, tolerance float64) Outcome {
	s.Failed = min(s.Failed+failStep, failMax)
	if value <= 0 {
		// a zero is a source that did not answer, it says nothing about the level
		return Rejected
	}
	agree := s.FailedValue > 0 &&
		math.Abs(value/s.FailedValue-1) <= math.Min(2*math.Max(tolerance, 0.1), 0.5)
	if agree {
		s.FailedStreak++
	} else {
		s.FailedStreak = 1
	}
	s.FailedValue = value
	if s.FailedStreak >= healStreak {
		// the stored level is wrong for good (bad benchmark, a device
		// dropped out): restart from what is measured now
		s.start(value, span, now)
		return Reset
	}
	return Rejected
}
