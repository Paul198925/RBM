package console

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"hashsteer/internal/api"
	"hashsteer/internal/engine"
)

// redrawEvery is how often the dashboard is drawn anew.
const redrawEvery = 2 * time.Second

// Source is where the dashboard gets what it shows. It is what the web
// interface is served from, too.
type Source interface {
	Waiting() string
	Status() engine.Status
	System() api.System
	Devices() []api.DeviceStatus
	Balances() api.BalanceInfo
	UpdateInfo() api.UpdateInfo
	Benchmarks() []engine.BenchmarkStatus
	Log() []string
	SetPaused(paused bool)
}

// Dashboard draws into a terminal and reads its keys.
type Dashboard struct {
	Source  Source
	Version string
	// Worker and URL are asked for at every redraw: both can change while
	// the program runs.
	Worker func() string
	URL    func() string

	out *os.File
	in  *os.File
	// mu keeps the redraw after a key apart from the one of the clock
	mu sync.Mutex
}

// Usable reports whether out is a terminal a dashboard can be drawn in.
// Output that goes to a file, a pipe or a service's journal is not: there
// the plain log is the right thing.
func Usable(out *os.File) bool {
	return term.IsTerminal(int(out.Fd())) && enableColours(out)
}

// New makes a dashboard that draws to out and reads keys from in.
func New(source Source, version string, out, in *os.File) *Dashboard {
	return &Dashboard{Source: source, Version: version, out: out, in: in}
}

// Run draws the dashboard until ctx is done, and leaves the terminal as it
// found it. quit is called when the user asks to end the program.
func (d *Dashboard) Run(ctx context.Context, quit func()) {
	keys := term.IsTerminal(int(d.in.Fd()))
	if keys {
		// keys are to arrive one by one, unseen
		if state, err := term.MakeRaw(int(d.in.Fd())); err == nil {
			defer term.Restore(int(d.in.Fd()), state)
			go d.readKeys(ctx, quit)
		} else {
			keys = false
		}
	}

	// the dashboard gets a screen of its own; what was in the window
	// before comes back when the program ends
	io.WriteString(d.out, "\x1b[?1049h\x1b[?25l")
	defer io.WriteString(d.out, "\x1b[?25h\x1b[?1049l")

	ticker := time.NewTicker(redrawEvery)
	defer ticker.Stop()
	for {
		d.draw(keys)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Dashboard) snapshot(keys bool) Snapshot {
	s := Snapshot{
		Version:    d.Version,
		Now:        time.Now(),
		Waiting:    d.Source.Waiting(),
		Status:     d.Source.Status(),
		System:     d.Source.System(),
		Devices:    d.Source.Devices(),
		Balances:   d.Source.Balances(),
		Update:     d.Source.UpdateInfo(),
		Benchmarks: d.Source.Benchmarks(),
		Log:        d.Source.Log(),
		Keys:       keys,
		ASCII:      !unicodeTerminal(),
	}
	if d.Worker != nil {
		s.Worker = d.Worker()
	}
	if d.URL != nil {
		s.URL = d.URL()
	}
	return s
}

// draw paints the dashboard over the last one: every line is written anew
// and cleared to its end, so nothing flickers and nothing is left over.
func (d *Dashboard) draw(keys bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	width, height, err := term.GetSize(int(d.out.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		width, height = 120, 40
	}
	lines := Render(d.snapshot(keys), width, height, true)

	var b strings.Builder
	b.WriteString("\x1b[H")
	for _, line := range lines {
		b.WriteString(line + "\x1b[K\r\n")
	}
	b.WriteString("\x1b[J")
	io.WriteString(d.out, b.String())
}

// readKeys acts on what the user types.
func (d *Dashboard) readKeys(ctx context.Context, quit func()) {
	buf := make([]byte, 32)
	for ctx.Err() == nil {
		n, err := d.in.Read(buf)
		if err != nil {
			return
		}
		// arrow and function keys arrive as a code that starts with
		// Escape and may hold letters; they mean nothing here
		if n > 1 && buf[0] == 0x1b {
			continue
		}
		for _, key := range buf[:n] {
			switch key {
			case 'q', 'Q', 3: // 3 is Ctrl+C, which is no signal while keys are read one by one
				quit()
				return
			case 'p', 'P':
				d.Source.SetPaused(!d.Source.System().Paused)
				d.draw(true)
			}
		}
	}
}
