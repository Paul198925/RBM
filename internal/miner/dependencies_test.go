package miner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"hashsteer/internal/sysdeps"
)

// TestListDependencies downloads every miner program, for Windows and for
// Linux, and prints what each needs from the system. It is how the list of
// packages in internal/sysfix is kept up to date, and only runs when asked
// to: set HASHSTEER_DEPS_DIR to a directory to download into. On Windows
// that should be one a virus scanner leaves alone, like data\bin. Nothing
// that is downloaded is run.
func TestListDependencies(t *testing.T) {
	base := os.Getenv("HASHSTEER_DEPS_DIR")
	if base == "" {
		t.Skip("set HASHSTEER_DEPS_DIR to download all miner programs and list what they need")
	}
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for _, goos := range []string{"windows", "linux"} {
		all := map[string][]string{}
		for _, name := range Names(defs) {
			dl, ok := defs[name].Downloads[goos]
			if !ok {
				continue
			}
			// the installer fetches what belongs to the system it runs
			// on: hand it the other system's download as its own
			def := *defs[name]
			def.Downloads = map[string]Download{runtime.GOOS: dl}
			dir := base
			if goos != runtime.GOOS {
				dir = filepath.Join(base, "_"+goos)
			}
			exe, err := def.Install(context.Background(), dir, nil)
			if err != nil {
				t.Errorf("%s for %s: %v", name, goos, err)
				continue
			}
			libs, err := sysdeps.Imports(def.Dir(dir))
			if err != nil {
				t.Errorf("%s for %s: %v", name, goos, err)
				continue
			}
			var notable []string
			for _, lib := range libs {
				if sysdeps.Notable(goos, lib) {
					notable = append(notable, lib)
				}
			}
			t.Logf("%-8s %-16s %s  imports: %s", goos, name, filepath.Base(exe), strings.Join(libs, " "))
			// what the definition says must be what the download shows
			listed := append([]string{}, defs[name].Requires[goos]...)
			sort.Strings(listed)
			sort.Strings(notable)
			if strings.Join(listed, " ") != strings.Join(notable, " ") {
				t.Errorf("%s for %s: the definition requires [%s], the download needs [%s]", name, goos, strings.Join(listed, ", "), strings.Join(notable, ", "))
			}
			for _, lib := range notable {
				all[lib] = append(all[lib], name)
			}
		}
		var libs []string
		for lib := range all {
			libs = append(libs, lib)
		}
		sort.Strings(libs)
		for _, lib := range libs {
			t.Logf("%-8s %-28s %s", goos, lib, strings.Join(all[lib], " "))
		}
	}
}
