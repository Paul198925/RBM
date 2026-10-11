package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hashsteer/internal/device"
	"hashsteer/internal/miner"
	"hashsteer/internal/pool"
	"hashsteer/internal/stats"
)

const (
	kawpowID = "hashrate/rigel/GPU#00/kawpow"
	shaID    = "hashrate/rigel/GPU#00/sha512256d"
)

// fakeSource is a pool with adjustable prices.
type fakeSource struct {
	prices map[string]float64
	// coins holds single-coin offers: algorithm, then coin, then price
	coins map[string]map[string]float64
	err   error
}

func (f *fakeSource) Name() string { return "fakepool" }

func (f *fakeSource) Fetch(ctx context.Context) ([]pool.Offer, error) {
	if f.err != nil {
		return nil, f.err
	}
	var offers []pool.Offer
	for name, price := range f.prices {
		offers = append(offers, pool.Offer{Pool: f.Name(), Algorithm: name, PoolAlgorithm: name,
			Host: name + ".example", Port: 1234, User: "wallet", Pass: "x", Price: price})
	}
	for name, coins := range f.coins {
		for coin, price := range coins {
			offers = append(offers, pool.Offer{Pool: f.Name(), Algorithm: name, PoolAlgorithm: name, Coin: coin,
				Host: name + ".example", Port: 1234, User: "wallet", Pass: "x,zap=" + coin, Price: price})
		}
	}
	return offers, nil
}

// fakeMiner reports a fixed hashrate until it is stopped or made to exit.
type fakeMiner struct {
	algorithm string
	hashrate  float64
	power     float64
	done      chan struct{}
	stopped   bool
}

func (f *fakeMiner) Poll(ctx context.Context) (miner.Sample, error) {
	select {
	case <-f.done:
		return miner.Sample{}, errors.New("connection refused")
	default:
	}
	return miner.Sample{
		Algorithms: map[string]miner.AlgoSample{f.algorithm: {Hashrate: f.hashrate, Accepted: 1}},
		PowerWatts: f.power,
	}, nil
}

func (f *fakeMiner) Done() <-chan struct{} { return f.done }
func (f *fakeMiner) ExitText() string      { return "exit code 1" }
func (f *fakeMiner) Describe() string      { return "fake" }

func (f *fakeMiner) Stop(ctx context.Context) error {
	f.stopped = true
	f.exit()
	return nil
}

func (f *fakeMiner) exit() {
	select {
	case <-f.done:
	default:
		close(f.done)
	}
}

// sim drives an engine through simulated time.
type sim struct {
	t      *testing.T
	engine *Engine
	store  *stats.Store
	source *fakeSource
	now    time.Time
	next   time.Time

	// what the fake miners do, by algorithm
	hashrates map[string]float64
	power     float64
	launchErr error

	launches []string
	// where each launch mines, and with which extra arguments
	targets   []string
	extraArgs [][]string
	miners    []*fakeMiner
	logs      []string
}

