// Package estimate holds hashrates that others have measured, to start from
// before this machine has measured its own. They are estimates and nothing
// more: the same card does different speeds with another driver, clock or
// power limit. What is mined is measured anyway, and the measurement takes
// the estimate's place.
package estimate

import (
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed estimates.yaml
var builtin []byte

// Entry is what one device of a model does with a miner program on an
// algorithm.
type Entry struct {
	// Device is the model as the engine names it: "GeForce RTX 3060", or
	// the name of a CPU.
	Device string `yaml:"device"`
	Miner  string `yaml:"miner"`
	// Version is the version of the miner program it was measured with.
	Version   string `yaml:"version,omitempty"`
	Algorithm string `yaml:"algorithm"`
	// Hashrate is in hashes per second, for one device.
	Hashrate float64 `yaml:"hashrate"`
	// PowerWatts is the draw of one device while mining, 0 if unknown.
	PowerWatts float64 `yaml:"power_watts,omitempty"`
	// Reports is how many rigs the figure is the middle of, where it comes
	// from the share server; 0 for one that comes with the program.
	Reports int `yaml:"reports,omitempty"`
}

// Table finds estimates. A nil table has none.
type Table struct {
	entries map[string]Entry
}

type file struct {
	Estimates []Entry `yaml:"estimates"`
}

// Builtin returns the estimates that come with the program.
func Builtin() (*Table, error) {
	return Parse(builtin)
}

// Parse reads a file of estimates.
func Parse(data []byte) (*Table, error) {
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("estimates: %w", err)
	}
	t := &Table{entries: map[string]Entry{}}
	for i, e := range f.Estimates {
		if e.Device == "" || e.Miner == "" || e.Algorithm == "" || e.Hashrate <= 0 || e.PowerWatts < 0 {
			return nil, fmt.Errorf("estimates: entry %d is incomplete: %+v", i+1, e)
		}
		k := key(e.Device, e.Miner, e.Algorithm)
		if _, twice := t.entries[k]; twice {
			return nil, fmt.Errorf("estimates: %s with %s on %s is listed twice", e.Device, e.Miner, e.Algorithm)
		}
		t.entries[k] = e
	}
	return t, nil
}

func key(device, miner, algorithm string) string {
	return strings.ToLower(strings.TrimSpace(device)) + "\n" + miner + "\n" + algorithm
}

// Lookup returns the estimate for one device of a model.
func (t *Table) Lookup(device, miner, algorithm string) (Entry, bool) {
	if t == nil {
		return Entry{}, false
	}
	e, ok := t.entries[key(device, miner, algorithm)]
	return e, ok
}

// Len returns the number of estimates.
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return len(t.entries)
}

// Marshal writes entries as a file of estimates, sorted, with figures
// rounded to what they are worth.
func Marshal(entries []Entry) ([]byte, error) {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Device != b.Device {
			return a.Device < b.Device
		}
		if a.Miner != b.Miner {
			return a.Miner < b.Miner
		}
		return a.Algorithm < b.Algorithm
	})
	for i := range sorted {
		sorted[i].Hashrate = round3(sorted[i].Hashrate)
		sorted[i].PowerWatts = float64(int(sorted[i].PowerWatts + 0.5))
	}
	return yaml.Marshal(file{Estimates: sorted})
}

// round3 keeps three significant digits: more would pretend a precision
// that a figure from another machine does not have.
func round3(v float64) float64 {
	if v <= 0 {
		return 0
	}
	// by way of the text, which gives the nearest number to the rounded one
	rounded, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 3, 64), 64)
	return rounded
}

// With returns a table that also holds entries, which win over what t has
// for the same device, miner and algorithm. Entries that are not complete
// are left out. t itself is not changed.
func (t *Table) With(entries []Entry) *Table {
	merged := &Table{entries: map[string]Entry{}}
	if t != nil {
		for k, e := range t.entries {
			merged.entries[k] = e
		}
	}
	for _, e := range entries {
		if e.Device == "" || e.Miner == "" || e.Algorithm == "" || e.Hashrate <= 0 || e.PowerWatts < 0 {
			continue
		}
		merged.entries[key(e.Device, e.Miner, e.Algorithm)] = e
	}
	return merged
}

// Entries returns everything the table holds, sorted by device, miner and
// algorithm.
func (t *Table) Entries() []Entry {
	if t == nil {
		return nil
	}
	entries := make([]Entry, 0, len(t.entries))
	for _, e := range t.entries {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Device != b.Device {
			return a.Device < b.Device
		}
		if a.Miner != b.Miner {
			return a.Miner < b.Miner
		}
		return a.Algorithm < b.Algorithm
	})
	return entries
}
