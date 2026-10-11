package runner

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"rbm/internal/config"
	"rbm/internal/device"
	"rbm/internal/engine"
	"rbm/internal/miner"
	"rbm/internal/pool"
	"rbm/internal/rates"
	"rbm/internal/stats"
)

// Auto mines with all GPUs on whatever pays best, until ctx is cancelled:
// it benchmarks what has not been measured yet and then switches between
// algorithms as the prices move.
func Auto(ctx context.Context, cfg *config.Config, out io.Writer) error {
	if err := cfg.ValidateMining(); err != nil {
		return err
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(out, "%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	}

	devices, err := device.Detect(ctx)
	if err != nil {
		return err
	}
	definitions, err := miner.Builtin()
	if err != nil {
		return err
	}
	zpool := &pool.Zpool{
		Wallet:   cfg.Zpool.Wallet,
		Currency: cfg.Zpool.Currency,
		Worker:   cfg.Worker,
		Region:   cfg.Zpool.Region,
	}
	if err := checkPayoutCurrency(ctx, zpool, logf); err != nil {
		return err
	}
	store, err := stats.Open(filepath.Join(cfg.DataDir, "stats.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	deps := engine.Deps{
		Store:       store,
		Sources:     []pool.Source{zpool},
		Definitions: definitions,
		Devices:     devices,
		Launch:      engine.Launch,
		Power:       device.GPUPower,
		Logf:        logf,
	}
	if cfg.Power.PricePerKWh > 0 {
		coinbase := &rates.Coinbase{}
		deps.Rate = func(ctx context.Context) (float64, error) {
			return coinbase.BTCPrice(ctx, cfg.Power.Currency)
		}
		logf("electricity: %g %s per kWh", cfg.Power.PricePerKWh, cfg.Power.Currency)
	} else {
		logf("electricity is not counted (power.price_per_kwh is 0)")
	}

	e, err := engine.New(engine.Options{
		Interval:          cfg.Switching.Interval,
		MinRunTime:        cfg.Switching.MinRunTime,
		BenchmarkTime:     cfg.Switching.BenchmarkTime,
		HysteresisPercent: cfg.Switching.HysteresisPercent,
		MineAtLoss:        cfg.Switching.MineAtLoss,
		PowerPricePerKWh:  cfg.Power.PricePerKWh,
		PowerCurrency:     cfg.Power.Currency,
		TLS:               cfg.Zpool.TLS,
		DataDir:           cfg.DataDir,
	}, deps)
	if err != nil {
		return err
	}
	return e.Run(ctx)
}