func newSim(t *testing.T, opts Options, prices map[string]float64) *sim {
	t.Helper()
	store, err := stats.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	defs, err := miner.Builtin()
	if err != nil {
		t.Fatal(err)
	}

	s := &sim{
		t:         t,
		store:     store,
		source:    &fakeSource{prices: prices},
		now:       time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC),
		hashrates: map[string]float64{"kawpow": 25e6, "sha512256d": 500e6},
		power:     150,
	}
	s.next = s.now

	if opts.Interval == 0 {
		opts.Interval = time.Minute
	}
	if opts.BenchmarkTime == 0 {
		opts.BenchmarkTime = 3 * time.Minute
	}
	if opts.MinRunTime == 0 {
		opts.MinRunTime = 5 * time.Minute
	}
	if opts.HysteresisPercent == 0 {
		opts.HysteresisPercent = 5
	}
	opts.PowerCurrency = "EUR"

	s.engine, err = New(opts, Deps{
		Store:       store,
		Sources:     []pool.Source{s.source},
		Definitions: map[string]*miner.Definition{"rigel": defs["rigel"]},
		Devices: []device.Device{
			{ID: "GPU#00", Kind: device.GPU, Vendor: "NVIDIA", Model: "GeForce RTX 3060", Index: 0, MemoryMiB: 12288},
			{ID: "CPU#00", Kind: device.CPU, Threads: 20},
		},
		Launch: func(ctx context.Context, spec LaunchSpec) (Miner, error) {
			s.launches = append(s.launches, spec.Algorithm.Name)
			s.targets = append(s.targets, spec.Definition.Name+"/"+spec.Algorithm.Name+"@"+spec.Offer.Label())
			s.extraArgs = append(s.extraArgs, spec.ExtraArgs)
			if s.launchErr != nil {
				return nil, s.launchErr
			}
			m := &fakeMiner{
				algorithm: spec.Algorithm.NameForMiner(),
				hashrate:  s.hashrates[spec.Algorithm.Name],
				power:     s.power,
				done:      make(chan struct{}),
			}
			s.miners = append(s.miners, m)
			return m, nil
		},
		Rate: func(ctx context.Context) (float64, error) { return 50000, nil },
		Logf: func(format string, args ...any) { s.logs = append(s.logs, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// advance lets d of simulated time pass, the way Engine.Run would spend it:
// a round, polls every ten seconds, the next round when the interval is over
// or a miner has exited.
func (s *sim) advance(d time.Duration) {
	s.t.Helper()
	ctx := context.Background()
	end := s.now.Add(d)
	for s.now.Before(end) {
		if !s.next.After(s.now) {
			if err := s.engine.round(ctx, s.now); err != nil {
				s.t.Fatal(err)
			}
			s.next = s.now.Add(s.engine.opts.Interval)
		}
		s.now = s.now.Add(pollInterval)
		if s.engine.poll(ctx, s.now) {
			s.next = s.now
		}
	}
}

// seed stores a finished benchmark.
func (s *sim) seed(key string, value float64) {
	s.t.Helper()
	var st stats.Stat
	st.Update(value, time.Hour, s.now, 0)
	if err := s.store.Save(key, st); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sim) running() string {
	if r := s.engine.groups[0].run; r != nil {
		return r.algo.Name
	}
	return ""
}

func (s *sim) logged(text string) bool {
	for _, line := range s.logs {
		if strings.Contains(line, text) {
			return true
		}
	}
	return false
}

func (s *sim) wantLaunches(want ...string) {
	s.t.Helper()
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(s.launches, want) {
		s.t.Fatalf("launches = %v, want %v\nlog:\n%s", s.launches, want, strings.Join(s.logs, "\n"))
	}
}

func TestBenchmarksThenMinesTheBest(t *testing.T) {
	// kawpow earns 2.5e-6, sha512256d 4e-6 BTC per day before fees
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13, "sha512256d": 8e-15})

	s.advance(2 * time.Minute)
	s.wantLaunches("kawpow")
	if !s.engine.groups[0].run.benchmark {
		t.Error("first run is not a benchmark")
	}

	s.advance(28 * time.Minute)
	s.wantLaunches("kawpow", "sha512256d")
	if s.running() != "sha512256d" || s.engine.groups[0].run.benchmark {
		t.Errorf("running %q, benchmark %v", s.running(), s.engine.groups[0].run.benchmark)
	}
	if !s.miners[0].stopped {
		t.Error("the first miner was not stopped")
	}

	for id, want := range map[string]float64{kawpowID: 25e6, shaID: 500e6} {
		st, ok, err := s.store.Load(id)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", id, ok, err)
		}
		if math.Abs(st.Average(stats.Week)-want) > 1 || st.Duration < 3*time.Minute {
			t.Errorf("%s: average %v over %v", id, st.Average(stats.Week), st.Duration)
		}
	}
	power, ok, _ := s.store.Load("power/rigel/GPU#00/kawpow")
	if !ok || power.Average(stats.Week) != 150 {
		t.Errorf("power stat = %+v", power)
	}
}

func TestSwitchesOnlyForAClearAdvantage(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1.00e-10, "sha512256d": 1.03e-10})
	s.seed(kawpowID, 100)
	s.seed(shaID, 100)
	s.hashrates = map[string]float64{"kawpow": 100, "sha512256d": 100}

	s.advance(10 * time.Minute)
	s.wantLaunches("sha512256d")

	// kawpow now pays 4% more: inside the hysteresis of 5%
	s.source.prices["kawpow"] = 1.07e-10
	s.advance(6 * time.Hour)
	s.wantLaunches("sha512256d")

	s.source.prices["kawpow"] = 1.30e-10
	s.advance(6 * time.Hour)
	s.wantLaunches("sha512256d", "kawpow")
	if s.running() != "kawpow" {
		t.Errorf("running %q", s.running())
	}
}

