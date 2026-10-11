// Package config loads and validates the user configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Worker names this rig at the pools.
	Worker string `yaml:"worker"`
	// DataDir holds miner binaries, logs and the stats database.
	DataDir   string    `yaml:"data_dir"`
	Zpool     Zpool     `yaml:"zpool"`
	Power     Power     `yaml:"power"`
	Switching Switching `yaml:"switching"`
	CPU       CPU       `yaml:"cpu"`
	API       API       `yaml:"api"`
}

// CPU controls mining with the processor. It is off unless enabled: a
// mining CPU makes the machine slow for everything else.
type CPU struct {
	Enabled bool `yaml:"enabled"`
	// Watts is the assumed power draw while mining, for the electricity
	// cost; a CPU's draw cannot be measured the way a GPU's can.
	Watts float64 `yaml:"watts"`
	// Threads limits the miner, 0 lets it use what it sees fit.
	Threads int `yaml:"threads"`
}

// API is the read-only HTTP status interface.
type API struct {
	// Listen is the address to serve on, empty to turn the API off.
	Listen string `yaml:"listen"`
}

// Power describes what electricity costs. With a price of 0 it is not
// counted and no exchange rate is needed.
type Power struct {
	PricePerKWh float64 `yaml:"price_per_kwh"`
	// Currency is the currency of PricePerKWh, e.g. EUR or USD.
	Currency string `yaml:"currency"`
}

// Switching controls how the most profitable miner is chosen.
type Switching struct {
	// Interval is the length of a round: how often hashrates are stored
	// and the choice is reconsidered.
	Interval time.Duration `yaml:"interval"`
	// HysteresisPercent is how much more another miner must earn before
	// the running one is stopped for it.
	HysteresisPercent float64 `yaml:"hysteresis_percent"`
	// MinRunTime is how long a started miner is left alone.
	MinRunTime time.Duration `yaml:"min_run_time"`
	// BenchmarkTime is how much measured mining a miner needs before its
	// hashrate is trusted.
	BenchmarkTime time.Duration `yaml:"benchmark_time"`
	// MineAtLoss keeps mining when electricity costs more than is earned.
	MineAtLoss bool `yaml:"mine_at_loss"`
}

type Zpool struct {
	// Wallet is the payout address. It must be an address of Currency.
	Wallet string `yaml:"wallet"`
	// Currency is the coin zpool pays out in, whatever is mined: BTC, LTC,
	// DOGE, ... "rbm currencies" lists the choices.
	Currency string `yaml:"currency"`
	// Region selects the stratum servers: na, eu, sea or jp.
	Region string `yaml:"region"`
	// TLS connects to the stratum servers with TLS where the pool has a
	// port for it.
	TLS bool `yaml:"tls"`
}

// ZpoolRegions lists the regions zpool.ca runs stratum servers in.
var ZpoolRegions = []string{"na", "eu", "sea", "jp"}

var (
	workerRE   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	currencyRE = regexp.MustCompile(`^[A-Z0-9]{2,10}$`)
	fiatRE     = regexp.MustCompile(`^[A-Z]{3}$`)
)

// Default returns the configuration used for everything the file leaves out.
func Default() *Config {
	return &Config{
		Worker:  "rbm",
		DataDir: "data",
		Zpool:   Zpool{Currency: "BTC", Region: "eu"},
		Power:   Power{Currency: "EUR"},
		API:     API{Listen: "127.0.0.1:4000"},
		Switching: Switching{
			Interval:          time.Minute,
			HysteresisPercent: 5,
			MinRunTime:        5 * time.Minute,
			BenchmarkTime:     3 * time.Minute,
		},
	}
}

// Load reads the YAML file at path on top of the defaults. Unknown keys are
// an error, so a misspelled option cannot go unnoticed.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := Default()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks everything that does not depend on what the program is
// asked to do.
func (c *Config) Validate() error {
	if !workerRE.MatchString(c.Worker) {
		return fmt.Errorf("worker %q: use 1-32 letters, digits, '-' or '_'", c.Worker)
	}
	if c.DataDir == "" {
		return errors.New("data_dir must not be empty")
	}
	if !slices.Contains(ZpoolRegions, c.Zpool.Region) {
		return fmt.Errorf("zpool.region %q: use one of %v", c.Zpool.Region, ZpoolRegions)
	}
	c.Zpool.Currency = strings.ToUpper(strings.TrimSpace(c.Zpool.Currency))
	if !currencyRE.MatchString(c.Zpool.Currency) {
		return fmt.Errorf("zpool.currency %q: use a coin symbol like BTC, LTC or DOGE", c.Zpool.Currency)
	}
	c.Zpool.Wallet = strings.TrimSpace(c.Zpool.Wallet)

	if c.Power.PricePerKWh < 0 {
		return errors.New("power.price_per_kwh must not be negative")
	}
	c.Power.Currency = strings.ToUpper(strings.TrimSpace(c.Power.Currency))
	if !fiatRE.MatchString(c.Power.Currency) {
		return fmt.Errorf("power.currency %q: use a three letter code like EUR or USD", c.Power.Currency)
	}

	sw := c.Switching
	if sw.Interval < 30*time.Second || sw.Interval > time.Hour {
		return fmt.Errorf("switching.interval %v: use 30s to 1h", sw.Interval)
	}
	if sw.HysteresisPercent < 0 || sw.HysteresisPercent > 100 {
		return fmt.Errorf("switching.hysteresis_percent %v: use 0 to 100", sw.HysteresisPercent)
	}
	if sw.MinRunTime < 0 {
		return errors.New("switching.min_run_time must not be negative")
	}
	if sw.BenchmarkTime < sw.Interval {
		return fmt.Errorf("switching.benchmark_time %v must be at least one interval (%v)", sw.BenchmarkTime, sw.Interval)
	}

	if c.CPU.Watts < 0 || c.CPU.Threads < 0 {
		return errors.New("cpu.watts and cpu.threads must not be negative")
	}
	c.API.Listen = strings.TrimSpace(c.API.Listen)
	if c.API.Listen != "" {
		if _, _, err := net.SplitHostPort(c.API.Listen); err != nil {
			return fmt.Errorf("api.listen %q: use host:port, e.g. 127.0.0.1:4000", c.API.Listen)
		}
	}
	return nil
}

// ValidateMining checks what is needed on top to actually mine.
func (c *Config) ValidateMining() error {
	if c.Zpool.Wallet == "" {
		return errors.New("zpool.wallet is not set")
	}
	return nil
}
