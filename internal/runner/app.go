package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"hashsteer/internal/api"
	"hashsteer/internal/config"
	"hashsteer/internal/device"
	"hashsteer/internal/engine"
	"hashsteer/internal/estimate"
	"hashsteer/internal/miner"
	"hashsteer/internal/pool"
	"hashsteer/internal/rates"
	"hashsteer/internal/stats"
	"hashsteer/internal/sysinfo"
	"hashsteer/internal/update"
)

// Version is this build's number. It is shown in the interface, and what a
// newer release is measured against. It is raised with every release.
const Version = "0.4.1"

const (
	logLines          = 1000
	minerLogTail      = 64 << 10
	minerLogsListed   = 60
	currencyMaxAge    = 10 * time.Minute
	telemetryInterval = 10 * time.Second
	balanceInterval   = 10 * time.Minute
	activityDays      = 3
	activityKept      = 30 * 24 * time.Hour
	activityListed    = 500
)

// App is the running application: it holds the configuration, runs the
// engine with it, starts the engine anew when the configuration changes, and
// answers the web interface.
type App struct {
	configDir   string
	out         io.Writer
	devices     []device.Device
	definitions map[string]*miner.Definition
	// what is missing on this machine, as found at the start
	notices []string
	// checker asks for newer releases, nil where that is not wanted;
	// restart is signalled when one is to take over
	checker *update.Checker
	restart chan struct{}
	reload  chan struct{}
	started time.Time
	// replaceable for tests
	telemetryFn func(context.Context) (map[int]device.Telemetry, error)
	balanceFn   func(context.Context, *pool.Zpool) (pool.Balance, error)
	// nil leaves the coin pools unasked
	coinBalanceFn func(context.Context, pool.CoinPool) ([]pool.Balance, error)
	// what coins cost, for the pools that pay in the mined coin
	coinPrices rates.CoinGecko

	mu           sync.Mutex
	cfg          *config.Config
	engine       *engine.Engine
	store        *stats.Store
	waiting      string
	paused       bool
	log          []string
	currencies   []pool.PayoutCurrency
	currenciesAt time.Time
	telemetry    map[int]device.Telemetry
	maxTemp      map[int]float64
	balances     api.BalanceInfo
	balanceKey   string
	balanceAt    time.Time
	update       updateState
}

// NewApp loads the configuration from dir and looks at the hardware.
func NewApp(ctx context.Context, configDir string, out io.Writer) (*App, error) {
	cfg, err := config.LoadDir(configDir)
	if err != nil {
		return nil, err
	}
	devices, problem := device.DetectAll(ctx)
	definitions, err := miner.Builtin()
	if err != nil {
		return nil, err
	}
	var notices []string
	if problem != "" {
		notices = append(notices, problem)
		fmt.Fprintln(out, problem)
	}
	return &App{
		notices:     notices,
		checker:     &update.Checker{},
		restart:     make(chan struct{}, 1),
		configDir:   configDir,
		out:         out,
		devices:     devices,
		definitions: definitions,
		reload:      make(chan struct{}, 1),
		started:     time.Now(),
		telemetryFn: device.GPUTelemetry,
		balanceFn:   func(ctx context.Context, z *pool.Zpool) (pool.Balance, error) { return z.Balance(ctx) },
		coinBalanceFn: func(ctx context.Context, p pool.CoinPool) ([]pool.Balance, error) {
			return p.Balances(ctx)
		},
		cfg:     cfg,
		waiting: "starting",
	}, nil
}

// logf writes a line to the console and keeps it for the interface.
func (a *App) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05") + " " + fmt.Sprintf(format, args...)
	fmt.Fprintln(a.out, line)
	a.mu.Lock()
	a.log = append(a.log, line)
	if len(a.log) > logLines {
		a.log = append(a.log[:0], a.log[len(a.log)-logLines:]...)
	}
	a.mu.Unlock()
}

