package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const swapHelperEnv = "HASHSTEER_SWAP_HELPER"

// TestMain doubles as a program that just runs, so that a test can replace
// a program file that is in use.
func TestMain(m *testing.M) {
	if os.Getenv(swapHelperEnv) != "" {
		time.Sleep(time.Minute)
		return
	}
	os.Exit(m.Run())
}

func TestPathsNextToTheProgram(t *testing.T) {
	for exe, want := range map[string][2]string{
		filepath.Join("mining", "hashsteer.exe"): {filepath.Join("mining", "hashsteer.new.exe"), filepath.Join("mining", "hashsteer.old.exe")},
		filepath.Join("opt", "hashsteer"):        {filepath.Join("opt", "hashsteer.new"), filepath.Join("opt", "hashsteer.old")},
		filepath.Join("mining", "rbm.exe"):       {filepath.Join("mining", "rbm.new.exe"), filepath.Join("mining", "rbm.old.exe")},
	} {
		if NewPath(exe) != want[0] || OldPath(exe) != want[1] {
			t.Errorf("%s: %s, %s", exe, NewPath(exe), OldPath(exe))
		}
	}
}

// The program file of a running program is replaced, the way an update
// replaces hashsteer itself.
func TestSwapInReplacesARunningProgram(t *testing.T) {
	dir := t.TempDir()
	name := "hashsteer"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(dir, name)
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, self, 0o755); err != nil {
		t.Fatal(err)
	}

	// nothing fetched: nothing happens
	if err := SwapIn(exe); err == nil {
		t.Error("swapped in a build that is not there")
	}

	running := exec.Command(exe)
	running.Env = append(os.Environ(), swapHelperEnv+"=1")
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		running.Process.Kill()
		running.Wait()
	}()
	time.Sleep(200 * time.Millisecond)

	if err := os.WriteFile(NewPath(exe), []byte("the new build"), 0o755); err != nil {
		t.Fatal(err)
	}
	// a stale leftover of an earlier update is in the way
	if err := os.WriteFile(OldPath(exe), []byte("older still"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SwapIn(exe); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "the new build" {
		t.Errorf("in place of the program: %d bytes, %q...", len(got), strings.ToValidUTF8(string(got[:min(len(got), 20)]), "?"))
	}
	if _, err := os.Stat(NewPath(exe)); err == nil {
		t.Error("the new build still waits next to the program")
	}
	if runtime.GOOS == "windows" {
		if got, _ := os.ReadFile(OldPath(exe)); len(got) != len(self) {
			t.Errorf("moved aside: %d bytes, want the %d of the running program", len(got), len(self))
		}
	}

	// once the old build has ended, what was moved aside goes
	running.Process.Kill()
	running.Wait()
	ClearOld(exe)
	if _, err := os.Stat(OldPath(exe)); err == nil {
		t.Error("the old build is still there")
	}
	ClearOld(exe)
}
