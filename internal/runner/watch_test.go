package runner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rbm/internal/device"
	"rbm/internal/pool"
	"rbm/internal/stats"
)

func TestDevicesCarryTelemetry(t *testing.T) {
	app, _ := testApp(t)
	readings := []map[int]device.Telemetry{
		{0: {TemperatureC: 61, PowerWatts: 170, FanPercent: 60}},
		{0: {TemperatureC: 78, PowerWatts: 168, FanPercent: 80}},
		{0: {TemperatureC: 70, PowerWatts: 169, FanPercent: 75}},
	}
	call := 0
	app.telemetryFn = func(context.Context) (map[int]device.Telemetry, error) {
		if call >= len(readings) {
			return nil, errors.New("nvidia-smi is busy")
		}
		call++
		return readings[call-1], nil
	}

	// before the first reading the devices are listed without figures
	if d := app.Devices(); len(d) != 2 || d[0].Telemetry != nil {
		t.Fatalf("devices = %+v", d)
	}
	for range readings {
		app.readTelemetry(context.Background())
	}
	// a failed reading keeps the last figures
	app.readTelemetry(context.Background())

	d := app.Devices()
	gpu, cpu := d[0], d[1]
	if gpu.ID != "GPU#00" || gpu.Telemetry == nil || gpu.Telemetry.TemperatureC != 70 || gpu.MaxTemperatureC != 78 {
		t.Errorf("gpu = %+v, telemetry %+v", gpu, gpu.Telemetry)
	}
	// the CPU shares index 0 with the first card but reports nothing
	if cpu.ID != "CPU#00" || cpu.Telemetry != nil {
		t.Errorf("cpu = %+v", cpu)
	}
	power, _ := app.gpuPower(context.Background())
	if power[0] != 169 {
		t.Errorf("power for the engine = %v", power)
	}
}

func TestBalanceIsReadNowAndThen(t *testing.T) {
	app, _ := testApp(t)
	calls := 0
	var fail error
	app.balanceFn = func(ctx context.Context, z *pool.Zpool) (pool.Balance, error) {
		calls++
		if fail != nil {
			return pool.Balance{}, fail
		}
		return pool.Balance{Pool: "zpool", Currency: z.Currency, Unpaid: float64(calls), Payouts: []pool.Payout{}}, nil
	}
	ctx := context.Background()

	// no wallet, no question to the pool
	app.readBalance(ctx)
	if calls != 0 || len(app.Balances().Balances) != 0 {
		t.Fatalf("asked without a wallet: %d calls, %+v", calls, app.Balances())
	}

	app.cfg.Pools.Zpool.Wallet = "RWallet1"
	app.cfg.Pools.Zpool.Currency = "RVN"
	app.readBalance(ctx)
	app.readBalance(ctx) // too soon to ask again
	if b := app.Balances(); calls != 1 || len(b.Balances) != 1 || b.Balances[0].Currency != "RVN" || b.Error != "" {
		t.Fatalf("%d calls, balances = %+v", calls, b)
	}

	// after the interval it is asked again; a failure keeps the old figure
	app.balanceAt = time.Now().Add(-balanceInterval - time.Second)
	fail = errors.New("zpool: status 502")
	app.readBalance(ctx)
	if b := app.Balances(); calls != 2 || len(b.Balances) != 1 || b.Balances[0].Unpaid != 1 || !strings.Contains(b.Error, "502") {
		t.Errorf("%d calls, balances = %+v", calls, b)
	}
	// and the failed try is not repeated at once
	app.readBalance(ctx)
	if calls != 2 {
		t.Errorf("%d calls after a failure", calls)
	}

	// another wallet is looked up at once, and its balance is its own
	fail = nil
	app.cfg.Pools.Zpool.Wallet = "RWallet2"
	app.readBalance(ctx)
	if b := app.Balances(); calls != 3 || b.Balances[0].Unpaid != 3 || b.Error != "" {
		t.Errorf("%d calls, balances = %+v", calls, b)
	}
}

func TestPauseWithoutAnEngine(t *testing.T) {
	app, out := testApp(t)
	if app.System().Paused {
		t.Fatal("paused from the start")
	}
	app.SetPaused(true)
	app.SetPaused(true)
	if !app.System().Paused || strings.Count(out.String(), "mining is paused") != 1 {
		t.Errorf("console: %q", out.String())
	}
	app.SetPaused(false)
	if app.System().Paused || !strings.Contains(out.String(), "mining continues") {
		t.Errorf("console: %q", out.String())
	}
	system := app.System()
	if system.Started.IsZero() && system.Memory == nil {
		t.Errorf("system = %+v", system)
	}
}

func TestActivityComesFromTheStore(t *testing.T) {
	app, _ := testApp(t)
	// nothing is open before the first cycle
	if runs, err := app.Activity(); err != nil || len(runs) != 0 {
		t.Fatalf("activity = %v, %v", runs, err)
	}

	store, err := stats.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	for i, age := range []time.Duration{time.Hour, 26 * time.Hour, 5 * 24 * time.Hour} {
		if err := store.AddRun(stats.Run{Group: "RTX3060", Miner: "rigel", Algorithm: "kawpow", Pool: "zpool",
			Started: now.Add(-age - 10*time.Minute), Ended: now.Add(-age), End: stats.EndSwitched, Hashrate: float64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if totals, err := app.Totals(); err != nil || len(totals) != 0 {
		t.Errorf("totals without a store: %v, %v", totals, err)
	}
	app.setStore(store)
	totals, err := app.Totals()
	// everything that is kept counts for the totals, also what is older than the activity list shows
	if err != nil || len(totals) != 1 || totals[0].Pool != "zpool" || totals[0].Runs != 3 || totals[0].Minutes != 30 {
		t.Errorf("totals = %+v, %v", totals, err)
	}
	app.ResetWatchdog() // without an engine there is nothing to reset
	runs, err := app.Activity()
	if err != nil {
		t.Fatal(err)
	}
	// the last three days, newest first
	if len(runs) != 2 || runs[0].Hashrate != 1 || runs[1].Hashrate != 2 {
		t.Errorf("runs = %+v", runs)
	}
}