// Run mines until ctx is cancelled. Whenever the configuration changes, the
// engine is stopped and started again with the new one.
func (a *App) Run(ctx context.Context) error {
	if listen := a.Config().General.API.Listen; listen != "" {
		server, err := api.Start(listen, api.Handler(a))
		if err != nil {
			return fmt.Errorf("web interface: %w", err)
		}
		defer server.Close()
		a.logf("web interface: http://%s/", server.Addr())
	}

	watchCtx, stopWatching := context.WithCancel(ctx)
	defer stopWatching()
	go a.watch(watchCtx)
	go a.watchUpdates(watchCtx)
	go a.watchSharing(watchCtx)

	// a restart for an update ends everything the way Ctrl+C does
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	restarting := make(chan struct{})
	go func() {
		select {
		case <-a.restart:
			close(restarting)
			stop()
		case <-ctx.Done():
		}
	}()

	for {
		err := a.cycle(ctx)
		select {
		case <-restarting:
			return ErrRestart
		default:
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		a.logf("configuration changed, applying it")
	}
}

// watch keeps the figures up to date that do not come from the engine: what
// the graphics cards report and what the pool holds for the wallet.
func (a *App) watch(ctx context.Context) {
	ticker := time.NewTicker(telemetryInterval)
	defer ticker.Stop()
	for {
		a.readTelemetry(ctx)
		a.readBalance(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) readTelemetry(ctx context.Context) {
	if a.telemetryFn == nil {
		return
	}
	telemetry, err := a.telemetryFn(ctx)
	if err != nil {
		// no NVIDIA driver, or the tool is busy: the last figures stay
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.telemetry = telemetry
	if a.maxTemp == nil {
		a.maxTemp = map[int]float64{}
	}
	for index, t := range telemetry {
		if t.TemperatureC > a.maxTemp[index] {
			a.maxTemp[index] = t.TemperatureC
		}
	}
}

// gpuPower hands the engine the power draw from the last reading.
func (a *App) gpuPower(ctx context.Context) (map[int]float64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	power := make(map[int]float64, len(a.telemetry))
	for index, t := range a.telemetry {
		power[index] = t.PowerWatts
	}
	return power, nil
}

// ExportEstimates writes what this machine has measured to w, in the form
// estimates are shared in.
func ExportEstimates(ctx context.Context, cfg *config.Config, w io.Writer) error {
	definitions, err := miner.Builtin()
	if err != nil {
		return err
	}
	devices, _ := device.DetectAll(ctx)
	path := filepath.Join(cfg.General.DataDir, "stats.db")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("nothing has been measured yet: %w", err)
	}
	store, err := stats.Open(path)
	if err != nil {
		return err
	}
	defer store.Close()
	entries, err := engine.ExportEstimates(store, definitions, devices, time.Duration(cfg.General.Switching.BenchmarkTime))
	if err != nil {
		return err
	}
	out, err := estimate.Marshal(entries)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

// coinPools makes the coin pools that cfg has ready to mine at.
func (a *App) coinPools(cfg *config.Config) []pool.CoinPool {
	var pools []pool.CoinPool
	for _, name := range config.CoinPoolNames {
		if cfg.Pools.CoinPoolReady(name) != nil {
			continue
		}
		c := cfg.Pools.CoinPool(name)
		p := pool.NewCoinPool(name, pool.CoinPoolSettings{
			Wallets:      cfg.Pools.Wallets,
			Worker:       cfg.General.Worker,
			Region:       c.Region,
			ExcludeCoins: c.ExcludeCoins,
			Prices:       a.coinPrices.InBTC,
		})
		if p != nil {
			pools = append(pools, p)
		}
	}
	return pools
}

// readBalance asks the pools for the balances of the wallets, now and
// then, and at once when a wallet changes.
func (a *App) readBalance(ctx context.Context) {
	cfg := a.Config()
	z := cfg.Pools.Zpool
	wallet := cfg.Pools.ZpoolWallet()
	coinPools := a.coinPools(&cfg)
	key := fmt.Sprint(z.Enabled, wallet, "/", z.Currency, cfg.Pools.Wallets)
	for _, p := range coinPools {
		key += "/" + p.Name()
	}

	a.mu.Lock()
	due := key != a.balanceKey || time.Since(a.balanceAt) >= balanceInterval
	// an old figure is better than none, as long as it is of the same wallets
	var old []pool.Balance
	if key == a.balanceKey {
		old = a.balances.Balances
	}
	a.mu.Unlock()
	if !due || a.balanceFn == nil {
		return
	}
	keep := func(name string) (kept []pool.Balance) {
		for _, b := range old {
			if b.Pool == name {
				kept = append(kept, b)
			}
		}
		return kept
	}

	info := api.BalanceInfo{}
	var failed []string
	if z.Enabled && wallet != "" {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		balance, err := a.balanceFn(callCtx, &pool.Zpool{Wallet: wallet, Currency: z.Currency})
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			failed = append(failed, err.Error())
			info.Balances = append(info.Balances, keep("zpool")...)
		} else {
			info.Balances = append(info.Balances, balance)
		}
	}
	for _, p := range coinPools {
		if a.coinBalanceFn == nil {
			break
		}
		callCtx, cancel := context.WithTimeout(ctx, time.Minute)
		balances, err := a.coinBalanceFn(callCtx, p)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			failed = append(failed, err.Error())
			// the coins that did answer are shown fresh, the others as they were
			for _, b := range keep(p.Name()) {
				if !slices.ContainsFunc(balances, func(n pool.Balance) bool { return n.Currency == b.Currency }) {
					balances = append(balances, b)
				}
			}
		}
		info.Balances = append(info.Balances, balances...)
	}
	info.Error = strings.Join(failed, "; ")
	a.mu.Lock()
	a.balances, a.balanceKey, a.balanceAt = info, key, time.Now()
	a.mu.Unlock()
}

// cycle runs the engine with the current configuration until that changes
// or ctx is cancelled. Without a usable configuration it just waits.
func (a *App) cycle(ctx context.Context) error {
	cfg := a.Config()
	store, err := stats.Open(filepath.Join(cfg.General.DataDir, "stats.db"))
	if err != nil {
		return err
	}
	a.setStore(store)
	defer func() {
		a.setStore(nil)
		store.Close()
	}()
	if err := store.PruneRuns(time.Now().Add(-activityKept)); err != nil {
		return err
	}

	e, reason := a.build(ctx, &cfg, store)
	if e == nil {
		a.setEngine(nil, reason)
		a.logf("not mining: %s", reason)
		select {
		case <-ctx.Done():
		case <-a.reload:
		}
		return nil
	}

	a.setEngine(e, "")
	engineCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- e.Run(engineCtx) }()

	select {
	case err := <-done:
		a.setEngine(nil, "the engine stopped")
		return err
	case <-ctx.Done():
	case <-a.reload:
	}
	cancel()
	err = <-done
	a.setEngine(nil, "restarting")
	return err
}

// build makes an engine for cfg. A nil engine comes with the reason why the
// configuration is not ready to mine yet.
func (a *App) build(ctx context.Context, cfg *config.Config, store *stats.Store) (*engine.Engine, string) {
	if err := cfg.ReadyToMine(); err != nil {
		return nil, err.Error()
	}
	// a pool that is enabled but not ready is left out while another one is
	var sources []pool.Source
	z := cfg.Pools.Zpool
	if err := cfg.Pools.ZpoolReady(); err == nil {
		zpool := &pool.Zpool{
			Wallet:        cfg.Pools.ZpoolWallet(),
			Currency:      z.Currency,
			Worker:        cfg.General.Worker,
			Region:        z.Region,
			AlgoSwitching: z.AlgoSwitching,
			CoinMining:    z.CoinMining,
			Coins:         z.Coins,
			ExcludeCoins:  z.ExcludeCoins,
		}
		if err := checkPayoutCurrency(ctx, zpool, a.logf); err != nil {
			if ctx.Err() != nil {
				return nil, "stopping"
			}
			return nil, err.Error()
		}
		sources = append(sources, zpool)
	} else if z.Enabled {
		a.logf("zpool is left out: %v", err)
	}
	poolTLS := map[string]bool{"zpool": z.TLS}
	for _, p := range a.coinPools(cfg) {
		sources = append(sources, p)
	}
	for _, name := range config.CoinPoolNames {
		c := cfg.Pools.CoinPool(name)
		poolTLS[name] = c.TLS
		if err := cfg.Pools.CoinPoolReady(name); err != nil && c.Enabled {
			a.logf("%s is left out: %v", name, err)
		}
	}

	var devices []device.Device
	for _, d := range a.devices {
		if !slices.Contains(cfg.Devices.Exclude, d.ID) {
			devices = append(devices, d)
		}
	}

	deps := engine.Deps{
		Store:       store,
		Sources:     sources,
		Definitions: a.definitions,
		Devices:     devices,
		Launch:      engine.Launch,
		Power:       a.gpuPower,
		Memory:      sysinfo.ReadMemory,
		Logf:        a.logf,
	}
	power := cfg.General.Power
	coinbase := &rates.Coinbase{}
	deps.Rate = func(ctx context.Context) (float64, error) {
		return coinbase.BTCPrice(ctx, power.Currency)
	}
	if power.PricePerKWh > 0 {
		a.logf("electricity: %g %s per kWh", power.PricePerKWh, power.Currency)
	} else {
		a.logf("electricity is not counted (power.price_per_kwh is 0)")
	}

	miners := map[string]engine.MinerOptions{}
	for name, m := range cfg.Miners {
		miners[name] = engine.MinerOptions{Disabled: m.Disabled, ExcludeAlgorithms: m.ExcludeAlgorithms, ExtraArgs: m.ExtraArgs}
	}
	sw := cfg.General.Switching
	e, err := engine.New(engine.Options{
		Interval:          time.Duration(sw.Interval),
		MinRunTime:        time.Duration(sw.MinRunTime),
		BenchmarkTime:     time.Duration(sw.BenchmarkTime),
		HysteresisPercent: sw.HysteresisPercent,
		MineAtLoss:        sw.MineAtLoss,
		Estimates:         a.estimates(sw.UseEstimates, cfg.General.DataDir),
		PowerPricePerKWh:  power.PricePerKWh,
		PowerCurrency:     power.Currency,
		PoolTLS:           poolTLS,
		DataDir:           cfg.General.DataDir,
		CPUEnabled:        cfg.General.CPU.Enabled,
		CPUWatts:          cfg.General.CPU.Watts,
		CPUThreads:        cfg.General.CPU.Threads,
		Miners:            miners,
		OnlyAlgorithms:    cfg.Algorithms.Only,
		ExcludeAlgorithms: cfg.Algorithms.Exclude,
		Paused:            a.isPaused(),
		MinFreeRAMMiB:     cfg.General.Memory.MinFreeRAMGB * 1024,
		ReservedVRAMMiB:   cfg.General.Memory.ReservedVRAMGB * 1024,
	}, deps)
	if err != nil {
		// e.g. every device is excluded: a matter of configuration
		return nil, err.Error()
	}
	return e, ""
}

func (a *App) setEngine(e *engine.Engine, waiting string) {
	a.mu.Lock()
	a.engine, a.waiting = e, waiting
	a.mu.Unlock()
}

func (a *App) setStore(store *stats.Store) {
	a.mu.Lock()
	a.store = store
	a.mu.Unlock()
}

func (a *App) currentEngine() *engine.Engine {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.engine
}

func (a *App) isPaused() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.paused
}

