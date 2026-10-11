// Package engine runs the mining rounds: it measures the running miners,
// asks the selector what each device group should mine, and starts and stops
// miner programs to match.
package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"hashsteer/internal/device"
	"hashsteer/internal/estimate"
	"hashsteer/internal/miner"
	"hashsteer/internal/pool"
	"hashsteer/internal/selector"
	"hashsteer/internal/stats"
	"hashsteer/internal/sysinfo"
)

const (
	pollInterval = 10 * time.Second
	// a miner needs this long to connect and build its DAG; what it
	// reports before says nothing about its speed
	warmup = 90 * time.Second
	// tolerance for hashrate samples against the stored average
	hashrateTolerance = 0.1

	offerInterval = 2 * time.Minute
	// offers a pool has not confirmed for this long are not mined on
	offerMaxAge  = 30 * time.Minute
	rateInterval = 15 * time.Minute

	// a miner without any hashrate for this long is stopped
	watchdogTimeout = 5 * time.Minute
	// this many failures within crashWindow take a candidate out
	maxCrashes       = 3
	crashWindow      = time.Hour
	crashPenalty     = time.Hour
	benchmarkPenalty = 6 * time.Hour

	stopGrace       = 10 * time.Second
	sampleRetention = 7 * 24 * time.Hour
)

// Options are the settings of the engine.
type Options struct {
	Interval          time.Duration
	MinRunTime        time.Duration
	BenchmarkTime     time.Duration
	HysteresisPercent float64
	MineAtLoss        bool
	// PowerPricePerKWh is in PowerCurrency, 0 to ignore electricity.
	PowerPricePerKWh float64
	PowerCurrency    string
	// TLS connects to the pools with TLS where pool and miner can; PoolTLS
	// says so for single pools, by name, and wins over TLS.
	TLS     bool
	PoolTLS map[string]bool
	DataDir string

	// Estimates are hashrates measured on other machines. They stand in
	// until this one has measured its own; nil does without them.
	Estimates *estimate.Table

	// CPUEnabled lets the CPU mine as well.
	CPUEnabled bool
	// CPUWatts is the assumed power draw of the CPU while mining; it
	// cannot be measured the way a GPU's can.
	CPUWatts float64
	// CPUThreads limits the CPU miner, 0 lets the miner decide.
	CPUThreads int

	// Miners holds the settings per miner program, by name.
	Miners map[string]MinerOptions
	// OnlyAlgorithms limits mining to these, empty allows all;
	// ExcludeAlgorithms are never mined.
	OnlyAlgorithms    []string
	ExcludeAlgorithms []string

	// Paused starts the engine without mining.
	Paused bool
	// MinFreeRAMMiB of the memory programs may reserve is left alone, and
	// ReservedVRAMMiB of every GPU's memory: a miner that needs more than
	// the rest is not started.
	MinFreeRAMMiB   float64
	ReservedVRAMMiB float64
}

// MinerOptions are the user's settings for one miner program.
type MinerOptions struct {
	Disabled          bool
	ExcludeAlgorithms []string
	ExtraArgs         []string
}

// Deps is what the engine works with. Everything that touches the outside
// world comes in here.
type Deps struct {
	Store       *stats.Store
	Sources     []pool.Source
	Definitions map[string]*miner.Definition
	Devices     []device.Device
	Launch      LaunchFunc
	// Rate returns the price of one bitcoin in the power currency. Only
	// needed with a power price.
	Rate func(ctx context.Context) (float64, error)
	// Power returns the measured power draw of GPUs by vendor index, for
	// miners that do not report their own. May be nil.
	Power func(ctx context.Context) (map[int]float64, error)
	// Memory returns how much memory the machine has left. May be nil,
	// then memory is not checked.
	Memory func() (sysinfo.Memory, error)
	Logf   func(format string, args ...any)
}

// Engine mines with every device group on what pays best.
type Engine struct {
	opts   Options
	deps   Deps
	groups []*group

	offers    map[string][]pool.Offer // by pool name
	offersAt  map[string]time.Time
	lastFetch time.Time

	rate      float64 // power currency per BTC, 0 if unknown
	rateTried time.Time
	params    selector.Params
	paused    bool
	// memory that miners started a moment ago will take but have not yet
	reserved []reservation

	// the miner version each stored hashrate was measured with
	versions map[string]string

	// what other goroutines may touch
	mu         sync.Mutex
	status     Status
	benchmarks []BenchmarkStatus
	offerSet   []pool.Offer
	commands   []func()
	wake       chan struct{}
}

// reservation is memory a just started miner is about to take.
type reservation struct {
	mib   float64
	until time.Time
}