func TestPriceSpikeDoesNotSwitch(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1.00e-10, "sha512256d": 1.03e-10})
	s.seed(kawpowID, 100)
	s.seed(shaID, 100)
	s.hashrates = map[string]float64{"kawpow": 100, "sha512256d": 100}
	s.advance(2 * time.Hour)

	// a spike of ten minutes against two hours of history
	s.source.prices["kawpow"] = 1.5e-10
	s.advance(4 * time.Minute)
	s.source.prices["kawpow"] = 1.00e-10
	s.advance(time.Hour)
	s.wantLaunches("sha512256d")
}

func TestCrashingMinerIsTakenOut(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 2e-10, "sha512256d": 1e-10})
	s.seed(kawpowID, 100)
	s.seed(shaID, 100)
	s.hashrates = map[string]float64{"kawpow": 100, "sha512256d": 100}

	s.advance(time.Minute)
	s.wantLaunches("kawpow")

	// each crash is noticed at the next poll and the miner restarted
	for i := 0; i < 2; i++ {
		s.miners[len(s.miners)-1].exit()
		s.advance(20 * time.Second)
	}
	s.wantLaunches("kawpow", "kawpow", "kawpow")

	// the third crash within the hour takes it out
	s.miners[len(s.miners)-1].exit()
	s.advance(20 * time.Second)
	s.wantLaunches("kawpow", "kawpow", "kawpow", "sha512256d")
	if !s.logged("failed 3 times") {
		t.Error("the removal was not logged")
	}

	// and after the penalty it is tried again
	s.advance(crashPenalty + 10*time.Minute)
	s.wantLaunches("kawpow", "kawpow", "kawpow", "sha512256d", "kawpow")
}

func TestFailedBenchmarkIsNotRepeated(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13, "sha512256d": 8e-15})
	s.hashrates["kawpow"] = 0

	s.advance(warmup + watchdogTimeout + 2*time.Minute)
	s.wantLaunches("kawpow", "sha512256d")
	if !s.miners[0].stopped || !s.logged("benchmark of "+kawpowID+" failed") {
		t.Errorf("log:\n%s", strings.Join(s.logs, "\n"))
	}
	if _, ok, _ := s.store.Load(kawpowID); ok {
		t.Error("a benchmark without hashrate left a stat behind")
	}

	s.advance(2 * time.Hour)
	s.wantLaunches("kawpow", "sha512256d")
	if s.running() != "sha512256d" {
		t.Errorf("running %q", s.running())
	}
}

func TestSilentMinerIsRestarted(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 2e-10})
	s.seed(kawpowID, 100)
	s.hashrates = map[string]float64{"kawpow": 100}
	s.advance(10 * time.Minute)
	s.wantLaunches("kawpow")

	// the miner hangs: it still answers, but hashes nothing
	s.miners[0].hashrate = 0
	s.advance(watchdogTimeout + 2*time.Minute)
	s.wantLaunches("kawpow", "kawpow")
	if !s.miners[0].stopped || !s.logged("reported no hashrate") {
		t.Errorf("log:\n%s", strings.Join(s.logs, "\n"))
	}
	// the stored hashrate was not dragged down by the hang
	st, _, _ := s.store.Load(kawpowID)
	if st.Average(stats.Week) != 100 {
		t.Errorf("average = %v", st.Average(stats.Week))
	}
}