// The methods below are api.Backend.

func (a *App) Waiting() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.waiting
}

func (a *App) Status() engine.Status {
	if e := a.currentEngine(); e != nil {
		return e.Status()
	}
	return engine.Status{Time: time.Now()}
}

func (a *App) System() api.System {
	system := api.System{Started: a.started, Paused: a.isPaused(), Notices: a.notices}
	if system.Notices == nil {
		system.Notices = []string{}
	}
	if memory, err := sysinfo.ReadMemory(); err == nil {
		system.Memory = &memory
	}
	return system
}

// SetPaused stops all mining or takes it up again. The pause holds across a
// change of the configuration, but not across a restart of the program.
func (a *App) SetPaused(paused bool) {
	a.mu.Lock()
	changed := a.paused != paused
	a.paused = paused
	e := a.engine
	a.mu.Unlock()
	if !changed {
		return
	}
	if e != nil {
		e.SetPaused(paused)
	} else if paused {
		a.logf("mining is paused")
	} else {
		a.logf("mining continues")
	}
}

func (a *App) Devices() []api.DeviceStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]api.DeviceStatus, len(a.devices))
	for i, d := range a.devices {
		out[i] = api.DeviceStatus{Device: d}
		if t, ok := a.telemetry[d.Index]; ok && d.Kind == device.GPU {
			out[i].Telemetry = &t
			out[i].MaxTemperatureC = a.maxTemp[d.Index]
		}
	}
	return out
}

