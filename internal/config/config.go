// Package config loads, validates and saves the user configuration. It lives
// in several files in one directory, one per topic, so that each can be
// edited by hand or through the web interface.
package config

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// The files of the configuration directory.
const (
	FileGeneral    = "config.yaml"
	FilePools      = "pools.yaml"
	FileMiners     = "miners.yaml"
	FileAlgorithms = "algorithms.yaml"
	FileDevices    = "devices.yaml"
)

// Config is the whole configuration, one field per file.
type Config struct {
	General    General    `json:"general"`
	Pools      Pools      `json:"pools"`
	Miners     Miners     `json:"miners"`
	Algorithms Algorithms `json:"algorithms"`
	Devices    Devices    `json:"devices"`
}

// General is config.yaml: everything that is not about a pool, a miner
// program, an algorithm or a device.
type General struct {
	// Worker names this rig at the pools.
	Worker string `yaml:"worker" json:"worker"`
	// DataDir holds miner binaries, logs and the stats database.
	DataDir   string    `yaml:"data_dir" json:"data_dir"`
	Power     Power     `yaml:"power" json:"power"`
	Switching Switching `yaml:"switching" json:"switching"`
	CPU       CPU       `yaml:"cpu" json:"cpu"`
	API       API       `yaml:"api" json:"api"`
}

// Power describes what electricity costs. With a price of 0 it is not
// counted and no exchange rate is needed.
type Power struct {
	PricePerKWh float64 `yaml:"price_per_kwh" json:"price_per_kwh"`
	// Currency is the currency of PricePerKWh, e.g. EUR or USD.
	Currency string `yaml:"currency" json:"currency"`
}

// Switching controls how the most profitable miner is chosen.
type Switching struct {
	// Interval is the length of a round: how often hashrates are stored
	// and the choice is reconsidered.
	Interval Duration `yaml:"interval" json:"interval"`
	// HysteresisPercent is how much more another candidate must earn
	// before the running miner is stopped for it.
	HysteresisPercent float64 `yaml:"hysteresis_percent" json:"hysteresis_percent"`
	// MinRunTime is how long a started miner is left alone.
	MinRunTime Duration `yaml:"min_run_time" json:"min_run_time"`
	// BenchmarkTime is how much measured mining a miner needs before its
	// hashrate is trusted.
	BenchmarkTime Duration `yaml:"benchmark_time" json:"benchmark_time"`
	// MineAtLoss keeps mining when electricity costs more than is earned.
	MineAtLoss bool `yaml:"mine_at_loss" json:"mine_at_loss"`
}

// CPU controls mining with the processor. It is off unless enabled: a
// mining CPU makes the machine slow for everything else.
type CPU struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Watts is the assumed power draw while mining, for the electricity
	// cost; a CPU's draw cannot be measured the way a GPU's can.
	Watts float64 `yaml:"watts" json:"watts"`
	// Threads limits the miner, 0 lets it use what it sees fit.
	Threads int `yaml:"threads" json:"threads"`
}

// API is the web interface and the HTTP API behind it.
type API struct {
	// Listen is the address to serve on, empty to turn it off.
	Listen string `yaml:"listen" json:"listen"`
	// Password protects the interface. It is required for changes from
	// any other machine than this one.
	Password string `yaml:"password" json:"password"`
}

// Pools is pools.yaml: one entry per pool.
type Pools struct {
	Zpool Zpool `yaml:"zpool" json:"zpool"`
}

