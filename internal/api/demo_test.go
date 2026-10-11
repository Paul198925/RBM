package api

import (
	"os"
	"os/signal"
	"testing"
	"time"

	"rbm/internal/config"
	"rbm/internal/engine"
	"rbm/internal/pool"
)

// demoBackend shows the interface with a bit of everything: a mining group,
// a benchmarking one, an idle one, measured and unmeasured candidates and a
// miner that was taken out.
type demoBackend struct {
	*fakeBackend
}

func (d demoBackend) Status() engine.Status {
	since := time.Now().Add(-47 * time.Minute)
	started := time.Now().Add(-2 * time.Minute)
	key := "hashrate/rigel/GPU#00/kawpow"
	return engine.Status{Time: time.Now(), BTCPrice: 74177, Currency: "EUR", BenchmarkSeconds: 180, Groups: []engine.GroupStatus{
		{
			Name: "RTX3060", Devices: []string{"GPU#00"}, State: "mining", Reason: "still the most profitable",
			Miner: "rigel", Algorithm: "kawpow", Pool: "zpool", Coin: "CLORE", Running: key + "@zpool:CLORE", Since: &since,
			Hashrate: 19.93e6, Power: 170, Accepted: 212, Rejected: 1, ProfitBTC: 1.94e-6,
			Candidates: []engine.CandidateStatus{
				{ID: key + "@zpool:CLORE", HashKey: key, Miner: "rigel", Algorithm: "kawpow", Pool: "zpool", Coin: "CLORE", Benchmarked: true, Hashrate: 19.9e6, Price: 1.1e-13, FeePercent: 1, Power: 170, RevenueBTC: 2.17e-6, ProfitBTC: 1.94e-6},
				{ID: key + "@zpool", HashKey: key, Miner: "rigel", Algorithm: "kawpow", Pool: "zpool", Benchmarked: true, Hashrate: 19.9e6, Price: 1.19e-13, FeePercent: 1, Power: 170, RevenueBTC: 2.34e-6, ProfitBTC: 2.0e-6},
				{ID: "hashrate/wildrig/GPU#00/kawpow@zpool:CLORE", HashKey: "hashrate/wildrig/GPU#00/kawpow", Miner: "wildrig", Algorithm: "kawpow", Pool: "zpool", Coin: "CLORE", Benchmarked: true, Hashrate: 18.2e6, Price: 1.1e-13, FeePercent: 0.75, Power: 175, RevenueBTC: 1.99e-6, ProfitBTC: -0.3e-6},
				{ID: "hashrate/wildrig/GPU#00/skydoge@zpool", HashKey: "hashrate/wildrig/GPU#00/skydoge", Miner: "wildrig", Algorithm: "skydoge", Pool: "zpool", Price: 4.2e-9, FeePercent: 2},
			},
			Disabled: []engine.DisabledStatus{{HashKey: "hashrate/wildrig/GPU#00/firopow", Until: time.Now().Add(5*time.Hour + 12*time.Minute)}},
			Crashes:  []engine.CrashStatus{{HashKey: "hashrate/srbminer/GPU#00/sha512256d", Count: 2, Last: time.Now().Add(-7 * time.Minute)}},
		},
		{
			Name: "RTX2080SUPER", Devices: []string{"GPU#01"}, State: "benchmarking", Reason: "needs a benchmark",
			Miner: "wildrig", Algorithm: "meowpow", Pool: "zpool", Coin: "MEWC", Running: "hashrate/wildrig/GPU#01/meowpow@zpool:MEWC", Since: &started,
			Hashrate: 21.4e6, Power: 188, Accepted: 3,
			Candidates: []engine.CandidateStatus{
				{ID: "hashrate/wildrig/GPU#01/meowpow@zpool:MEWC", HashKey: "hashrate/wildrig/GPU#01/meowpow", Miner: "wildrig", Algorithm: "meowpow", Pool: "zpool", Coin: "MEWC", Price: 9e-14, FeePercent: 0.75},
			},
			Disabled: []engine.DisabledStatus{},
			Crashes:  []engine.CrashStatus{},
		},
		{
			Name: "CPU", Devices: []string{"CPU#00"}, State: "idle", Reason: "not profitable: electricity costs more than mining earns",
			Candidates: []engine.CandidateStatus{
				{ID: "hashrate/xmrig/CPU#00/ghostrider@zpool", HashKey: "hashrate/xmrig/CPU#00/ghostrider", Miner: "xmrig", Algorithm: "ghostrider", Pool: "zpool", Benchmarked: true, Hashrate: 2150, Price: 5.5e-10, FeePercent: 1, Power: 125, RevenueBTC: 1.17e-6, ProfitBTC: -0.4e-6},
			},
			Disabled: []engine.DisabledStatus{},
			Crashes:  []engine.CrashStatus{},
		},
	}}
}