func (a *App) Offers() []pool.Offer {
	if e := a.currentEngine(); e != nil {
		return e.Offers()
	}
	return nil
}

func (a *App) Balances() api.BalanceInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.balances
}

func (a *App) Activity() ([]stats.Run, error) {
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()
	if store == nil {
		return nil, nil
	}
	return store.Runs(time.Now().Add(-activityDays*24*time.Hour), activityListed)
}

// Config returns a copy of the configuration that the caller may change.
func (a *App) Config() config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return cloneConfig(a.cfg)
}

func cloneConfig(c *config.Config) config.Config {
	out := *c
	out.Pools.Zpool.Coins = slices.Clone(c.Pools.Zpool.Coins)
	out.Pools.Zpool.ExcludeCoins = slices.Clone(c.Pools.Zpool.ExcludeCoins)
	for _, name := range config.CoinPoolNames {
		out.Pools.CoinPool(name).ExcludeCoins = slices.Clone(c.Pools.CoinPool(name).ExcludeCoins)
	}
	out.Pools.Wallets = maps.Clone(c.Pools.Wallets)
	out.Algorithms.Only = slices.Clone(c.Algorithms.Only)
	out.Algorithms.Exclude = slices.Clone(c.Algorithms.Exclude)
	out.Devices.Exclude = slices.Clone(c.Devices.Exclude)
	out.Miners = make(config.Miners, len(c.Miners))
	for name, m := range c.Miners {
		m.ExcludeAlgorithms = slices.Clone(m.ExcludeAlgorithms)
		m.ExtraArgs = slices.Clone(m.ExtraArgs)
		out.Miners[name] = m
	}
	return out
}

