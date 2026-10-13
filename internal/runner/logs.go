package runner

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Every start of a miner writes a log of its own. They are tidied up now
// and then: logs older than logMaxAge go, and the oldest go first while all
// of them together are larger than logMaxTotal.
const (
	logMaxAge   = 14 * 24 * time.Hour
	logMaxTotal = 1 << 30
	logTidyGap  = 6 * time.Hour
	// a log this fresh may belong to a miner that still runs
	logInUse = 10 * time.Minute
)

// watchLogs tidies the miner logs at the start and every few hours.
func (a *App) watchLogs(ctx context.Context) {
	ticker := time.NewTicker(logTidyGap)
	defer ticker.Stop()
	for {
		if removed, freed := pruneLogs(a.logDir(), time.Now(), logMaxAge, logMaxTotal); removed > 0 {
			a.logf("removed %d old miner logs, %d MB", removed, freed>>20)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// pruneLogs removes the *.log files in dir that are older than maxAge, and
// then the oldest ones until the rest is no larger than maxTotal. It says
// how many it removed and how many bytes that freed.
func pruneLogs(dir string, now time.Time, maxAge time.Duration, maxTotal int64) (removed int, freed int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	type logFile struct {
		path     string
		size     int64
		modified time.Time
	}
	var logs []logFile
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || filepath.Ext(entry.Name()) != ".log" {
			continue
		}
		logs = append(logs, logFile{filepath.Join(dir, entry.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].modified.Before(logs[j].modified) })
	for _, l := range logs {
		age := now.Sub(l.modified)
		if age < logInUse || (age <= maxAge && total <= maxTotal) {
			continue
		}
		if os.Remove(l.path) != nil {
			continue
		}
		removed++
		freed += l.size
		total -= l.size
	}
	return removed, freed
}
