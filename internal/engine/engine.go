// Package engine runs the mining rounds: it measures the running miners,
// asks the selector what each device group should mine, and starts and stops
// miner programs to match.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"rbm/internal/device"
	"rbm/internal/miner"
	"rbm/internal/pool"
	"rbm/internal/selector"
	"rbm/internal/stats"
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
	TLS              bool
	DataDir          string
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
	Logf  func(format string, args ...any)
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
}

// group is a set of identical devices that mine together with one miner.
type group struct {
	name    string
	vendor  string
	devices []device.Device

	run *run
	// failures and the time a candidate is taken out until, by candidate ID
	crashes  map[string][]time.Time
	disabled map[string]time.Time
}

// option is a candidate together with what is needed to start it.
type option struct {
	selector.Candidate
	def      *miner.Definition
	algo     miner.Algorithm
	offer    pool.Offer
	powerKey string
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
		groups:   groupDevices(deps.Devices),
		offers:   map[string][]pool.Offer{},
		offersAt: map[string]time.Time{},
	}
	if len(e.groups) == 0 {
		return nil, errors.New("no GPU found to mine with")
	}
	return e, nil
}

// groupDevices puts the GPUs of the same model into one group each.
func groupDevices(devices []device.Device) []*group {
	byName := map[string]*group{}
	for _, d := range devices {
		if d.Kind != device.GPU {
			continue
		}
		name := groupName(d.Model)
		g := byName[d.Vendor+"/"+name]
		if g == nil {
			g = &group{name: name, vendor: d.Vendor, crashes: map[string][]time.Time{}, disabled: map[string]time.Time{}}
			byName[d.Vendor+"/"+name] = g
		}
		g.devices = append(g.devices, d)
	}
	groups := make([]*group, 0, len(byName))
	for _, g := range byName {
		sort.Slice(g.devices, func(i, j int) bool { return g.devices[i].Index < g.devices[j].Index })
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].devices[0].Index < groups[j].devices[0].Index })
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
	defer e.stopAll()
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
		for time.Now().Before(next) {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(pollInterval):
			}
			// a miner that died does not wait for the end of the round
			if e.poll(ctx, time.Now()) {
				break
			}
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// round stores what was measured, checks on the running miners and makes
// every group mine what the selector picks.
func (e *Engine) round(ctx context.Context, now time.Time) error {
	if err := e.store(now); err != nil {
		return err
	}
	if err := e.refreshOffers(ctx, now); err != nil {
		return err
	}
	e.refreshRate(ctx, now)

	params := selector.Params{
		HysteresisPercent: e.opts.HysteresisPercent,
		MineAtLoss:        e.opts.MineAtLoss,
	}
	if e.opts.PowerPricePerKWh > 0 && e.rate > 0 {
		params.PowerCostBTCPerKWh = e.opts.PowerPricePerKWh / e.rate
	}

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
		if g.run != nil {
			running = g.run.ID
			locked = g.run.benchmark || now.Sub(g.run.started) < e.opts.MinRunTime
			// a finished benchmark frees the group for the next one
			if o, ok := byID[running]; ok && g.run.benchmark && o.Benchmarked {
				g.run.benchmark = false
				locked = false
				e.deps.Logf("%s: benchmark of %s done: %s", g.name, running, formatHashrate(o.Hashrate))
			}
		}

		decision := selector.Decide(candidates, running, locked, params)
		if len(candidates) == 0 && len(g.disabled) > 0 {
			decision.Reason = fmt.Sprintf("%d candidates are taken out after failures", len(g.disabled))
		}
		if decision.ID != running {
			if g.run != nil {
				e.deps.Logf("%s: stopping %s", g.name, running)
				e.stop(g)
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
		}
		e.report(g, decision, params)
	}
	return nil
}