// SaveConfig checks cfg, writes it to the configuration directory and makes
// the engine start anew with it.
func (a *App) SaveConfig(cfg config.Config) error {
	for name := range cfg.Miners {
		if _, ok := a.definitions[name]; !ok {
			return fmt.Errorf("unknown miner %q", name)
		}
	}
	for _, id := range cfg.Devices.Exclude {
		if !slices.ContainsFunc(a.devices, func(d device.Device) bool { return d.ID == id }) {
			return fmt.Errorf("unknown device %q", id)
		}
	}
	if err := config.SaveDir(a.configDir, &cfg); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = &cfg
	a.mu.Unlock()
	select {
	case a.reload <- struct{}{}:
	default:
	}
	return nil
}

func (a *App) Meta(ctx context.Context) api.Meta {
	meta := api.Meta{
		Version:    Version,
		ConfigDir:  a.configDir,
		Regions:    config.ZpoolRegions,
		CoinPools:  pool.CoinPools(),
		Currencies: a.payoutCurrencies(ctx),
	}
	if abs, err := filepath.Abs(a.configDir); err == nil {
		meta.ConfigDir = abs
	}
	for _, name := range miner.Names(a.definitions) {
		def := a.definitions[name]
		info := api.MinerInfo{Name: def.Name, Version: def.Version, Vendor: def.Vendor, FeePercent: def.Fee}
		for _, alg := range def.Algorithms {
			info.Algorithms = append(info.Algorithms, alg.Name)
		}
		meta.Miners = append(meta.Miners, info)
	}
	return meta
}

