// Package runner wires the parts together into something that mines.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"rbm/internal/algo"
	"rbm/internal/config"
	"rbm/internal/device"
	"rbm/internal/miner"
	"rbm/internal/pool"
	"rbm/internal/proc"
	"rbm/internal/stats"
)

const (
	pollInterval  = 10 * time.Second
	storeInterval = time.Minute
	// a miner needs this long to connect and build its DAG; what it
	// reports before says nothing about its speed
	warmup = 90 * time.Second
	// tolerance for hashrate samples against the stored average
	hashrateTolerance = 0.1
	stopGrace         = 10 * time.Second
	sampleRetention   = 7 * 24 * time.Hour
)

// ManualOptions selects what Mine runs. There is no profit switching here:
// one miner, one algorithm, until interrupted.
type ManualOptions struct {
	Miner     string
	Algorithm string
	TLS       bool
}

// Mine runs one miner on one algorithm at zpool until ctx is cancelled or
// the miner exits, and records what it measures.
func Mine(ctx context.Context, cfg *config.Config, opts ManualOptions, out io.Writer) error {
	if err := cfg.ReadyToMine(); err != nil {
		return err
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(out, time.Now().Format("15:04:05")+" "+format+"\n", args...)
	}

	defs, err := miner.Builtin()
	if err != nil {
		return err
	}
	def, ok := defs[opts.Miner]
	if !ok {
		return fmt.Errorf("unknown miner %q, known: %s", opts.Miner, strings.Join(miner.Names(defs), ", "))
	}
	algoName := algo.Normalize(opts.Algorithm)
	minerAlgo, ok := def.Algorithm(algoName)
	if !ok {
		return fmt.Errorf("%s does not mine %q", def.Name, opts.Algorithm)
	}
	api, err := miner.NewAPI(def.API)
	if err != nil {
		return err
	}

	devices, err := selectDevices(ctx, def, minerAlgo)
	if err != nil {
		return err
	}
	var deviceIDs []string
	var deviceIndexes []int
	for _, d := range devices {
		deviceIDs = append(deviceIDs, d.ID)
		deviceIndexes = append(deviceIndexes, d.Index)
		logf("using %s: %s", d.ID, d.Model)
	}

	zpool := &pool.Zpool{
		Wallet:   cfg.Pools.Zpool.Wallet,
		Currency: cfg.Pools.Zpool.Currency,
		Worker:   cfg.General.Worker,
		Region:   cfg.Pools.Zpool.Region,
		// by hand, the pool's own port for the algorithm is what is meant
		AlgoSwitching: true,
	}
	if err := checkPayoutCurrency(ctx, zpool, logf); err != nil {
		return err
	}
	offer, err := fetchOffer(ctx, zpool, algoName)
	if err != nil {
		return err
	}
	port := offer.Port
	if opts.TLS {
		if offer.TLSPort == 0 {
			return fmt.Errorf("zpool has no TLS port for %s", algoName)
		}
		port = offer.TLSPort
	}

	store, err := stats.Open(filepath.Join(cfg.General.DataDir, "stats.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	if _, err := store.PruneSamples(time.Now().Add(-sampleRetention)); err != nil {
		return err
	}
	priceKey := stats.PriceKey(zpool.Name(), algoName)
	hashKey := stats.HashrateKey(def.Name, deviceIDs, algoName)
	priceStat, _, err := store.Load(priceKey)
	if err != nil {
		return err
	}
	hashStat, known, err := store.Load(hashKey)
	if err != nil {
		return err
	}
	if known {
		logf("stored hashrate for %s: %s", hashKey, FormatHashrate(hashStat.Average(stats.Week)))
	}

	binDir := filepath.Join(cfg.General.DataDir, "bin")
	if _, ok := def.Installed(binDir); !ok {
		logf("downloading %s %s ...", def.Name, def.Version)
	}
	exe, err := def.Install(ctx, binDir, nil)
	if err != nil {
		return err
	}

	apiPort, err := freePort()
	if err != nil {
		return err
	}
	args, err := def.BuildArgs(miner.Job{
		Algorithm:     minerAlgo,
		DeviceIndexes: deviceIndexes,
		APIPort:       apiPort,
		Host:          offer.Host,
		Port:          port,
		TLS:           opts.TLS,
		User:          offer.User,
		Pass:          offer.Pass,
	})
	if err != nil {
		return err
	}

	logPath := filepath.Join(cfg.General.DataDir, "logs",
		fmt.Sprintf("%s-%s-%s.log", def.Name, algoName, time.Now().Format("20060102-150405")))
	process, err := proc.Start(proc.Spec{Path: exe, Args: args, LogPath: logPath, LowPriority: true})
	if err != nil {
		return err
	}
	logf("started %s (pid %d) on %s at %s:%d, log: %s", def.Name, process.PID(), algoName, offer.Host, port, logPath)
	defer func() {
		if process.Running() {
			logf("stopping %s ...", def.Name)
			// ctx is cancelled by now, the stop gets its own
			if err := process.Stop(context.Background(), stopGrace); err != nil {
				logf("stop: %v", err)
			}
		}
	}()

	var window miner.Window
	measureFrom := process.Started().Add(warmup)
	lastStore := process.Started()
	lastPrice := time.Now()
	priceStat.Update(offer.Price, pool.ZpoolMinInterval, lastPrice, 0)
	if err := store.Save(priceKey, priceStat); err != nil {
		return err
	}

	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-process.Done():
			return fmt.Errorf("%s exited on its own (%v), see %s", def.Name, exitText(process.ExitErr()), logPath)
		case now := <-poll.C:
			sample, err := api.Poll(ctx, apiPort)
			if err != nil {
				if ctx.Err() == nil {
					logf("%s api: %v", def.Name, err)
				}
				continue
			}
			if sample.PowerWatts == 0 {
				sample.PowerWatts = gpuPower(ctx, deviceIndexes)
			}
			window.Add(sample)
			current := sample.For(minerAlgo.NameForMiner())
			logf("%s  accepted %d  rejected %d  invalid %d  %.0f W  ~%.8f BTC/day",
				FormatHashrate(current.Hashrate), current.Accepted, current.Rejected, current.Invalid,
				sample.PowerWatts, current.Hashrate*offer.Price)

			if now.Sub(lastPrice) >= pool.ZpoolMinInterval {
				if fresh, err := fetchOffer(ctx, zpool, algoName); err != nil {
					logf("zpool: %v", err)
				} else {
					priceStat.Update(fresh.Price, now.Sub(lastPrice), now, 0)
					if err := store.Save(priceKey, priceStat); err != nil {
						return err
					}
					offer.Price = fresh.Price
				}
				lastPrice = now
			}

			if now.Sub(lastStore) < storeInterval || now.Before(measureFrom.Add(storeInterval)) {
				continue
			}
			since := lastStore
			if since.Before(measureFrom) {
				since = measureFrom
			}
			hashrate, power, n := window.Average(minerAlgo.NameForMiner(), since)
			lastStore = now
			window.Prune(now.Add(-2 * storeInterval))
			if n == 0 || hashrate <= 0 {
				logf("no hashrate in the last interval, nothing stored")
				continue
			}
			outcome := hashStat.Update(hashrate, now.Sub(since), now, hashrateTolerance)
			if err := store.Save(hashKey, hashStat); err != nil {
				return err
			}
			if err := store.AddSample(hashKey, now, hashrate, power); err != nil {
				return err
			}
			logf("interval average %s over %d samples: %s, stored average %s",
				FormatHashrate(hashrate), n, outcome, FormatHashrate(hashStat.Average(stats.Week)))
		}
	}
}