// Zpool configures zpool.ca. It pays everything out in one coin, whatever is
// mined.
type Zpool struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Wallet is the payout address. It must be an address of Currency.
	Wallet string `yaml:"wallet" json:"wallet"`
	// Currency is the coin zpool pays out in: BTC, LTC, DOGE, ...
	Currency string `yaml:"currency" json:"currency"`
	// Region selects the stratum servers: na, eu, sea or jp.
	Region string `yaml:"region" json:"region"`
	// TLS connects to the stratum servers with TLS where possible.
	TLS bool `yaml:"tls" json:"tls"`

	// AlgoSwitching mines on the pool's algorithm ports, where the pool
	// decides which coin is mined and may change it every few minutes.
	AlgoSwitching bool `yaml:"algo_switching" json:"algo_switching"`
	// CoinMining mines single coins, chosen here by what they pay.
	CoinMining bool `yaml:"coin_mining" json:"coin_mining"`
	// Coins limits coin mining to these symbols; empty allows all.
	Coins []string `yaml:"coins" json:"coins"`
	// ExcludeCoins are never mined as single coins.
	ExcludeCoins []string `yaml:"exclude_coins" json:"exclude_coins"`
}

// Miners is miners.yaml: settings per miner program, by its name. A miner
// without an entry is used as it is.
type Miners map[string]Miner

// Miner holds the settings of one miner program.
type Miner struct {
	Disabled bool `yaml:"disabled" json:"disabled"`
	// ExcludeAlgorithms are not mined with this program.
	ExcludeAlgorithms []string `yaml:"exclude_algorithms" json:"exclude_algorithms"`
	// ExtraArgs are added to the program's command line.
	ExtraArgs []string `yaml:"extra_args" json:"extra_args"`
}

// Algorithms is algorithms.yaml: which algorithms may be mined at all.
type Algorithms struct {
	// Only limits mining to these algorithms; empty allows all.
	Only []string `yaml:"only" json:"only"`
	// Exclude are never mined.
	Exclude []string `yaml:"exclude" json:"exclude"`
}

// Devices is devices.yaml: which devices stay out of mining.
type Devices struct {
	// Exclude lists device IDs like "GPU#01".
	Exclude []string `yaml:"exclude" json:"exclude"`
}

// ZpoolRegions lists the regions zpool.ca runs stratum servers in.
var ZpoolRegions = []string{"na", "eu", "sea", "jp"}

var (
	workerRE   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	currencyRE = regexp.MustCompile(`^[A-Z0-9]{2,10}$`)
	coinRE     = regexp.MustCompile(`^[A-Z0-9-]{1,16}$`)
	fiatRE     = regexp.MustCompile(`^[A-Z]{3}$`)
	nameRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	deviceRE   = regexp.MustCompile(`^(GPU|CPU)#\d{2,}$`)
	walletRE   = regexp.MustCompile(`^[A-Za-z0-9:._-]{1,128}$`)
)

// Default returns the configuration used for everything the files leave out.
func Default() *Config {
	return &Config{
		General: General{
			Worker:  "rbm",
			DataDir: "data",
			Power:   Power{Currency: "EUR"},
			Switching: Switching{
				Interval:          Duration(time.Minute),
				HysteresisPercent: 5,
				MinRunTime:        Duration(5 * time.Minute),
				BenchmarkTime:     Duration(3 * time.Minute),
			},
			API: API{Listen: "127.0.0.1:4000"},
		},
		Pools: Pools{Zpool: Zpool{
			Enabled:       true,
			Currency:      "BTC",
			Region:        "eu",
			AlgoSwitching: true,
			CoinMining:    true,
		}},
		Miners: Miners{},
	}
}

