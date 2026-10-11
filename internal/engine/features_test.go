package engine

import (
	"math"
	"strings"
	"testing"
	"time"

	"rbm/internal/device"
	"rbm/internal/stats"
	"rbm/internal/sysinfo"
)

// twoPrices mines kawpow (needs 6144 MiB per card) and sha512256d (2048),
// both benchmarked at 100 H/s, kawpow paying twice as much.
func twoPrices(t *testing.T, opts Options) *sim {
	t.Helper()
	s := newSim(t, opts, map[string]float64{"kawpow": 2e-10, "sha512256d": 1e-10})
	s.seed(kawpowID, 100)
	s.seed(shaID, 100)
	s.hashrates = map[string]float64{"kawpow": 100, "sha512256d": 100}
	return s
}

// freeCommit makes the engine see this much reservable memory.
func (s *sim) freeCommit(mib *float64) {
	s.engine.deps.Memory = func() (sysinfo.Memory, error) {
		return sysinfo.Memory{TotalMiB: 32000, FreeMiB: *mib, CommitLimitMiB: 54000, CommitFreeMiB: *mib}, nil
	}
}

func (s *sim) runs() []stats.Run {
	s.t.Helper()
	runs, err := s.store.Runs(time.Time{}, 100)
	if err != nil {
		s.t.Fatal(err)
	}
	return runs
}

func TestPause(t *testing.T) {
	s := twoPrices(t, Options{})
	s.advance(10 * time.Minute)
	if s.running() != "kawpow" {
		t.Fatal("not mining before the pause")
	}

	s.engine.SetPaused(true)
	s.next = s.now
	s.advance(5 * time.Minute)
	status := s.engine.Status()
	if s.running() != "" || !s.miners[0].stopped || !status.Paused || status.Groups[0].Reason != "paused" {
		t.Fatalf("not paused: %+v\n%s", status.Groups[0], strings.Join(s.logs, "\n"))
	}
	if runs := s.runs(); len(runs) != 1 || runs[0].End != stats.EndPaused {
		t.Errorf("runs = %+v", runs)
	}
	s.wantLaunches("kawpow")

	s.engine.SetPaused(false)
	s.next = s.now
	s.advance(time.Minute)
	if s.running() != "kawpow" || s.engine.Status().Paused {
		t.Errorf("not mining again:\n%s", strings.Join(s.logs, "\n"))
	}
}

func TestStartsPausedIfAskedTo(t *testing.T) {
	s := twoPrices(t, Options{Paused: true})
	s.advance(10 * time.Minute)
	s.wantLaunches()
	if !s.engine.Status().Paused {
		t.Error("status does not say paused")
	}
}

func TestActivityLog(t *testing.T) {
	s := twoPrices(t, Options{})
	// 90 s of warm-up, then 8 1/2 measured minutes at 100 H/s and 150 W
	s.advance(10 * time.Minute)
	s.source.prices["sha512256d"] = 9e-10
	s.advance(6 * time.Hour)
	s.miners[len(s.miners)-1].exit()
	s.advance(20 * time.Second)

	runs := s.runs()
	if len(runs) != 2 {
		t.Fatalf("%d runs:\n%s", len(runs), strings.Join(s.logs, "\n"))
	}
	// newest first
	crashed, first := runs[0], runs[1]
	if crashed.Algorithm != "sha512256d" || crashed.End != stats.EndCrashed {
		t.Errorf("second run = %+v", crashed)
	}
	if first.Group != "RTX3060" || first.Miner != "rigel" || first.Algorithm != "kawpow" || first.Pool != "fakepool" ||
		first.End != stats.EndSwitched || first.Benchmark || first.Accepted != 1 {
		t.Errorf("first run = %+v", first)
	}
	if math.Abs(first.Hashrate-100) > 1e-6 {
		t.Errorf("hashrate = %v", first.Hashrate)
	}
	// what it earned: 100 H/s at about 2e-10 BTC per H/s and day, 1% fee,
	// for the measured part of the run
	measured := first.Ended.Sub(first.Started) - warmup
	wantEarned := 100 * 2e-10 * 0.99 * measured.Hours() / 24
	if math.Abs(first.EarnedBTC-wantEarned) > wantEarned*0.02 {
		t.Errorf("earned %g, want about %g", first.EarnedBTC, wantEarned)
	}
	wantEnergy := 0.150 * measured.Hours()
	if math.Abs(first.EnergyKWh-wantEnergy) > wantEnergy*0.02 {
		t.Errorf("energy %g kWh, want about %g", first.EnergyKWh, wantEnergy)
	}
}

func TestStopAllRecordsTheLastMinutes(t *testing.T) {
	s := twoPrices(t, Options{})
	s.advance(4*time.Minute + 30*time.Second)
	s.engine.stopAll()
	runs := s.runs()
	if len(runs) != 1 || runs[0].End != stats.EndStopped || runs[0].EarnedBTC <= 0 {
		t.Errorf("runs = %+v", runs)
	}
}