// payoutCurrencies asks zpool what it pays out in, at most every few
// minutes. Without an answer the list stays empty or old.
func (a *App) payoutCurrencies(ctx context.Context) []pool.PayoutCurrency {
	a.mu.Lock()
	cached, at := a.currencies, a.currenciesAt
	a.mu.Unlock()
	if time.Since(at) < currencyMaxAge {
		if cached == nil {
			return []pool.PayoutCurrency{}
		}
		return cached
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	fresh, err := (&pool.Zpool{}).PayoutCurrencies(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	// a failed try is not repeated at once either
	a.currenciesAt = time.Now()
	if err == nil {
		a.currencies = fresh
	}
	if a.currencies == nil {
		return []pool.PayoutCurrency{}
	}
	return a.currencies
}

func (a *App) ResetBenchmarks(which string, hashKeys []string) {
	if e := a.currentEngine(); e != nil {
		e.ResetBenchmarks(which, hashKeys)
	}
}

// Benchmarks returns the engine's benchmark list, each entry with the name
// of its newest miner log.
func (a *App) Benchmarks() []engine.BenchmarkStatus {
	e := a.currentEngine()
	if e == nil {
		return nil
	}
	list := slices.Clone(e.Benchmarks())

	// log files are named miner-algorithm-group-date-time.log, and the
	// date sorts by time
	newest := map[string]string{}
	entries, _ := os.ReadDir(a.logDir())
	for _, entry := range entries {
		name := entry.Name()
		if len(name) < len("-20060102-150405.log") || filepath.Ext(name) != ".log" {
			continue
		}
		prefix := name[:len(name)-len("-20060102-150405.log")]
		if name > newest[prefix] {
			newest[prefix] = name
		}
	}
	for i, b := range list {
		list[i].Log = newest[b.Miner+"-"+b.Algorithm+"-"+b.Group]
	}
	return list
}

func (a *App) Enable(hashKey string) {
	if e := a.currentEngine(); e != nil {
		e.Enable(hashKey)
	}
}

func (a *App) ResetWatchdog() {
	if e := a.currentEngine(); e != nil {
		e.ResetWatchdog()
	}
}

func (a *App) Totals() ([]stats.Total, error) {
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()
	if store == nil {
		return nil, nil
	}
	return store.Totals()
}

func (a *App) Log() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.log)
}

func (a *App) logDir() string {
	cfg := a.Config()
	return filepath.Join(cfg.General.DataDir, "logs")
}

// MinerLogs lists the newest miner logs, newest first.
func (a *App) MinerLogs() ([]api.LogFile, error) {
	entries, err := os.ReadDir(a.logDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []api.LogFile
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		files = append(files, api.LogFile{Name: entry.Name(), Size: info.Size(), Modified: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Modified.After(files[j].Modified) })
	if len(files) > minerLogsListed {
		files = files[:minerLogsListed]
	}
	return files, nil
}

var (
	logNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.log$`)
	// colours and cursor movements a miner writes for a terminal
	ansiRE = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")
)

// MinerLog returns the end of one miner log, without terminal control codes.
// name must be a plain file name from MinerLogs.
func (a *App) MinerLog(name string) (string, error) {
	if !logNameRE.MatchString(name) {
		return "", errors.New("not a log file name")
	}
	f, err := os.Open(filepath.Join(a.logDir(), name))
	if err != nil {
		return "", fs.ErrNotExist
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > minerLogTail {
		if _, err := f.Seek(-minerLogTail, io.SeekEnd); err != nil {
			return "", err
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, minerLogTail))
	if err != nil {
		return "", err
	}
	return ansiRE.ReplaceAllString(string(data), ""), nil
}