func (d demoBackend) System() System {
	s := d.fakeBackend.System()
	s.Started = time.Now().Add(-27*time.Hour - 44*time.Minute)
	return s
}

func (d demoBackend) Offers() []pool.Offer {
	updated := time.Now().Add(-40 * time.Second)
	host := func(algo string) string { return algo + ".eu.mine.zpool.ca" }
	return []pool.Offer{
		{Pool: "zpool", Algorithm: "kawpow", Host: host("kawpow"), Port: 1325, Price: 1.19e-13, Price24h: 1.12e-13, Actual24h: 1.05e-13, Hashrate: 5.17e9, Workers: 88, FeePercent: 1, Updated: updated},
		{Pool: "zpool", Algorithm: "kawpow", Coin: "CLORE", CoinName: "Clore", Host: host("kawpow"), Port: 1325, Price: 1.1e-13, Hashrate: 3.1e9, Workers: 60, FeePercent: 1, LastBlockSeconds: 396, Blocks24h: 211, Updated: updated},
		{Pool: "zpool", Algorithm: "kawpow", Coin: "MEWC", CoinName: "Meowcoin <b>bold?</b>", Host: host("kawpow"), Port: 1325, Price: 9e-14, Hashrate: 2e8, Workers: 11, FeePercent: 1, LastBlockSeconds: 4210, Blocks24h: 17, Updated: updated},
		{Pool: "zpool", Algorithm: "ghostrider", Host: host("ghostrider"), Port: 5354, Price: 6.3e-10, Price24h: 6.0e-10, Actual24h: 6.4e-10, Hashrate: 97190, Workers: 7223, FeePercent: 1, Updated: updated},
		{Pool: "2miners", Algorithm: "kawpow", Coin: "RVN", CoinName: "Ravencoin", Host: "rvn.2miners.com", Port: 6060, Price: 1.05e-13, Hashrate: 3.08e11, Workers: 3626, FeePercent: 1, LastBlockSeconds: 76, Blocks24h: 705, Updated: updated},
		{Pool: "woolypooly", Algorithm: "kawpow", Coin: "RVN", CoinName: "Ravencoin", Host: "pool.woolypooly.com", Port: 55555, Price: 1.02e-13, Hashrate: 5.85e9, Workers: 101, FeePercent: 0.9, Blocks24h: 13, Updated: updated},
		{Pool: "zpool", Algorithm: "sha256", Host: host("sha256"), Port: 3333, Price: 2e-19, Hashrate: 4e17, Workers: 300, FeePercent: 1, Updated: updated},
	}
}

// TestDemoServer serves the interface with made-up data, to look at it in a
// browser without mining:
//
//	RBM_UI_DEMO=127.0.0.1:4011 go test ./internal/api -run TestDemoServer -timeout 0
func TestDemoServer(t *testing.T) {
	addr := os.Getenv("RBM_UI_DEMO")
	if addr == "" {
		t.Skip("set RBM_UI_DEMO to an address to serve the interface with made-up data")
	}
	backend := newBackend()
	backend.cfg.Pools.Wallets = config.Wallets{"RVN": "RDemoAddress1", "ERG": "9demoAddress"}
	backend.cfg.Pools.TwoMiners.Enabled = true
	srv, err := Start(addr, Handler(demoBackend{backend}))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	t.Logf("serving the demo at http://%s/ until interrupted", srv.Addr())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
}
