package runner

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestPruneLogs(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	write := func(name string, size int, age time.Duration) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	left := func() []string {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}

	write("ancient.log", 10, 20*24*time.Hour)
	write("old-big.log", 600, 3*24*time.Hour)
	write("old.log", 300, 2*24*time.Hour)
	write("recent.log", 300, time.Hour)
	write("running.log", 900, time.Minute)
	write("notes.txt", 5000, 30*24*time.Hour)

	// too old goes, and the oldest go until the rest fits; what may still
	// be written is never touched, and neither is what is no log
	removed, freed := pruneLogs(dir, now, 14*24*time.Hour, 1500)
	if removed != 2 || freed != 610 {
		t.Errorf("removed %d logs, %d bytes", removed, freed)
	}
	if got := left(); !slices.Equal(got, []string{"notes.txt", "old.log", "recent.log", "running.log"}) {
		t.Errorf("left: %v", got)
	}

	// nothing to do the second time
	if removed, _ := pruneLogs(dir, now, 14*24*time.Hour, 1500); removed != 0 {
		t.Errorf("removed %d logs the second time", removed)
	}
	// a directory that is not there is no trouble
	if removed, _ := pruneLogs(filepath.Join(dir, "missing"), now, time.Hour, 1); removed != 0 {
		t.Errorf("removed %d logs from nowhere", removed)
	}
}
