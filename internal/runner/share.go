package runner

import (
	"context"
	"errors"
	"strings"
	"time"

	"hashsteer/internal/api"
	"hashsteer/internal/config"
	"hashsteer/internal/engine"
	"hashsteer/internal/estimate"
	"hashsteer/internal/share"
)

const (
	// how often the shared hashrates are fetched, and this rig's sent
	shareInterval = 12 * time.Hour
	// the first exchange, once the program has settled
	shareDelay = 2 * time.Minute
)

// estimates returns the hashrates of other machines to start from: the
// ones that come with the program, and over them what the share server
// handed out last. nil if they are not wanted.
func (a *App) estimates(wanted bool, dataDir string) *estimate.Table {
	if !wanted {
		return nil
	}
	table, err := estimate.Builtin()
	if err != nil {
		a.logf("%v", err)
		return nil
	}
	shared, _ := share.LoadCache(dataDir)
	return table.With(shared)
}

// watchSharing fetches the shared hashrates now and then and, if the user
// agreed to it, sends what this rig has measured.
func (a *App) watchSharing(ctx context.Context) {
	timer := time.NewTimer(shareDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		timer.Reset(shareInterval)
		a.exchangeHashrates(ctx)
	}
}

// exchangeHashrates does one round with the share server. What goes wrong
// is logged and returned.
func (a *App) exchangeHashrates(ctx context.Context) error {
	cfg := a.Config()
	sharing := cfg.General.Sharing
	if sharing.URL == "" {
		return nil
	}
	client := &share.Client{URL: sharing.URL}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	var failed error
	if sharing.Download {
		entries, err := client.Estimates(ctx)
		switch {
		case err != nil:
			a.logf("%v", err)
			failed = err
		case len(entries) > 0:
			// they count from the next time the engine starts
			if err := share.SaveCache(cfg.General.DataDir, entries, time.Now()); err != nil {
				a.logf("shared hashrates: %v", err)
				failed = err
			}
		}
	}

	if !sharing.Upload {
		return failed
	}
	entries := a.ownHashrates(&cfg)
	if len(entries) == 0 {
		return failed
	}
	id, err := share.ClientID(cfg.General.DataDir)
	if err != nil {
		a.logf("sharing hashrates: %v", err)
		return err
	}
	if err := client.Report(ctx, id, entries); err != nil {
		a.logf("%v", err)
		return err
	}
	a.logf("shared %d measured hashrates with %s", len(entries), sharing.URL)
	return failed
}

// ownHashrates returns what this rig has measured itself, per device. It
// is empty while no engine has run.
func (a *App) ownHashrates(cfg *config.Config) []estimate.Entry {
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()
	if store == nil {
		return nil
	}
	entries, err := engine.ExportEstimates(store, a.definitions, a.devices, time.Duration(cfg.General.Switching.BenchmarkTime))
	if err != nil {
		return nil
	}
	return entries
}

// RefreshShared exchanges hashrates with the share server now.
func (a *App) RefreshShared(ctx context.Context) error {
	if a.Config().General.Sharing.URL == "" {
		return errors.New("no share server is set")
	}
	return a.exchangeHashrates(ctx)
}

// Shared lists the hashrates measured on other machines: the ones that come
// with the program, and over them what the share server handed out last.
func (a *App) Shared() (api.SharedInfo, error) {
	cfg := a.Config()
	sharing := cfg.General.Sharing
	info := api.SharedInfo{URL: sharing.URL, Download: sharing.Download, Upload: sharing.Upload, Used: cfg.General.Switching.UseEstimates}
	builtin, err := estimate.Builtin()
	if err != nil {
		return info, err
	}
	fetched, at := share.LoadCache(cfg.General.DataDir)
	if !at.IsZero() {
		info.Fetched = &at
	}

	models := map[string]bool{}
	for _, d := range a.devices {
		models[strings.ToLower(d.Model)] = true
	}
	own := map[string]float64{}
	for _, e := range a.ownHashrates(&cfg) {
		own[strings.ToLower(e.Device)+"/"+e.Miner+"/"+e.Algorithm] = e.Hashrate
	}
	for _, e := range builtin.With(fetched).Entries() {
		info.Entries = append(info.Entries, api.SharedHashrate{
			Device: e.Device, Miner: e.Miner, Version: e.Version, Algorithm: e.Algorithm,
			Hashrate: e.Hashrate, PowerWatts: e.PowerWatts, Reports: e.Reports,
			Mine:        models[strings.ToLower(e.Device)],
			OwnHashrate: own[strings.ToLower(e.Device)+"/"+e.Miner+"/"+e.Algorithm],
		})
	}
	return info, nil
}