func TestMemoryGuard(t *testing.T) {
	free := 5000.0
	s := twoPrices(t, Options{MinFreeRAMMiB: 2048})
	s.freeCommit(&free)

	// 5000 free, 2048 to be left alone: kawpow's 6144 do not fit, sha512256d's 2048 do
	s.advance(10 * time.Minute)
	s.wantLaunches("sha512256d")

	// with less, nothing fits, and the status says why
	s2 := twoPrices(t, Options{MinFreeRAMMiB: 2048})
	free2 := 3000.0
	s2.freeCommit(&free2)
	s2.advance(10 * time.Minute)
	s2.wantLaunches()
	if reason := s2.engine.Status().Groups[0].Reason; !strings.Contains(reason, "not enough free memory for 2 candidates") {
		t.Errorf("reason = %q", reason)
	}
	// and mining starts once there is room
	free2 = 20000
	s2.advance(2 * time.Minute)
	s2.wantLaunches("kawpow")
}

func TestMemoryOfTheRunningMinerIsNotCountedAgainstIt(t *testing.T) {
	free := 20000.0
	s := twoPrices(t, Options{MinFreeRAMMiB: 2048})
	s.freeCommit(&free)
	s.advance(10 * time.Minute)
	s.wantLaunches("kawpow")

	// the miner has taken its memory, little is left - it keeps running
	free = 1000
	s.advance(30 * time.Minute)
	s.wantLaunches("kawpow")
	// and it can be replaced by something smaller: its memory comes back
	s.source.prices["sha512256d"] = 9e-10
	s.advance(6 * time.Hour)
	s.wantLaunches("kawpow", "sha512256d")
}

func TestMemoryIsReservedForAMinerThatJustStarted(t *testing.T) {
	// room for exactly one kawpow miner next to the reserve
	free := 6144.0 + 2048 + 100
	s := newSim(t, Options{MinFreeRAMMiB: 2048}, map[string]float64{"kawpow": 2e-10})
	s.engine.groups = groupDevices([]device.Device{
		{ID: "GPU#00", Kind: device.GPU, Vendor: "NVIDIA", Model: "GeForce RTX 3060", Index: 0, MemoryMiB: 12288},
		{ID: "GPU#01", Kind: device.GPU, Vendor: "NVIDIA", Model: "GeForce RTX 2080 SUPER", Index: 1, MemoryMiB: 8192},
	}, false)
	s.seed(kawpowID, 100)
	s.seed("hashrate/rigel/GPU#01/kawpow", 100)
	s.hashrates = map[string]float64{"kawpow": 100}
	s.freeCommit(&free)

	// both groups decide in the same round, before the first miner has
	// taken any memory: the second one must wait all the same
	s.advance(2 * time.Minute)
	s.wantLaunches("kawpow")
	if reason := s.engine.Status().Groups[1].Reason; !strings.Contains(reason, "not enough free memory") {
		t.Errorf("second group: %q", reason)
	}

	// later the measurement shows what the first one took
	free = 2148
	s.advance(10 * time.Minute)
	s.wantLaunches("kawpow")
	// until there is room for both
	free = 20000
	s.advance(2 * time.Minute)
	s.wantLaunches("kawpow", "kawpow")
}

func TestReservedVRAM(t *testing.T) {
	// 12288 MiB card: with 7000 kept free, kawpow's 6144 do not fit any more
	s := twoPrices(t, Options{ReservedVRAMMiB: 7000})
	s.advance(10 * time.Minute)
	s.wantLaunches("sha512256d")

	s = twoPrices(t, Options{ReservedVRAMMiB: 512})
	s.advance(10 * time.Minute)
	s.wantLaunches("kawpow")
}

// The bitcoin price is fetched whether electricity is counted or not: it is
// what turns earnings into real money on the screen.
func TestExchangeRateWithoutAPowerPrice(t *testing.T) {
	s := twoPrices(t, Options{})
	s.advance(10 * time.Minute)
	status := s.engine.Status()
	if status.BTCPrice != 50000 || status.Currency != "EUR" {
		t.Errorf("rate = %v %q", status.BTCPrice, status.Currency)
	}
	// but without a power price it costs nothing: profit is all revenue
	c := status.Groups[0].Candidates[0]
	if c.ProfitBTC != c.RevenueBTC || c.ProfitBTC <= 0 {
		t.Errorf("candidate = %+v", c)
	}
}

func TestWatchdogStatusAndReset(t *testing.T) {
	s := twoPrices(t, Options{})
	s.advance(time.Minute)
	s.miners[0].exit()
	s.advance(20 * time.Second)
	// one crash: counted, not yet taken out
	g := s.engine.Status().Groups[0]
	if len(g.Crashes) != 1 || g.Crashes[0].HashKey != kawpowID || g.Crashes[0].Count != 1 || len(g.Disabled) != 0 {
		t.Fatalf("after one crash: crashes %+v, disabled %+v", g.Crashes, g.Disabled)
	}
	for i := 0; i < 2; i++ {
		s.miners[len(s.miners)-1].exit()
		s.advance(20 * time.Second)
	}
	if g = s.engine.Status().Groups[0]; len(g.Disabled) != 1 || len(g.Crashes) != 0 {
		t.Fatalf("after three crashes: crashes %+v, disabled %+v", g.Crashes, g.Disabled)
	}

	s.engine.ResetWatchdog()
	s.next = s.now
	s.advance(6 * time.Minute)
	if g = s.engine.Status().Groups[0]; len(g.Disabled) != 0 || len(g.Crashes) != 0 || s.running() != "kawpow" {
		t.Errorf("after the reset: %+v, running %q", g, s.running())
	}
	if !s.logged("watchdog was reset, 1 entries cleared") {
		t.Errorf("log:\n%s", strings.Join(s.logs, "\n"))
	}
}
