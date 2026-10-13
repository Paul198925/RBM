package update

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// swapTries and swapWait bound how long SwapIn waits for a file that is in
// use for a moment, as a virus scanner has a freshly written program.
const (
	swapTries = 10
	swapWait  = 500 * time.Millisecond
)

// sibling turns ".../hashsteer.exe" and "new" into ".../hashsteer.new.exe",
// and ".../hashsteer" into ".../hashsteer.new".
func sibling(exe, tag string) string {
	dir, base := filepath.Dir(exe), filepath.Base(exe)
	if name, isExe := strings.CutSuffix(base, ".exe"); isExe {
		return filepath.Join(dir, name+"."+tag+".exe")
	}
	return filepath.Join(dir, base+"."+tag)
}

// NewPath is where a newer build of the program exe waits to take over.
func NewPath(exe string) string { return sibling(exe, "new") }

// OldPath is where the build that was replaced lies until it is cleared.
func OldPath(exe string) string { return sibling(exe, "old") }

// SwapIn puts the build at NewPath(exe) in the place of exe, while exe may
// be the running program. Windows does not let a running program be
// overwritten, but it lets it be renamed: the old build is moved aside to
// OldPath(exe), for ClearOld to remove once it has ended. If the new build
// cannot be put in place, the old one is put back and everything is as
// before.
func SwapIn(exe string) error {
	fresh := NewPath(exe)
	if _, err := os.Stat(fresh); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		// the running program keeps the file it was started from
		return os.Rename(fresh, exe)
	}
	old := OldPath(exe)
	os.Remove(old)
	if err := retry(func() error { return os.Rename(exe, old) }); err != nil {
		return fmt.Errorf("moving the running program aside: %w", err)
	}
	if err := retry(func() error { return os.Rename(fresh, exe) }); err != nil {
		if back := os.Rename(old, exe); back != nil {
			return fmt.Errorf("putting the new program in place: %w; and the old one could not be put back: %v", err, back)
		}
		return fmt.Errorf("putting the new program in place: %w", err)
	}
	return nil
}

// ClearOld removes what SwapIn moved aside. It is for the start of the
// program, when the build that was replaced no longer runs.
func ClearOld(exe string) {
	os.Remove(OldPath(exe))
}

func retry(fn func() error) error {
	var err error
	for i := 0; i < swapTries; i++ {
		if err = fn(); err == nil || os.IsNotExist(err) {
			return err
		}
		time.Sleep(swapWait)
	}
	return err
}