// checkRun stops a miner that exited or went silent and records the failure.
func (e *Engine) checkRun(g *group, now time.Time) {
	r := g.run
	if r == nil {
		return
	}
	select {
	case <-r.miner.Done():
		e.deps.Logf("%s: %s exited on its own (%s)", g.name, r.ID, r.miner.ExitText())
		e.stop(g)
		e.failed(g, r.ID, now, false)
		return
	default:
	}

	silentSince := r.lastHash
	if silentSince.Before(r.measureFrom) {
		silentSince = r.measureFrom
	}
	if now.Sub(silentSince) < watchdogTimeout {
		return
	}
	e.deps.Logf("%s: %s reported no hashrate for %v", g.name, r.ID, now.Sub(silentSince).Round(time.Second))
	e.stop(g)
	e.failed(g, r.ID, now, r.benchmark)
}

// failed counts a failure of candidate id. A benchmark that produced nothing
// takes the candidate out at once, a miner that keeps failing after a few
// tries.
func (e *Engine) failed(g *group, id string, now time.Time, benchmark bool) {
	if benchmark {
		g.disabled[id] = now.Add(benchmarkPenalty)
		e.deps.Logf("%s: benchmark of %s failed, not tried again for %v", g.name, id, benchmarkPenalty)
		return
	}
	recent := g.crashes[id][:0]
	for _, t := range g.crashes[id] {
		if now.Sub(t) < crashWindow {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	if len(recent) < maxCrashes {
		g.crashes[id] = recent
		return
	}
	delete(g.crashes, id)
	g.disabled[id] = now.Add(crashPenalty)
	e.deps.Logf("%s: %s failed %d times within %v, not tried again for %v", g.name, id, maxCrashes, crashWindow, crashPenalty)
}

// options lists what group g could mine right now.
func (e *Engine) options(g *group, now time.Time) ([]option, error) {
	for id, until := range g.disabled {
		if !now.Before(until) {
			delete(g.disabled, id)
		}
	}

	var options []option
	for _, name := range miner.Names(e.deps.Definitions) {
		def := e.deps.Definitions[name]
		if def.Vendor != g.vendor {
			continue
		}
		for _, a := range def.Algorithms {
			if !fits(g, a) {
				continue
			}
			offer, ok := e.bestOffer(a.Name, now)
			if !ok {
				continue
			}
			id := stats.HashrateKey(def.Name, g.deviceIDs(), a.Name)
			if _, out := g.disabled[id]; out {
				continue
			}
			o := option{
				Candidate: selector.Candidate{ID: id, Price: offer.Price, FeePercent: def.FeePercent(a)},
				def:       def,
				algo:      a,
				offer:     offer,
				powerKey:  "power/" + strings.TrimPrefix(id, "hashrate/"),
			}

			// the hourly average rather than the latest figure: a price
			// spike of a few minutes is no reason to restart miners
			price, ok, err := e.deps.Store.Load(stats.PriceKey(offer.Pool, a.Name))
			if err != nil {
				return nil, err
			}
			if ok && !price.Empty() {
				o.Price = price.Average(stats.Hour)
			}
			hashrate, ok, err := e.deps.Store.Load(id)
			if err != nil {
				return nil, err
			}
			if ok && hashrate.Duration >= e.opts.BenchmarkTime {
				o.Hashrate = hashrate.Average(stats.Week)
				o.Benchmarked = true
			}
			power, ok, err := e.deps.Store.Load(o.powerKey)
			if err != nil {
				return nil, err
			}
			if ok {
				o.PowerWatts = power.Average(stats.Week)
			}
			options = append(options, o)
		}
	}
	return options, nil
}

// fits reports whether every device of g has the memory algorithm a needs.
func fits(g *group, a miner.Algorithm) bool {
	for _, d := range g.devices {
		if d.MemoryMiB < a.MinMemoryMiB {
			return false
		}
	}
	return true
}

// bestOffer returns the best paying fresh offer for an algorithm.
func (e *Engine) bestOffer(algorithm string, now time.Time) (pool.Offer, bool) {
	var best pool.Offer
	found := false
	for _, source := range e.deps.Sources {
		name := source.Name()
		if now.Sub(e.offersAt[name]) > offerMaxAge {
			continue
		}
		for _, offer := range e.offers[name] {
			if offer.Algorithm == algorithm && (!found || offer.Price > best.Price) {
				best, found = offer, true
			}
		}
	}
	return best, found
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
			key := stats.PriceKey(offer.Pool, offer.Algorithm)
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
	return nil
}

// refreshRate updates the bitcoin price. Without one, electricity cannot be
// priced and is left out until the next try.
func (e *Engine) refreshRate(ctx context.Context, now time.Time) {
	if e.opts.PowerPricePerKWh <= 0 {
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
			e.deps.Logf("exchange rate: %v, electricity is not counted for now", err)
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
	e.deps.Logf("%s: %s %s on %s at %s (%s)", g.name, what, o.def.Name, o.algo.Name, o.offer.Pool, decision.Reason)

	m, err := e.deps.Launch(ctx, LaunchSpec{
		Definition: o.def,
		Algorithm:  o.algo,
		Devices:    g.devices,
		Offer:      o.offer,
		TLS:        e.opts.TLS && o.offer.TLSPort != 0,
		DataDir:    e.opts.DataDir,
		Tag:        g.name,
	})
	if err != nil {
		if ctx.Err() == nil {
			e.deps.Logf("%s: could not start %s: %v", g.name, o.ID, err)
			e.failed(g, o.ID, now, false)
		}
		return
	}
	e.deps.Logf("%s: %s is running (%s)", g.name, o.def.Name, m.Describe())
	g.run = &run{
		option:      o,
		miner:       m,
		benchmark:   decision.Benchmark,
		started:     now,
		measureFrom: now.Add(warmup),
		lastStore:   now,
	}
}

// stop ends the miner of g. The stop gets its own context: it must also
// work while the program is shutting down.
func (e *Engine) stop(g *group) {
	if g.run == nil {
		return
	}
	if err := g.run.miner.Stop(context.Background()); err != nil {
		e.deps.Logf("%s: stop: %v", g.name, err)
	}
	g.run = nil
}

func (e *Engine) stopAll() {
	for _, g := range e.groups {
		if g.run != nil {
			e.deps.Logf("%s: stopping %s", g.name, g.run.ID)
			e.stop(g)
		}
	}
}

// poll reads the statistics of all running miners. It reports whether one
// of them has exited.
func (e *Engine) poll(ctx context.Context, now time.Time) (exited bool) {
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
		if sample.PowerWatts == 0 && e.deps.Power != nil {
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
		if sample.Algorithms[r.algo.NameForMiner()].Hashrate > 0 {
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
		if n == 0 || hashrate <= 0 {
			continue
		}
		span := now.Sub(since)

		st, _, err := e.deps.Store.Load(r.ID)
		if err != nil {
			return err
		}
		outcome := st.Update(hashrate, span, now, hashrateTolerance)
		if err := e.deps.Store.Save(r.ID, st); err != nil {
			return err
		}
		if err := e.deps.Store.AddSample(r.ID, now, hashrate, power); err != nil {
			return err
		}
		if outcome != stats.Accepted {
			e.deps.Logf("%s: hashrate %s of %s was %s (stored average %s)",
				g.name, formatHashrate(hashrate), r.ID, outcome, formatHashrate(st.Average(stats.Week)))
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
		live = last.Algorithms[r.algo.NameForMiner()]
		power = last.PowerWatts
	}
	line := fmt.Sprintf("%s: %s/%s %s  %.0f W  shares %d/%d/%d",
		g.name, r.def.Name, r.algo.Name, formatHashrate(live.Hashrate), power,
		live.Accepted, live.Rejected, live.Invalid)
	if r.benchmark {
		e.deps.Logf("%s  (benchmark)", line)
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
