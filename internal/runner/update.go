package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"rbm/internal/api"
	"rbm/internal/update"
)

const (
	// how often GitHub is asked for a newer release
	updateInterval = 6 * time.Hour
	// the first look, once the program has settled
	updateDelay = time.Minute
)

// RestartEnv is set by the start scripts. It says that a program which
// ends with RestartCode is started again, with a newer build swapped in.
const (
	RestartEnv  = "RBM_RESTART"
	RestartCode = 75
)

// ErrRestart is what Run returns when the program is to be started again
// to finish an update.
var ErrRestart = errors.New("restarting to finish the update")

// updateState is what is known about newer releases.
type updateState struct {
	latest update.Release
	found  bool
	// checked is when GitHub was last asked, zero if never
	checked time.Time
	err     string
	// downloaded is the version that waits next to the program, to be
	// swapped in at the next start
	downloaded string
	busy       bool
}

// restartable reports whether ending the program gets it started again.
func restartable() bool { return os.Getenv(RestartEnv) != "" }

// newProgramPath is where a newer build waits for the next start: next to
// the running program, where the start scripts look for it.
func newProgramPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	name := "rbm.new"
	if runtime.GOOS == "windows" {
		name = "rbm.new.exe"
	}
	return filepath.Join(filepath.Dir(exe), name), nil
}

// UpdateInfo says whether a newer release exists and what was done with it.
func (a *App) UpdateInfo() api.UpdateInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	u := a.update
	info := api.UpdateInfo{
		Current:     Version,
		Restartable: restartable(),
		Downloaded:  u.downloaded,
		Busy:        u.busy,
		Error:       u.err,
	}
	if !u.checked.IsZero() {
		checked := u.checked
		info.Checked = &checked
	}
	if u.found {
		info.Latest, info.Page = u.latest.Version, u.latest.Page
		info.Available = update.Newer(Version, u.latest.Version)
	}
	return info
}

// CheckUpdate asks GitHub for the newest release now.
func (a *App) CheckUpdate(ctx context.Context) error {
	if a.checker == nil {
		return errors.New("this build does not look for updates")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	release, found, err := a.checker.Latest(ctx)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.update.checked = time.Now()
	if err != nil {
		a.update.err = err.Error()
		return err
	}
	wasNew := !a.update.found || a.update.latest.Version != release.Version
	a.update.latest, a.update.found, a.update.err = release, found, ""
	if found && wasNew && update.Newer(Version, release.Version) {
		// logf takes the lock itself
		go a.logf("rbm %s is available (this is %s): %s", release.Version, Version, release.Page)
	}
	return nil
}

// InstallUpdate fetches the newest release and puts it next to the program.
// Where the program gets started again by its start script, it then ends
// to let the new one take over; elsewhere the new one waits for the next
// start.
func (a *App) InstallUpdate(ctx context.Context) error {
	if a.checker == nil {
		return errors.New("this build does not look for updates")
	}
	a.mu.Lock()
	u := a.update
	switch {
	case u.busy:
		a.mu.Unlock()
		return errors.New("an update is being fetched already")
	case !u.found || !update.Newer(Version, u.latest.Version):
		a.mu.Unlock()
		return errors.New("there is no newer release")
	}
	a.update.busy = true
	a.mu.Unlock()

	err := a.fetchUpdate(ctx, u.latest)

	a.mu.Lock()
	a.update.busy = false
	if err != nil {
		a.update.err = err.Error()
	} else {
		a.update.downloaded, a.update.err = u.latest.Version, ""
	}
	a.mu.Unlock()
	if err != nil {
		a.logf("%v", err)
		return err
	}

	if !restartable() {
		a.logf("rbm %s is fetched and checked. It takes over at the next start.", u.latest.Version)
		return nil
	}
	a.logf("rbm %s is fetched and checked. Restarting to let it take over.", u.latest.Version)
	select {
	case a.restart <- struct{}{}:
	default:
	}
	return nil
}

func (a *App) fetchUpdate(ctx context.Context, release update.Release) error {
	target, err := newProgramPath()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return a.checker.Download(ctx, release, target)
}

// watchUpdates looks for a newer release now and then, and installs it if
// that is asked for and the program can be started again.
func (a *App) watchUpdates(ctx context.Context) {
	timer := time.NewTimer(updateDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		timer.Reset(updateInterval)

		cfg := a.Config()
		if !cfg.General.Updates.Check || a.checker == nil {
			continue
		}
		// a failed look is no news: the next one comes by itself
		if a.CheckUpdate(ctx) != nil {
			continue
		}
		info := a.UpdateInfo()
		if cfg.General.Updates.Auto && info.Available && info.Restartable && info.Downloaded != info.Latest {
			a.InstallUpdate(ctx)
		}
	}
}
