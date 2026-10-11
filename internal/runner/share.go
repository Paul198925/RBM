package runner

import (
	"context"
	"time"

	"rbm/internal/engine"
	"rbm/internal/estimate"
	"rbm/internal/share"
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

// exchangeHashrates does one round with the share server.
func (a *App) exchangeHashrates(ctx context.Context) {
	cfg := a.Config()
	sharing := cfg.General.Sharing
	if sharing.URL == "" {
		return
	}
	client := &share.Client{URL: sharing.URL}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	if sharing.Download {
		entries, err := client.Estimates(ctx)
		switch {
		case err != nil:
			a.logf("%v", err)
		case len(entries) > 0:
			// they count from the next time the engine starts
			if err := share.SaveCache(cfg.General.DataDir, entries, time.Now()); err != nil {
				a.logf("shared hashrates: %v", err)
			}
		}
	}

	if !sharing.Upload {
		return
	}
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()
	if store == nil {
		return
	}
	entries, err := engine.ExportEstimates(store, a.definitions, a.devices, time.Duration(cfg.General.Switching.BenchmarkTime))
	if err != nil || len(entries) == 0 {
		return
	}
	id, err := share.ClientID(cfg.General.DataDir)
	if err != nil {
		a.logf("sharing hashrates: %v", err)
		return
	}
	if err := client.Report(ctx, id, entries); err != nil {
		a.logf("%v", err)
		return
	}
	a.logf("shared %d measured hashrates with %s", len(entries), sharing.URL)
}