func TestElectricityCanMakeMiningPointless(t *testing.T) {
	prices := map[string]float64{"kawpow": 1e-10}
	// 0.30 EUR per kWh at 50000 EUR per BTC: 200 W cost 2.88e-5 BTC per
	// day, the miner earns 1e-8
	opts := Options{PowerPricePerKWh: 0.30}

	s := newSim(t, opts, prices)
	s.seed(kawpowID, 100)
	s.seed("power/rigel/GPU#00/kawpow", 200)
	s.advance(30 * time.Minute)
	s.wantLaunches()
	if !s.logged("not profitable") {
		t.Errorf("log:\n%s", strings.Join(s.logs, "\n"))
	}

	opts.MineAtLoss = true
	s = newSim(t, opts, prices)
	s.seed(kawpowID, 100)
	s.seed("power/rigel/GPU#00/kawpow", 200)
	s.advance(30 * time.Minute)
	s.wantLaunches("kawpow")
}

func TestPoolOutage(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 2e-10})
	s.seed(kawpowID, 100)
	s.hashrates = map[string]float64{"kawpow": 100}
	s.advance(10 * time.Minute)
	s.wantLaunches("kawpow")

	// a short outage changes nothing
	s.source.err = errors.New("502 bad gateway")
	s.advance(offerMaxAge - 10*time.Minute)
	if s.running() != "kawpow" {
		t.Fatalf("stopped during a short outage, log:\n%s", strings.Join(s.logs, "\n"))
	}
	// a long one stops the mining on prices nobody confirms any more
	s.advance(20 * time.Minute)
	if s.running() != "" {
		t.Error("still mining on stale offers")
	}
	// and it resumes when the pool is back
	s.source.err = nil
	s.advance(5 * time.Minute)
	s.wantLaunches("kawpow", "kawpow")
}

func TestLaunchFailureIsTakenOut(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 2e-10})
	s.seed(kawpowID, 100)
	s.launchErr = errors.New("sha256 mismatch")
	s.advance(10 * time.Minute)
	// three tries, then the penalty
	s.wantLaunches("kawpow", "kawpow", "kawpow")
	if s.running() != "" {
		t.Error("running without a miner")
	}
}

func TestStopAll(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 2e-10})
	s.advance(time.Minute)
	s.engine.stopAll(s.now)
	if !s.miners[0].stopped || s.running() != "" {
		t.Error("miner survived stopAll")
	}
}

func TestGroupDevices(t *testing.T) {
	devices := []device.Device{
		{ID: "CPU#00", Kind: device.CPU},
		{ID: "GPU#02", Kind: device.GPU, Vendor: "NVIDIA", Model: "GeForce RTX 3060", Index: 2},
		{ID: "GPU#01", Kind: device.GPU, Vendor: "NVIDIA", Model: "GeForce RTX 2080 SUPER", Index: 1},
		{ID: "GPU#00", Kind: device.GPU, Vendor: "NVIDIA", Model: "GeForce RTX 3060", Index: 0},
	}
	// the CPU only mines if asked to, and comes last
	withCPU := groupDevices(devices, true)
	if len(withCPU) != 3 || withCPU[2].name != "CPU" || withCPU[2].vendor != miner.VendorCPU {
		t.Fatalf("with cpu: %d groups", len(withCPU))
	}
	groups := groupDevices(devices, false)
	if len(groups) != 2 {
		t.Fatalf("%d groups", len(groups))
	}
	if groups[0].name != "RTX3060" || !reflect.DeepEqual(groups[0].deviceIDs(), []string{"GPU#00", "GPU#02"}) {
		t.Errorf("group 0 = %s %v", groups[0].name, groups[0].deviceIDs())
	}
	if groups[1].name != "RTX2080SUPER" || !reflect.DeepEqual(groups[1].deviceIDs(), []string{"GPU#01"}) {
		t.Errorf("group 1 = %s %v", groups[1].name, groups[1].deviceIDs())
	}
}

func TestNewNeedsADevice(t *testing.T) {
	_, err := New(Options{}, Deps{
		Store:   &stats.Store{},
		Sources: []pool.Source{&fakeSource{}},
		Devices: []device.Device{{ID: "CPU#00", Kind: device.CPU}},
		Launch:  func(context.Context, LaunchSpec) (Miner, error) { return nil, nil },
		Logf:    func(string, ...any) {},
	})
	if err == nil || !strings.Contains(err.Error(), "no device") {
		t.Errorf("err = %v", err)
	}
}