// selectDevices returns the devices the miner can use for the algorithm.
func selectDevices(ctx context.Context, def *miner.Definition, a miner.Algorithm) ([]device.Device, error) {
	all, err := device.Detect(ctx)
	if err != nil {
		return nil, err
	}
	var usable []device.Device
	for _, d := range all {
		switch {
		case def.Vendor == miner.VendorCPU && d.Kind == device.CPU:
			usable = append(usable, d)
		case d.Kind == device.GPU && d.Vendor == def.Vendor && d.MemoryMiB >= a.MinMemoryMiB:
			usable = append(usable, d)
		}
	}
	if len(usable) == 0 {
		return nil, fmt.Errorf("no %s device with at least %d MiB for %s", def.Vendor, a.MinMemoryMiB, a.Name)
	}
	return usable, nil
}

// checkPayoutCurrency refuses a currency zpool does not pay out in, before
// any share is mined for an address the pool cannot pay. If the pool cannot
// be asked, mining goes on: the list is a safeguard, not a requirement.
func checkPayoutCurrency(ctx context.Context, zpool *pool.Zpool, logf func(string, ...any)) error {
	currencies, err := zpool.PayoutCurrencies(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logf("could not check the payout currency: %v", err)
		return nil
	}
	currency, ok := pool.FindPayoutCurrency(currencies, zpool.Currency)
	if !ok {
		return fmt.Errorf("zpool does not pay out in %s, run \"rbm currencies\" for the list", zpool.Currency)
	}
	logf("payout in %s (%s)", currency.Symbol, currency.Name)
	if currency.MinimumPayout > 0 {
		logf("minimum payout: %g %s", currency.MinimumPayout, currency.Symbol)
	}
	if currency.Notice != "" {
		logf("zpool notice for %s: %s", currency.Symbol, currency.Notice)
	}
	return nil
}

func fetchOffer(ctx context.Context, source pool.Source, algorithm string) (pool.Offer, error) {
	offers, err := source.Fetch(ctx)
	if err != nil {
		return pool.Offer{}, err
	}
	for _, offer := range offers {
		if offer.Algorithm == algorithm {
			return offer, nil
		}
	}
	return pool.Offer{}, fmt.Errorf("%s does not offer %s", source.Name(), algorithm)
}

// gpuPower sums the measured power draw of the GPUs, 0 if unknown.
func gpuPower(ctx context.Context, indexes []int) float64 {
	power, err := device.GPUPower(ctx)
	if err != nil {
		return 0
	}
	var sum float64
	for _, index := range indexes {
		sum += power[index]
	}
	return sum
}

// freePort returns a local TCP port that is free right now.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func exitText(err error) string {
	if err == nil {
		return "exit code 0"
	}
	var exitErr interface{ ExitCode() int }
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("exit code %d", exitErr.ExitCode())
	}
	return err.Error()
}

// FormatHashrate prints a hashrate with a fitting unit.
func FormatHashrate(h float64) string {
	units := []string{"H/s", "kH/s", "MH/s", "GH/s", "TH/s", "PH/s"}
	i := 0
	for h >= 1000 && i < len(units)-1 {
		h /= 1000
		i++
	}
	return fmt.Sprintf("%.2f %s", h, units[i])
}