// Validate checks the configuration and brings it into its normal form:
// symbols in upper case, lists sorted and without duplicates.
func (c *Config) Validate() error {
	g := &c.General
	if !workerRE.MatchString(g.Worker) {
		return fmt.Errorf("worker %q: use 1-32 letters, digits, '-' or '_'", g.Worker)
	}
	if g.DataDir == "" {
		return errors.New("data_dir must not be empty")
	}

	if g.Power.PricePerKWh < 0 {
		return errors.New("power.price_per_kwh must not be negative")
	}
	g.Power.Currency = strings.ToUpper(strings.TrimSpace(g.Power.Currency))
	if !fiatRE.MatchString(g.Power.Currency) {
		return fmt.Errorf("power.currency %q: use a three letter code like EUR or USD", g.Power.Currency)
	}

	sw := g.Switching
	if sw.Interval < Duration(30*time.Second) || sw.Interval > Duration(time.Hour) {
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

	if g.CPU.Watts < 0 || g.CPU.Threads < 0 {
		return errors.New("cpu.watts and cpu.threads must not be negative")
	}
	g.API.Listen = strings.TrimSpace(g.API.Listen)
	if g.API.Listen != "" {
		if _, _, err := net.SplitHostPort(g.API.Listen); err != nil {
			return fmt.Errorf("api.listen %q: use host:port, e.g. 127.0.0.1:4000", g.API.Listen)
		}
	}

	z := &c.Pools.Zpool
	if !slices.Contains(ZpoolRegions, z.Region) {
		return fmt.Errorf("zpool region %q: use one of %v", z.Region, ZpoolRegions)
	}
	z.Currency = strings.ToUpper(strings.TrimSpace(z.Currency))
	if !currencyRE.MatchString(z.Currency) {
		return fmt.Errorf("zpool currency %q: use a coin symbol like BTC, LTC or DOGE", z.Currency)
	}
	z.Wallet = strings.TrimSpace(z.Wallet)
	if z.Wallet != "" && !walletRE.MatchString(z.Wallet) {
		return errors.New("zpool wallet: this does not look like an address")
	}
	var err error
	if z.Coins, err = symbols(z.Coins, coinRE, "zpool coin"); err != nil {
		return err
	}
	if z.ExcludeCoins, err = symbols(z.ExcludeCoins, coinRE, "zpool excluded coin"); err != nil {
		return err
	}

	if c.Miners == nil {
		c.Miners = Miners{}
	}
	for name, m := range c.Miners {
		if !nameRE.MatchString(name) {
			return fmt.Errorf("miner %q: not a miner name", name)
		}
		m.ExcludeAlgorithms = algorithms(m.ExcludeAlgorithms)
		if m.ExtraArgs == nil {
			m.ExtraArgs = []string{}
		}
		for _, arg := range m.ExtraArgs {
			if strings.TrimSpace(arg) == "" {
				return fmt.Errorf("miner %s: empty extra argument", name)
			}
		}
		c.Miners[name] = m
	}

	c.Algorithms.Only = algorithms(c.Algorithms.Only)
	c.Algorithms.Exclude = algorithms(c.Algorithms.Exclude)

	if c.Devices.Exclude, err = symbols(c.Devices.Exclude, deviceRE, "device"); err != nil {
		return err
	}
	return nil
}

// ReadyToMine reports what is still missing before mining can start.
func (c *Config) ReadyToMine() error {
	z := c.Pools.Zpool
	if !z.Enabled {
		return errors.New("no pool is enabled")
	}
	if z.Wallet == "" {
		return errors.New("the zpool wallet address is not set")
	}
	if !z.AlgoSwitching && !z.CoinMining {
		return errors.New("zpool has both algorithm switching and coin mining turned off")
	}
	return nil
}

// symbols normalizes a list of upper case identifiers.
func symbols(list []string, re *regexp.Regexp, what string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, s := range list {
		s = strings.ToUpper(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if !re.MatchString(s) {
			return nil, fmt.Errorf("%s %q is not valid", what, s)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return slices.Compact(out), nil
}

// algorithms normalizes a list of algorithm names the way pools and miner
// definitions spell them: lower case, without separators.
func algorithms(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		s = strings.Map(func(r rune) rune {
			switch r {
			case '-', '_', ' ', '.', '/':
				return -1
			}
			return r
		}, strings.ToLower(strings.TrimSpace(s)))
		if s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// Duration is a time span that reads and writes as "90s", "5m" or "1h30m".
type Duration time.Duration

func (d Duration) String() string {
	t := time.Duration(d)
	if t == 0 {
		return "0s"
	}
	// time.Duration prints 5m as "5m0s"
	s := t.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Duration) UnmarshalText(text []byte) error {
	t, err := time.ParseDuration(strings.TrimSpace(string(text)))
	if err != nil {
		return fmt.Errorf("%q is not a duration like 90s, 5m or 1h", text)
	}
	*d = Duration(t)
	return nil
}
