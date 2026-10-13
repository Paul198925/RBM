package runner

import (
	"time"

	"hashsteer/internal/console"
)

// The size of the console window as the web interface shows it: wide
// enough for the widest table, and tall enough for a good piece of the log.
const (
	consoleColumns = 150
	consoleRows    = 46
)

// Console returns what the console window shows, drawn anew: the same
// picture `hashsteer run` paints into a terminal, as lines with the
// terminal's colour codes in them. The keys are left out; a page has
// buttons for what they do.
func (a *App) Console() []string {
	cfg := a.Config()
	return console.Render(console.Snapshot{
		Version:    Version,
		Worker:     cfg.General.Worker,
		URL:        a.URL(),
		Now:        time.Now(),
		Waiting:    a.Waiting(),
		Status:     a.Status(),
		System:     a.System(),
		Devices:    a.Devices(),
		Balances:   a.Balances(),
		Update:     a.UpdateInfo(),
		Benchmarks: a.Benchmarks(),
		Log:        a.Log(),
	}, consoleColumns, consoleRows, true)
}