// reservationTime is how long a started miner may need to take its memory.
const reservationTime = 3 * time.Minute

// group is a set of identical devices that mine together with one miner.
type group struct {
	name    string
	vendor  string
	devices []device.Device

	run *run
	// failures and the time a miner on an algorithm is taken out until,
	// by hashrate key
	crashes  map[string][]time.Time
	disabled map[string]time.Time
	// why a miner on an algorithm failed the last time
	reasons map[string]string

	// what the last round saw and decided, for the status
	options  []option
	decision selector.Decision
	// candidates left out in the last round for lack of memory
	memoryBlocked int
}

// option is a candidate together with what is needed to start it. Its ID
// names miner, algorithm, devices and the offer; hashKey leaves the offer
// out, because the speed of a miner does not depend on where it mines.
type option struct {
	selector.Candidate
	hashKey  string
	powerKey string
	// measured is how much mining time the stored hashrate rests on
	measured time.Duration
	// estimated says the hashrate is not this machine's own measurement
	estimated bool
	def       *miner.Definition
	algo      miner.Algorithm
	offer     pool.Offer
}

// label names the option for logs: "rigel/kawpow at zpool:CLORE".
func (o option) label() string {
	return o.def.Name + "/" + o.algo.Name + " at " + o.offer.Label()
}

// run is a started miner.
type run struct {
	option
	miner     Miner
	window    miner.Window
	benchmark bool
	started   time.Time
	// only samples after measureFrom count
	measureFrom time.Time
	lastStore   time.Time
	lastHash    time.Time
	apiErrors   int

	// what the run has delivered so far, for the activity log
	measuredTime time.Duration
	hashSeconds  float64 // sum of hashrate times seconds
	earnedBTC    float64
	energyWh     float64
}

// New prepares an engine for the devices in deps.
func New(opts Options, deps Deps) (*Engine, error) {
	if deps.Store == nil || deps.Launch == nil || deps.Logf == nil || len(deps.Sources) == 0 {
		return nil, errors.New("engine: store, launch, log and at least one pool are required")
	}
	if opts.PowerPricePerKWh > 0 && deps.Rate == nil {
		return nil, errors.New("engine: a power price needs an exchange rate source")
	}
	e := &Engine{
		opts:     opts,
		deps:     deps,
		groups:   groupDevices(deps.Devices, opts.CPUEnabled),
		offers:   map[string][]pool.Offer{},
		offersAt: map[string]time.Time{},
		wake:     make(chan struct{}, 1),
		paused:   opts.Paused,
	}
	if len(e.groups) == 0 {
		return nil, errors.New("no device found to mine with")
	}
	return e, nil
}

// groupDevices puts the GPUs of the same model into one group each, and the
// CPU into a group of its own if it is to mine. The GPU groups come first.
func groupDevices(devices []device.Device, cpu bool) []*group {
	newGroup := func(name, vendor string) *group {
		return &group{name: name, vendor: vendor, crashes: map[string][]time.Time{}, disabled: map[string]time.Time{}, reasons: map[string]string{}}
	}
	byName := map[string]*group{}
	var cpuGroup *group
	for _, d := range devices {
		switch d.Kind {
		case device.GPU:
			name := groupName(d.Model)
			g := byName[d.Vendor+"/"+name]
			if g == nil {
				g = newGroup(name, d.Vendor)
				byName[d.Vendor+"/"+name] = g
			}
			g.devices = append(g.devices, d)
		case device.CPU:
			if cpu {
				if cpuGroup == nil {
					cpuGroup = newGroup("CPU", miner.VendorCPU)
				}
				cpuGroup.devices = append(cpuGroup.devices, d)
			}
		}
	}
	groups := make([]*group, 0, len(byName)+1)
	for _, g := range byName {
		sort.Slice(g.devices, func(i, j int) bool { return g.devices[i].Index < g.devices[j].Index })
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].devices[0].Index < groups[j].devices[0].Index })
	if cpuGroup != nil {
		groups = append(groups, cpuGroup)
	}
	return groups
}

// groupName shortens a model name to letters and digits: "RTX3060".
func groupName(model string) string {
	model = strings.TrimPrefix(model, "GeForce ")
	name := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, model)
	if name == "" {
		return "GPU"
	}
	return name
}

func (g *group) deviceIDs() []string {
	ids := make([]string, len(g.devices))
	for i, d := range g.devices {
		ids[i] = d.ID
	}
	return ids
}

func (g *group) deviceIndexes() []int {
	indexes := make([]int, len(g.devices))
	for i, d := range g.devices {
		indexes[i] = d.Index
	}
	return indexes
}

