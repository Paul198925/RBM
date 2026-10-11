// Command hashsteer mines with the most profitable combination of miner program,
// algorithm, coin and pool.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"

	"hashsteer/internal/config"
	"hashsteer/internal/device"
	"hashsteer/internal/pool"
	"hashsteer/internal/rates"
	"hashsteer/internal/runner"
)

const usage = `usage: hashsteer [-config dir] <command> [options]

commands:
  run         mine with all devices on what pays best, until Ctrl+C:
              benchmarks first, then switches as the prices move.
              Everything can be watched and configured in the web
              interface, by default at http://127.0.0.1:4000/
  devices     list the GPUs and the CPU found on this machine
  pools       list what the enabled pools currently pay, per algorithm and coin
  currencies  list the coins zpool.ca can pay the earnings out in
  estimates   print what this machine has measured, per device, in the form
              hashrates are shared in
  mine        run one miner on one algorithm until Ctrl+C, for testing
                -miner name   miner program (default rigel)
                -algo name    algorithm (default kawpow)
                -tls          connect to the pool with TLS

The configuration is a directory of YAML files (default: config). It is
created with defaults on the first run.
`

// legacyConfig is the single file the first versions read. It is looked for
// next to the configuration directory.
const legacyConfig = "config.yaml"

func main() {
	if err := run(); err != nil {
		// the start script starts the program again, with the new build
		if errors.Is(err, runner.ErrRestart) {
			os.Exit(runner.RestartCode)
		}
		fmt.Fprintln(os.Stderr, "hashsteer:", err)
		os.Exit(1)
	}
}

func run() error {
	configDir := flag.String("config", "config", "configuration directory")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	command, args := flag.Arg(0), flag.Args()[1:]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch command {
	case "devices":
		return listDevices(ctx)
	case "pools":
		cfg, err := loadConfig(*configDir)
		if err != nil {
			return err
		}
		return listPools(ctx, cfg)
	case "estimates":
		cfg, err := loadConfig(*configDir)
		if err != nil {
			return err
		}
		return runner.ExportEstimates(ctx, cfg, os.Stdout)
	case "currencies":
		cfg, err := loadConfig(*configDir)
		if err != nil {
			return err
		}
		return listCurrencies(ctx, cfg)
	case "run":
		if err := prepareConfig(*configDir); err != nil {
			return err
		}
		app, err := runner.NewApp(ctx, *configDir, os.Stdout)
		if err != nil {
			return err
		}
		return app.Run(ctx)
	case "mine":
		flags := flag.NewFlagSet("mine", flag.ExitOnError)
		opts := runner.ManualOptions{}
		flags.StringVar(&opts.Miner, "miner", "rigel", "miner program")
		flags.StringVar(&opts.Algorithm, "algo", "kawpow", "algorithm")
		flags.BoolVar(&opts.TLS, "tls", false, "connect to the pool with TLS")
		flags.Parse(args)
		cfg, err := loadConfig(*configDir)
		if err != nil {
			return err
		}
		return runner.Mine(ctx, cfg, opts, os.Stdout)
	}
	flag.Usage()
	return fmt.Errorf("unknown command %q", command)
}

// prepareConfig makes sure the configuration directory exists: converted
// from the single file of the first versions, or written with the defaults.
func prepareConfig(dir string) error {
	legacy := filepath.Join(filepath.Dir(filepath.Clean(dir)), legacyConfig)
	migrated, err := config.MigrateLegacy(legacy, dir)
	if err != nil {
		return err
	}
	if migrated {
		fmt.Printf("configuration moved from %s into the directory %s (old file kept as %s.old)\n", legacy, dir, legacy)
		return nil
	}
	if !config.Exists(dir) {
		if err := config.SaveDir(dir, config.Default()); err != nil {
			return err
		}
		fmt.Printf("wrote a default configuration to the directory %s\n", dir)
	}
	return nil
}

// loadConfig makes sure there is a configuration and reads it.
func loadConfig(dir string) (*config.Config, error) {
	if err := prepareConfig(dir); err != nil {
		return nil, err
	}
	return config.LoadDir(dir)
}

func listDevices(ctx context.Context) error {
	devices, problem := device.DetectAll(ctx)
	if problem != "" {
		fmt.Fprintln(os.Stderr, problem)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tMODEL\tMEMORY\tTHREADS")
	for _, d := range devices {
		memory, threads := "-", "-"
		if d.Kind == device.GPU {
			memory = fmt.Sprintf("%d MiB", d.MemoryMiB)
		} else {
			threads = fmt.Sprint(d.Threads)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", d.ID, d.Kind, d.Model, memory, threads)
	}
	return w.Flush()
}

func zpoolFor(cfg *config.Config) *pool.Zpool {
	z := cfg.Pools.Zpool
	return &pool.Zpool{
		Wallet:        cfg.Pools.ZpoolWallet(),
		Currency:      z.Currency,
		Worker:        cfg.General.Worker,
		Region:        z.Region,
		AlgoSwitching: z.AlgoSwitching,
		CoinMining:    z.CoinMining,
		Coins:         z.Coins,
		ExcludeCoins:  z.ExcludeCoins,
	}
}

func listCurrencies(ctx context.Context, cfg *config.Config) error {
	currencies, err := zpoolFor(cfg).PayoutCurrencies(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SYMBOL\tNAME\tMIN PAYOUT\tNOTICE")
	for _, c := range currencies {
		symbol := c.Symbol
		if symbol == cfg.Pools.Zpool.Currency {
			symbol += " *"
		}
		// the pool does not publish a minimum for every coin
		minimum := "-"
		if c.MinimumPayout > 0 {
			minimum = fmt.Sprint(c.MinimumPayout)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", symbol, c.Name, minimum, c.Notice)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	_, err = fmt.Println("\n* configured payout currency")
	return err
}

func listPools(ctx context.Context, cfg *config.Config) error {
	var sources []pool.Source
	if cfg.Pools.Zpool.Enabled {
		sources = append(sources, zpoolFor(cfg))
	}
	prices := &rates.CoinGecko{}
	for _, name := range config.CoinPoolNames {
		if cfg.Pools.CoinPoolReady(name) != nil {
			continue
		}
		c := cfg.Pools.CoinPool(name)
		sources = append(sources, pool.NewCoinPool(name, pool.CoinPoolSettings{Wallets: cfg.Pools.Wallets, Worker: cfg.General.Worker,
			Region: c.Region, ExcludeCoins: c.ExcludeCoins, Prices: prices.InBTC}))
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "POOL\tALGORITHM\tCOIN\tBTC/GH/DAY\tPOOL HASHRATE\tWORKERS\tFEE\t")
	for _, source := range sources {
		offers, err := source.Fetch(ctx)
		if err != nil {
			return err
		}
		for _, o := range offers {
			coin := o.Coin
			if coin == "" {
				coin = "(pool picks)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%.8f\t%s\t%d\t%.1f%%\t\n",
				o.Pool, o.Algorithm, coin, o.Price*1e9, runner.FormatHashrate(o.Hashrate), o.Workers, o.FeePercent)
		}
	}
	return w.Flush()
}
