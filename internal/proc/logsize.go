package proc

import (
	"fmt"
	"io"
	"os"
	"time"
)

// A miner that runs into trouble can write the same line thousands of times
// a second. Its log is cut back when it grows past maxLogBytes: the end of
// it stays, which is where the trouble shows.
const (
	maxLogBytes  = 32 << 20
	keepLogBytes = 1 << 20
	logCheck     = 30 * time.Second
)

// watchLog keeps the log at path within bounds until done is closed.
func watchLog(path string, done <-chan struct{}) {
	ticker := time.NewTicker(logCheck)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			// a log that cannot be cut is no reason to stop the miner
			trimLog(path, maxLogBytes, keepLogBytes)
		}
	}
}

// trimLog cuts the file at path down to its last keep bytes if it is larger
// than limit, and reports whether it did. The program that writes the file
// appends to it, so it carries on behind what is left. Lines it writes in
// the very moment of the cut may be lost.
func trimLog(path string, limit, keep int64) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	size := info.Size()
	if size <= limit {
		return false, nil
	}
	tail := make([]byte, min(keep, size))
	if _, err := f.ReadAt(tail, size-int64(len(tail))); err != nil && err != io.EOF {
		return false, err
	}
	// start at a whole line
	for i, c := range tail {
		if c == '\n' {
			tail = tail[i+1:]
			break
		}
	}
	if err := f.Truncate(0); err != nil {
		return false, err
	}
	note := fmt.Sprintf("[hashsteer: the log grew to %d MB and was cut down to its end at %s]\n",
		size>>20, time.Now().Format("2006-01-02 15:04:05"))
	if _, err := f.WriteAt(append([]byte(note), tail...), 0); err != nil {
		return true, err
	}
	return true, nil
}