// Run mines until ctx is cancelled, then stops all miners.
func (e *Engine) Run(ctx context.Context) error {
	defer func() { e.stopAll(time.Now()) }()
	if _, err := e.deps.Store.PruneSamples(time.Now().Add(-sampleRetention)); err != nil {
		return err
	}
	for _, g := range e.groups {
		e.deps.Logf("group %s: %s", g.name, strings.Join(g.deviceIDs(), ", "))
	}

	for {
		if err := e.round(ctx, time.Now()); err != nil {
			return err
		}
		next := time.Now().Add(e.opts.Interval)
	wait:
		for time.Now().Before(next) {
			select {
			case <-ctx.Done():
				return nil
			case <-e.wake:
				// a command from the interface does not wait for the
				// end of the round
				break wait
			case <-time.After(pollInterval):
			}
			// a miner that died does not wait either
			if e.poll(ctx, time.Now()) {
				break
			}
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// do queues fn to run on the engine's goroutine at the start of the next
// round, and starts that round at once. It is how other goroutines change
// anything in the engine.
func (e *Engine) do(fn func()) {
	e.mu.Lock()
	e.commands = append(e.commands, fn)
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) runCommands() {
	e.mu.Lock()
	commands := e.commands
	e.commands = nil
	e.mu.Unlock()
	for _, fn := range commands {
		fn()
	}
}

// ResetBenchmark forgets what was measured for a miner on an algorithm, so
// that it is benchmarked again. It is safe to call from any goroutine.
func (e *Engine) ResetBenchmark(hashKey string) {
	e.ResetBenchmarks("", []string{hashKey})
}

// Enable puts a miner on an algorithm back into the selection before its
// penalty is over. It is safe to call from any goroutine.
func (e *Engine) Enable(hashKey string) {
	e.do(func() {
		for _, g := range e.groups {
			if _, out := g.disabled[hashKey]; out {
				delete(g.disabled, hashKey)
				delete(g.crashes, hashKey)
				e.deps.Logf("%s: %s is enabled again", g.name, hashKey)
			}
		}
	})
}

// SetPaused stops all mining, or takes it up again. It is safe to call from
// any goroutine.
func (e *Engine) SetPaused(paused bool) {
	e.do(func() {
		if e.paused == paused {
			return
		}
		e.paused = paused
		if paused {
			e.deps.Logf("mining is paused")
		} else {
			e.deps.Logf("mining continues")
		}
	})
}

// ResetWatchdog forgets all recent failures: what was taken out is let back
// in, and the counters start over. It is safe to call from any goroutine.
func (e *Engine) ResetWatchdog() {
	e.do(func() {
		n := 0
		for _, g := range e.groups {
			n += len(g.disabled) + len(g.crashes)
			g.disabled = map[string]time.Time{}
			g.crashes = map[string][]time.Time{}
			g.reasons = map[string]string{}
		}
		e.deps.Logf("watchdog was reset, %d entries cleared", n)
	})
}

// round stores what was measured, checks on the running miners and makes
// every group mine what the selector picks.
func (e *Engine) round(ctx context.Context, now time.Time) error {
	if err := e.store(now); err != nil {
		return err
	}
	e.runCommands()
	if err := e.refreshOffers(ctx, now); err != nil {
		return err
	}
	e.refreshRate(ctx, now)

	if e.paused {
		for _, g := range e.groups {
			if g.run != nil {
				e.deps.Logf("%s: stopping %s", g.name, g.run.label())
				e.stop(g, stats.EndPaused, now)
			}
			g.options, g.memoryBlocked = nil, 0
			g.decision = selector.Decision{Reason: "paused"}
		}
		e.publish(now)
		return e.listBenchmarks(now)
	}

	params := selector.Params{
		HysteresisPercent: e.opts.HysteresisPercent,
		MineAtLoss:        e.opts.MineAtLoss,
	}
	if e.opts.PowerPricePerKWh > 0 && e.rate > 0 {
		params.PowerCostBTCPerKWh = e.opts.PowerPricePerKWh / e.rate
	}
	e.params = params
	defer e.publish(now)

	for _, g := range e.groups {
		if ctx.Err() != nil {
			return nil
		}
		e.checkRun(g, now)

		options, err := e.options(g, now)
		if err != nil {
			return err
		}
		candidates := make([]selector.Candidate, len(options))
		byID := make(map[string]option, len(options))
		for i, o := range options {
			candidates[i] = o.Candidate
			byID[o.ID] = o
		}

		running, locked := "", false
		if r := g.run; r != nil {
			running = r.ID
			locked = r.benchmark || now.Sub(r.started) < e.opts.MinRunTime
			// a finished benchmark frees the group for the next one
			if o, ok := byID[running]; ok && r.benchmark && o.Benchmarked {
				r.benchmark = false
				locked = false
				e.deps.Logf("%s: benchmark of %s done: %s", g.name, r.hashKey, formatHashrate(o.Hashrate))
			}
		}

		decision := selector.Decide(candidates, running, locked, params)
		if len(candidates) == 0 && len(g.disabled) > 0 {
			decision.Reason = fmt.Sprintf("%d candidates are taken out after failures", len(g.disabled))
		}
		if decision.ID == "" && g.memoryBlocked > 0 {
			decision.Reason = fmt.Sprintf("not enough free memory for %d candidates that need a lot of it", g.memoryBlocked)
		}
		if decision.ID != running {
			if g.run != nil {
				e.deps.Logf("%s: stopping %s", g.name, g.run.label())
				e.stop(g, stats.EndSwitched, now)
			}
			if decision.ID != "" {
				e.start(ctx, g, byID[decision.ID], decision, now)
				if g.run == nil {
					decision.Reason = "the miner could not be started"
				}
			}
		} else if g.run != nil {
			// keep the price and profit figures of the running miner current
			if o, ok := byID[running]; ok {
				g.run.option = o
			}
			// a benchmark that was reset is measured again by the miner
			// that is running anyway
			if decision.Benchmark && !g.run.benchmark {
				g.run.benchmark = true
				e.deps.Logf("%s: benchmarking %s again", g.name, g.run.label())
			}
		}
		g.options, g.decision = options, decision
		e.report(g, decision, params)
	}
	return e.listBenchmarks(now)
}

// checkRun stops a miner that exited or went silent and records the failure.
func (e *Engine) checkRun(g *group, now time.Time) {
	r := g.run
	if r == nil {
		return
	}
	select {
	case <-r.miner.Done():
		e.deps.Logf("%s: %s exited on its own (%s)", g.name, r.label(), r.miner.ExitText())
		e.stop(g, stats.EndCrashed, now)
		e.failed(g, r.hashKey, now, false, "exited on its own: "+r.miner.ExitText())
		return
	default:
	}

	silentSince := r.lastHash
	if silentSince.Before(r.measureFrom) {
		silentSince = r.measureFrom
	}
	if now.Sub(silentSince) < silenceLimit(r.algo, !r.lastHash.IsZero()) {
		return
	}
	e.deps.Logf("%s: %s reported no hashrate for %v", g.name, r.label(), now.Sub(silentSince).Round(time.Second))
	e.stop(g, stats.EndNoHashrate, now)
	e.failed(g, r.hashKey, now, r.benchmark, fmt.Sprintf("reported no hashrate for %v", now.Sub(silentSince).Round(time.Second)))
}

// silenceLimit is how long a miner may go without reporting a hashrate.
// Before its first one it gets the time its algorithm needs to prepare,
// if that is more than the usual.
func silenceLimit(a miner.Algorithm, hashed bool) time.Duration {
	if startup := time.Duration(a.StartupMinutes) * time.Minute; !hashed && startup > watchdogTimeout {
		return startup
	}
	return watchdogTimeout
}

// failed counts a failure of a miner on an algorithm. A benchmark that
// produced nothing takes it out at once, a miner that keeps failing after a
// few tries.
func (e *Engine) failed(g *group, hashKey string, now time.Time, benchmark bool, reason string) {
	g.reasons[hashKey] = reason
	if benchmark {
		g.disabled[hashKey] = now.Add(benchmarkPenalty)
		e.deps.Logf("%s: benchmark of %s failed, not tried again for %v", g.name, hashKey, benchmarkPenalty)
		return
	}
	recent := g.crashes[hashKey][:0]
	for _, t := range g.crashes[hashKey] {
		if now.Sub(t) < crashWindow {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	if len(recent) < maxCrashes {
		g.crashes[hashKey] = recent
		return
	}
	delete(g.crashes, hashKey)
	g.disabled[hashKey] = now.Add(crashPenalty)
	e.deps.Logf("%s: %s failed %d times within %v, not tried again for %v", g.name, hashKey, maxCrashes, crashWindow, crashPenalty)
}

// allowed reports whether the user's settings permit the miner program on
// the algorithm.
func (e *Engine) allowed(def *miner.Definition, algorithm string) bool {
	if len(e.opts.OnlyAlgorithms) > 0 && !slices.Contains(e.opts.OnlyAlgorithms, algorithm) {
		return false
	}
	if slices.Contains(e.opts.ExcludeAlgorithms, algorithm) {
		return false
	}
	m := e.opts.Miners[def.Name]
	return !m.Disabled && !slices.Contains(m.ExcludeAlgorithms, algorithm)
}

func powerKeyFor(hashKey string) string {
	return "power/" + strings.TrimPrefix(hashKey, "hashrate/")
}

// priceKey names the price stat of an offer.
func priceKey(o pool.Offer) string {
	key := stats.PriceKey(o.Pool, o.Algorithm)
	if o.Coin != "" {
		key += "/" + o.Coin
	}
	return key
}

// estimate returns what the devices of g together are expected to do with a
// miner on an algorithm, going by what others measured with the same model.
func (e *Engine) estimate(g *group, def *miner.Definition, a miner.Algorithm) (estimate.Entry, bool) {
	if len(g.devices) == 0 {
		return estimate.Entry{}, false
	}
	entry, ok := e.opts.Estimates.Lookup(g.devices[0].Model, def.Name, a.Name)
	if !ok {
		return estimate.Entry{}, false
	}
	n := float64(len(g.devices))
	entry.Hashrate *= n
	entry.PowerWatts *= n
	return entry, true
}

// options lists what group g could mine right now: every permitted miner on
// every algorithm it supports, once per offer for that algorithm.
func (e *Engine) options(g *group, now time.Time) ([]option, error) {
	for id, until := range g.disabled {
		if !now.Before(until) {
			delete(g.disabled, id)
		}
	}

	freeMiB, checkMemory := e.freeMemory(g, now)
	g.memoryBlocked = 0

	var options []option
	for _, name := range miner.Names(e.deps.Definitions) {
		def := e.deps.Definitions[name]
		if def.Vendor != g.vendor || !def.Available() {
			continue
		}
		for _, a := range def.Algorithms {
			if !e.fits(g, a) || !e.allowed(def, a.Name) {
				continue
			}
			offers := e.offersFor(a.Name, now)
			if len(offers) == 0 {
				continue
			}
			hashKey := stats.HashrateKey(def.Name, g.deviceIDs(), a.Name)
			if _, out := g.disabled[hashKey]; out {
				continue
			}
			// a miner that is running has its memory already
			running := g.run != nil && g.run.hashKey == hashKey
			if checkMemory && !running && memoryNeed(g, a) > freeMiB {
				g.memoryBlocked++
				continue
			}
			base := option{
				Candidate: selector.Candidate{FeePercent: def.FeePercent(a)},
				hashKey:   hashKey,
				powerKey:  powerKeyFor(hashKey),
				def:       def,
				algo:      a,
			}
			hashrate, ok, err := e.deps.Store.Load(hashKey)
			if err != nil {
				return nil, err
			}
			if ok {
				base.measured = hashrate.Duration
			}
			guess, guessed := e.estimate(g, def, a)
			switch {
			case ok && hashrate.Duration >= e.opts.BenchmarkTime:
				base.Hashrate = hashrate.Average(stats.Week)
				base.Benchmarked = true
			case guessed:
				// good enough to compare with; if it is mined, it is
				// measured, and the measurement takes this figure's place
				base.Hashrate = guess.Hashrate
				base.Benchmarked, base.estimated = true, true
			}
			power, ok, err := e.deps.Store.Load(base.powerKey)
			if err != nil {
				return nil, err
			}
			if ok {
				base.PowerWatts = power.Average(stats.Week)
			} else if base.estimated {
				base.PowerWatts = guess.PowerWatts
			}

			for _, offer := range offers {
				o := base
				o.offer = offer
				o.ID = hashKey + "@" + offer.Label()
				// the hourly average rather than the latest figure: a
				// price spike of a few minutes is no reason to restart
				o.Price = offer.Price
				price, ok, err := e.deps.Store.Load(priceKey(offer))
				if err != nil {
					return nil, err
				}
				if ok && !price.Empty() {
					o.Price = price.Average(stats.Hour)
				}
				options = append(options, o)
			}
		}
	}

	// what is not measured yet is measured once, where it pays best; the
	// other offers for it would only be more benchmarks of the same thing
	best := map[string]option{}
	for _, o := range options {
		if b, ok := best[o.hashKey]; !ok || o.Price > b.Price {
			best[o.hashKey] = o
		}
	}
	kept := options[:0]
	for _, o := range options {
		running := g.run != nil && g.run.ID == o.ID
		if o.Benchmarked || running || best[o.hashKey].ID == o.ID {
			kept = append(kept, o)
		}
	}
	return kept, nil
}

// fits reports whether every device of g has the memory algorithm a needs,
// with the reserve the user wants to keep.
func (e *Engine) fits(g *group, a miner.Algorithm) bool {
	if a.MinMemoryMiB <= 0 {
		return true
	}
	for _, d := range g.devices {
		if float64(d.MemoryMiB)-e.opts.ReservedVRAMMiB < float64(a.MinMemoryMiB) {
			return false
		}
	}
	return true
}

// memoryNeed estimates the main memory a miner reserves for algorithm a on
// group g: the operating system backs what a miner puts on a GPU, a DAG
// above all, with the same amount of main memory.
func memoryNeed(g *group, a miner.Algorithm) float64 {
	return float64(a.MinMemoryMiB * len(g.devices))
}

// freeMemory returns how much memory a new miner of g could reserve. ok is
// false if that is not known and memory is not to be checked.
func (e *Engine) freeMemory(g *group, now time.Time) (mib float64, ok bool) {
	if e.deps.Memory == nil {
		return 0, false
	}
	memory, err := e.deps.Memory()
	if err != nil {
		return 0, false
	}
	free := memory.CommitFreeMiB - e.opts.MinFreeRAMMiB

	// miners started a moment ago have not taken their share yet
	kept := e.reserved[:0]
	for _, r := range e.reserved {
		if now.Before(r.until) {
			kept = append(kept, r)
			free -= r.mib
		}
	}
	e.reserved = kept
	// and what the group's own miner holds comes back when it is replaced
	if r := g.run; r != nil && now.Sub(r.started) >= reservationTime {
		free += memoryNeed(g, r.algo)
	}
	return free, true
}

// wantsTLS reports whether the connection to a pool is to use TLS.
func (e *Engine) wantsTLS(pool string) bool {
	if want, ok := e.opts.PoolTLS[pool]; ok {
		return want
	}
	return e.opts.TLS
}

// offersFor returns the fresh offers for an algorithm.
func (e *Engine) offersFor(algorithm string, now time.Time) []pool.Offer {
	var offers []pool.Offer
	for _, source := range e.deps.Sources {
		name := source.Name()
		if now.Sub(e.offersAt[name]) > offerMaxAge {
			continue
		}
		for _, offer := range e.offers[name] {
			if offer.Algorithm == algorithm {
				offers = append(offers, offer)
			}
		}
	}
	return offers
}

// refreshOffers asks the pools for their prices and feeds the price stats.
// A pool that does not answer keeps its last offers for a while.
func (e *Engine) refreshOffers(ctx context.Context, now time.Time) error {
	if !e.lastFetch.IsZero() && now.Sub(e.lastFetch) < offerInterval {
		return nil
	}
	e.lastFetch = now

	wanted := map[string]bool{}
	for _, def := range e.deps.Definitions {
		for _, a := range def.Algorithms {
			wanted[a.Name] = true
		}
	}
	for _, source := range e.deps.Sources {
		name := source.Name()
		offers, err := source.Fetch(ctx)
		if err != nil {
			if ctx.Err() == nil {
				e.deps.Logf("%s: %v", name, err)
			}
			continue
		}
		span := offerInterval
		if last, ok := e.offersAt[name]; ok {
			span = now.Sub(last)
		}
		e.offers[name], e.offersAt[name] = offers, now

		for _, offer := range offers {
			if !wanted[offer.Algorithm] {
				continue
			}
			key := priceKey(offer)
			st, _, err := e.deps.Store.Load(key)
			if err != nil {
				return err
			}
			st.Update(offer.Price, span, now, 0)
			if err := e.deps.Store.Save(key, st); err != nil {
				return err
			}
		}
	}

	var all []pool.Offer
	for _, source := range e.deps.Sources {
		if now.Sub(e.offersAt[source.Name()]) <= offerMaxAge {
			all = append(all, e.offers[source.Name()]...)
		}
	}
	e.mu.Lock()
	e.offerSet = all
	e.mu.Unlock()
	return nil
}

// Offers returns what the pools pay according to the last answer of each.
// It is safe to call from any goroutine.
func (e *Engine) Offers() []pool.Offer {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.offerSet
}

// refreshRate updates the bitcoin price. Without one, electricity cannot be
// priced and is left out until the next try.
func (e *Engine) refreshRate(ctx context.Context, now time.Time) {
	// the price is wanted in any case, to show earnings in real money; only
	// the cost of electricity depends on it
	if e.deps.Rate == nil {
		return
	}
	if !e.rateTried.IsZero() && now.Sub(e.rateTried) < rateInterval {
		return
	}
	e.rateTried = now
	rate, err := e.deps.Rate(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if e.rate > 0 {
			e.deps.Logf("exchange rate: %v, keeping %.2f %s per BTC", err, e.rate, e.opts.PowerCurrency)
		} else {
			e.deps.Logf("exchange rate: %v, no %s figures for now", err, e.opts.PowerCurrency)
		}
		return
	}
	e.rate = rate
}

func (e *Engine) start(ctx context.Context, g *group, o option, decision selector.Decision, now time.Time) {
	what := "starting"
	if decision.Benchmark {
		what = "benchmarking"
	}
	e.deps.Logf("%s: %s %s (%s)", g.name, what, o.label(), decision.Reason)

	m, err := e.deps.Launch(ctx, LaunchSpec{
		Definition: o.def,
		Algorithm:  o.algo,
		Devices:    g.devices,
		Offer:      o.offer,
		TLS:        e.wantsTLS(o.offer.Pool) && o.offer.TLSPort != 0 && o.def.SupportsTLS(),
		Threads:    e.threads(g),
		ExtraArgs:  e.opts.Miners[o.def.Name].ExtraArgs,
		DataDir:    e.opts.DataDir,
		Tag:        g.name,
	})
	if err != nil {
		if ctx.Err() == nil {
			e.deps.Logf("%s: could not start %s: %v", g.name, o.label(), err)
			e.failed(g, o.hashKey, now, false, "could not be started: "+err.Error())
		}
		return
	}
	e.deps.Logf("%s: %s is running (%s)", g.name, o.def.Name, m.Describe())
	if need := memoryNeed(g, o.algo); need > 0 {
		e.reserved = append(e.reserved, reservation{mib: need, until: now.Add(reservationTime)})
	}
	g.run = &run{
		option:      o,
		miner:       m,
		benchmark:   decision.Benchmark,
		started:     now,
		measureFrom: now.Add(warmup),
		lastStore:   now,
	}
}

// threads returns the thread limit for the miner of g, 0 for none.
func (e *Engine) threads(g *group) int {
	if g.vendor == miner.VendorCPU {
		return e.opts.CPUThreads
	}
	return 0
}

// stop ends the miner of g and writes the run into the activity log. The
// stop gets its own context: it must also work while the program is
// shutting down.
func (e *Engine) stop(g *group, end string, now time.Time) {
	r := g.run
	if r == nil {
		return
	}
	if err := r.miner.Stop(context.Background()); err != nil {
		e.deps.Logf("%s: stop: %v", g.name, err)
	}
	g.run = nil

	record := stats.Run{
		Group:     g.name,
		Miner:     r.def.Name,
		Algorithm: r.algo.Name,
		Pool:      r.offer.Pool,
		Coin:      r.offer.Coin,
		Started:   r.started,
		Ended:     now,
		Benchmark: r.benchmark,
		End:       end,
		EarnedBTC: r.earnedBTC,
		EnergyKWh: r.energyWh / 1000,
	}
	if seconds := r.measuredTime.Seconds(); seconds > 0 {
		record.Hashrate = r.hashSeconds / seconds
	}
	if last, ok := r.window.Last(); ok {
		shares := last.For(r.algo.NameForMiner())
		record.Accepted, record.Rejected = shares.Accepted, shares.Rejected+shares.Invalid
	}
	if err := e.deps.Store.AddRun(record); err != nil {
		e.deps.Logf("activity log: %v", err)
	}
}

// stopAll stops every miner, as of now.
func (e *Engine) stopAll(now time.Time) {
	// what the last minutes delivered still counts
	if err := e.store(now); err != nil {
		e.deps.Logf("storing the last measurements: %v", err)
	}
	for _, g := range e.groups {
		if g.run != nil {
			e.deps.Logf("%s: stopping %s", g.name, g.run.label())
			e.stop(g, stats.EndStopped, now)
		}
	}
	e.publish(now)
}

// poll reads the statistics of all running miners. It reports whether one
// of them has exited.
func (e *Engine) poll(ctx context.Context, now time.Time) (exited bool) {
	defer e.publish(now)
	var gpuPower map[int]float64
	for _, g := range e.groups {
		r := g.run
		if r == nil {
			continue
		}
		select {
		case <-r.miner.Done():
			exited = true
			continue
		default:
		}

		sample, err := r.miner.Poll(ctx)
		if err != nil {
			// a miner that is still starting does not answer yet
			r.apiErrors++
			if ctx.Err() == nil && now.After(r.measureFrom) && r.apiErrors%6 == 1 {
				e.deps.Logf("%s: %s api: %v", g.name, r.def.Name, err)
			}
			continue
		}
		r.apiErrors = 0
		sample.Time = now
		if sample.PowerWatts == 0 && g.vendor == miner.VendorCPU {
			sample.PowerWatts = e.opts.CPUWatts
		} else if sample.PowerWatts == 0 && e.deps.Power != nil {
			if gpuPower == nil {
				gpuPower, _ = e.deps.Power(ctx)
				if gpuPower == nil {
					gpuPower = map[int]float64{}
				}
			}
			for _, index := range g.deviceIndexes() {
				sample.PowerWatts += gpuPower[index]
			}
		}
		r.window.Add(sample)
		if sample.For(r.algo.NameForMiner()).Hashrate > 0 {
			r.lastHash = now
		}
	}
	return exited
}

// store turns the samples since the last round into one value per miner and
// feeds the hashrate and power stats with it.
func (e *Engine) store(now time.Time) error {
	for _, g := range e.groups {
		r := g.run
		if r == nil {
			continue
		}
		since := r.lastStore
		if since.Before(r.measureFrom) {
			since = r.measureFrom
		}
		if !now.After(since) {
			continue
		}
		hashrate, power, n := r.window.Average(r.algo.NameForMiner(), since)
		r.lastStore = now
		r.window.Prune(now.Add(-2 * e.opts.Interval))
		span := now.Sub(since)
		if n > 0 {
			// the activity log counts every measured second, also the ones
			// in which the miner stood still
			r.measuredTime += span
			r.hashSeconds += hashrate * span.Seconds()
			r.earnedBTC += hashrate * r.Price * (1 - r.FeePercent/100) * span.Hours() / 24
			r.energyWh += power * span.Hours()
		}
		if n == 0 || hashrate <= 0 {
			continue
		}

		st, _, err := e.deps.Store.Load(r.hashKey)
		if err != nil {
			return err
		}
		// while the benchmark runs there is no trusted average yet to
		// judge a sample by: every interval counts, also one in which the
		// pool switched coins and the miner stood still for a while
		tolerance := hashrateTolerance
		if st.Duration < e.opts.BenchmarkTime {
			tolerance = 0
		}
		outcome := st.Update(hashrate, span, now, tolerance)
		if err := e.deps.Store.Save(r.hashKey, st); err != nil {
			return err
		}
		if err := e.deps.Store.AddSample(r.hashKey, now, hashrate, power); err != nil {
			return err
		}
		if outcome != stats.Rejected {
			if err := e.noteVersion(r.hashKey, r.def.Version); err != nil {
				return err
			}
		}
		if outcome != stats.Accepted {
			e.deps.Logf("%s: hashrate %s of %s was %s (stored average %s)",
				g.name, formatHashrate(hashrate), r.hashKey, outcome, formatHashrate(st.Average(stats.Week)))
		}
		if power > 0 {
			pst, _, err := e.deps.Store.Load(r.powerKey)
			if err != nil {
				return err
			}
			pst.Update(power, span, now, 0)
			if err := e.deps.Store.Save(r.powerKey, pst); err != nil {
				return err
			}
		}
	}
	return nil
}

// report logs what a group is doing after a round.
func (e *Engine) report(g *group, decision selector.Decision, params selector.Params) {
	r := g.run
	if r == nil {
		e.deps.Logf("%s: idle - %s", g.name, decision.Reason)
		return
	}
	var live miner.AlgoSample
	var power float64
	if last, ok := r.window.Last(); ok {
		live = last.For(r.algo.NameForMiner())
		power = last.PowerWatts
	}
	line := fmt.Sprintf("%s: %s %s  %.0f W  shares %d/%d/%d",
		g.name, r.label(), formatHashrate(live.Hashrate), power,
		live.Accepted, live.Rejected, live.Invalid)
	if r.benchmark {
		e.deps.Logf("%s  (benchmark, %v of %v measured)", line,
			r.measured.Round(time.Second), e.opts.BenchmarkTime)
		return
	}
	profit := selector.Profit(r.Candidate, params)
	line += fmt.Sprintf("  %.8f BTC/day", profit)
	if e.rate > 0 {
		line += fmt.Sprintf(" = %.2f %s/day", profit*e.rate, e.opts.PowerCurrency)
	}
	e.deps.Logf("%s", line)
}

func formatHashrate(h float64) string {
	units := []string{"H/s", "kH/s", "MH/s", "GH/s", "TH/s", "PH/s"}
	i := 0
	for h >= 1000 && i < len(units)-1 {
		h /= 1000
		i++
	}
	return fmt.Sprintf("%.2f %s", h, units[i])
}
