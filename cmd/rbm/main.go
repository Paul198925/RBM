// Command rbm mines with the most profitable combination of miner program,
// algorithm and pool.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"rbm/internal/config"
	"rbm/internal/device"
	"rbm/internal/pool"
	"rbm/internal/runner"
)

const usage = `usage: rbm [-config file] <command> [options]

commands:
  devices     list the GPUs and the CPU found on this machine
  pools       list what zpool.ca currently pays per algorithm
  currencies  list the coins zpool.ca can pay the earnings out in
  run         mine with all GPUs on what pays best, until Ctrl+C:
              benchmarks first, then switches as the prices move
  mine        run one miner on one algorithm until Ctrl+C, for testing
                -miner name   miner program (default rigel)
                -algo name    algorithm (default kawpow)
                -tls          connect to the pool with TLS
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rbm:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config.yaml", "configuration file")
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
		cfg, err := loadConfig(*configPath, false)
		if err != nil {
			return err
		}
		return listPools(ctx, cfg)
	case "currencies":
		cfg, err := loadConfig(*configPath, false)
		if err != nil {
			return err
		}
		return listCurrencies(ctx, cfg)
	case "run":
		cfg, err := loadConfig(*configPath, true)
		if err != nil {
			return err
		}
		return runner.Auto(ctx, cfg, os.Stdout)
	case "mine":
		flags := flag.NewFlagSet("mine", flag.ExitOnError)
		opts := runner.ManualOptions{}
		flags.StringVar(&opts.Miner, "miner", "rigel", "miner program")
		flags.StringVar(&opts.Algorithm, "algo", "kawpow", "algorithm")
		flags.BoolVar(&opts.TLS, "tls", false, "connect to the pool with TLS")
		flags.Parse(args)
		cfg, err := loadConfig(*configPath, true)
		if err != nil {
			return err
		}
		return runner.Mine(ctx, cfg, opts, os.Stdout)
	}
	flag.Usage()
	return fmt.Errorf("unknown command %q", command)
}

// loadConfig reads the configuration file. Commands that only look around
// work without one.
func loadConfig(path string, required bool) (*config.Config, error) {
	cfg, err := config.Load(path)
	if errors.Is(err, fs.ErrNotExist) {
		if required {
			return nil, fmt.Errorf("%s not found: copy config.example.yaml and fill in your wallet", path)
		}
		return config.Default(), nil
	}
	return cfg, err
}

func listDevices(ctx context.Context) error {
	devices, err := device.Detect(ctx)
	if err != nil {
		return err
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
	return &pool.Zpool{
		Wallet:   cfg.Zpool.Wallet,
		Currency: cfg.Zpool.Currency,
		Worker:   cfg.Worker,
		Region:   cfg.Zpool.Region,
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
		if symbol == cfg.Zpool.Currency {
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
	offers, err := zpoolFor(cfg).Fetch(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "ALGORITHM\tBTC/GH/DAY\t24H\tPAID 24H\tFEE\tPOOL HASHRATE\tWORKERS\t")
	for _, o := range offers {
		fmt.Fprintf(w, "%s\t%.8f\t%.8f\t%.8f\t%.1f%%\t%s\t%d\t\n",
			o.Algorithm, o.Price*1e9, o.Price24h*1e9, o.Actual24h*1e9,
			o.FeePercent, runner.FormatHashrate(o.Hashrate), o.Workers)
	}
	return w.Flush()
}
