package engine

import (
	"testing"
	"time"

	"hashsteer/internal/miner"
)

// withCPU returns a sim whose CPU mines ghostrider with xmrig next to the GPU.
func withCPU(t *testing.T) *sim {
	t.Helper()
	s := newSim(t, Options{CPUEnabled: true, CPUWatts: 125, CPUThreads: 12},
		map[string]float64{"kawpow": 1e-13, "ghostrider": 5e-10})
	defs, err := miner.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	s.engine.deps.Definitions = map[string]*miner.Definition{"rigel": defs["rigel"], "xmrig": defs["xmrig"]}
	s.hashrates["ghostrider"] = 2000
	// like a real xmrig, the fake one reports no power draw of its own
	s.power = 0
	return s
}

func TestCPUGroupMinesNextToTheGPU(t *testing.T) {
	s := withCPU(t)
	if len(s.engine.groups) != 2 || s.engine.groups[1].name != "CPU" {
		t.Fatalf("groups = %d", len(s.engine.groups))
	}

	s.advance(10 * time.Minute)
	// one miner per group, both started in the first round
	s.wantLaunches("kawpow", "ghostrider")
	cpu := s.engine.groups[1].run
	if cpu == nil || cpu.def.Name != "xmrig" || cpu.hashKey != "hashrate/xmrig/CPU#00/ghostrider" || cpu.ID != "hashrate/xmrig/CPU#00/ghostrider@fakepool" {
		t.Fatalf("cpu run = %+v", cpu)
	}

	// the assumed power draw stands in for a measurement
	power, ok, err := s.store.Load("power/xmrig/CPU#00/ghostrider")
	if err != nil || !ok || power.Average(0) != 125 {
		t.Errorf("cpu power = %+v, ok %v, err %v", power, ok, err)
	}
	// the GPU has neither a reported nor a measured draw in this test
	if _, ok, _ := s.store.Load("power/rigel/GPU#00/kawpow"); ok {
		t.Error("a power stat without a measurement")
	}
}

func TestCPUStaysOffUnlessEnabled(t *testing.T) {
	s := newSim(t, Options{}, map[string]float64{"kawpow": 1e-13, "ghostrider": 5e-10})
	defs, _ := miner.Builtin()
	s.engine.deps.Definitions = defs
	if len(s.engine.groups) != 1 {
		t.Fatalf("%d groups without cpu mining", len(s.engine.groups))
	}
}

func TestStatusSnapshot(t *testing.T) {
	s := withCPU(t)
	if got := s.engine.Status(); len(got.Groups) != 0 {
		t.Errorf("status before the first round: %+v", got)
	}

	s.advance(2 * time.Minute)
	status := s.engine.Status()
	if len(status.Groups) != 2 || !status.Time.Equal(s.now) {
		t.Fatalf("status = %+v", status)
	}
	gpu, cpu := status.Groups[0], status.Groups[1]
	if gpu.Name != "RTX3060" || gpu.State != "benchmarking" || gpu.Miner != "rigel" || gpu.Algorithm != "kawpow" || gpu.Pool != "fakepool" {
		t.Errorf("gpu = %+v", gpu)
	}
	if gpu.Hashrate != 25e6 || gpu.Accepted != 1 || gpu.Since == nil || gpu.ProfitBTC != 0 {
		t.Errorf("gpu figures = %+v", gpu)
	}
	if cpu.Name != "CPU" || cpu.Miner != "xmrig" || cpu.Hashrate != 2000 || cpu.Power != 125 {
		t.Errorf("cpu = %+v", cpu)
	}
	if len(gpu.Candidates) != 1 || gpu.Candidates[0].Benchmarked || len(cpu.Candidates) != 1 {
		t.Errorf("candidates: gpu %+v, cpu %+v", gpu.Candidates, cpu.Candidates)
	}

	// once measured, the state changes and the profit appears
	s.advance(20 * time.Minute)
	cpu = s.engine.Status().Groups[1]
	if cpu.State != "mining" || cpu.ProfitBTC <= 0 || !cpu.Candidates[0].Benchmarked || cpu.Candidates[0].Hashrate != 2000 {
		t.Errorf("cpu after the benchmark = %+v", cpu)
	}

	// a miner that keeps crashing shows up as taken out. The GPU miner
	// keeps running, so the miner started last is always the CPU's
	for i := 0; i < 3; i++ {
		s.miners[len(s.miners)-1].exit()
		s.advance(20 * time.Second)
	}
	cpu = s.engine.Status().Groups[1]
	if len(cpu.Disabled) != 1 || cpu.Disabled[0].HashKey != "hashrate/xmrig/CPU#00/ghostrider" || cpu.State != "idle" {
		t.Errorf("cpu after failures = %+v", cpu)
	}
	if gpu = s.engine.Status().Groups[0]; gpu.State != "mining" {
		t.Errorf("gpu was affected: %+v", gpu)
	}
}
