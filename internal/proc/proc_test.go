package proc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const helperEnv = "HASHSTEER_PROC_HELPER"

// TestMain doubles as the program under test: started with helperEnv set,
// the test binary plays a miner instead of running the tests.
func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "exit3":
		fmt.Println("hello from helper")
		fmt.Fprintln(os.Stderr, "and from stderr")
		os.Exit(3)
	case "sleep":
		fmt.Println("ready")
		time.Sleep(time.Hour)
	case "spawn":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := child.Start(); err != nil {
			fmt.Println("spawn failed:", err)
			os.Exit(1)
		}
		fmt.Printf("child %d\n", child.Process.Pid)
		time.Sleep(time.Hour)
	}
}

func helper(t *testing.T, mode string) (Spec, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "logs", "helper.log")
	return Spec{
		Path:        os.Args[0],
		Dir:         t.TempDir(),
		Env:         []string{helperEnv + "=" + mode},
		LogPath:     logPath,
		LowPriority: true,
	}, logPath
}

func waitDone(t *testing.T, p *Process, d time.Duration) {
	t.Helper()
	select {
	case <-p.Done():
	case <-time.After(d):
		p.Stop(context.Background(), 0)
		t.Fatal("process did not exit in time")
	}
}

// waitLog polls the log file until re matches and returns the first group.
func waitLog(t *testing.T, path string, re *regexp.Regexp) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		if m := re.FindSubmatch(data); m != nil {
			return string(m[len(m)-1])
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("log %s never matched %v", path, re)
	return ""
}

func TestExitCodeAndLog(t *testing.T) {
	spec, logPath := helper(t, "exit3")
	p, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, p, 10*time.Second)

	if p.Running() {
		t.Error("still running after Done")
	}
	var exitErr *exec.ExitError
	if !errors.As(p.ExitErr(), &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("exit error = %v", p.ExitErr())
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hello from helper", "and from stderr"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log misses %q: %q", want, data)
		}
	}
	// stopping a finished process is not an error
	if err := p.Stop(context.Background(), time.Second); err != nil {
		t.Error(err)
	}
}

// a miner is installed below the data directory, which is usually given
// relative to where we were started
func TestRelativePath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, os.Args[0])
	if err != nil {
		t.Skip("test binary is not reachable by a relative path:", err)
	}
	// no Dir: the program's own directory becomes the working directory
	p, err := Start(Spec{Path: rel, Env: []string{helperEnv + "=exit3"}})
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, p, 10*time.Second)
	var exitErr *exec.ExitError
	if !errors.As(p.ExitErr(), &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("exit error = %v", p.ExitErr())
	}
}

func TestStartFailure(t *testing.T) {
	_, err := Start(Spec{Path: filepath.Join(t.TempDir(), "does-not-exist")})
	if err == nil {
		t.Fatal("no error for a missing program")
	}
}

func TestStopEndsProcess(t *testing.T) {
	spec, logPath := helper(t, "sleep")
	p, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	waitLog(t, logPath, regexp.MustCompile(`ready`))
	if !p.Running() {
		t.Fatal("not running")
	}

	start := time.Now()
	if err := p.Stop(context.Background(), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if p.Running() {
		t.Error("still running after Stop")
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Errorf("Stop took %v", d)
	}
	if pidAlive(p.PID()) {
		t.Error("pid is still alive")
	}
}

func TestShutdownHookIsPreferred(t *testing.T) {
	spec, logPath := helper(t, "sleep")
	called := false
	var p *Process
	spec.Shutdown = func(ctx context.Context) error {
		called = true
		// stands in for a miner that exits when its API is told to
		return p.cmd.Process.Kill()
	}
	p, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	waitLog(t, logPath, regexp.MustCompile(`ready`))
	if err := p.Stop(context.Background(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("shutdown hook was not called")
	}
}

func TestStopEndsChildren(t *testing.T) {
	spec, logPath := helper(t, "spawn")
	p, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(waitLog(t, logPath, regexp.MustCompile(`child (\d+)`)))
	if err != nil {
		t.Fatal(err)
	}
	if !pidAlive(child) {
		t.Fatal("child is not running")
	}

	if err := p.Stop(context.Background(), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for pidAlive(child) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pidAlive(child) {
		t.Errorf("child %d survived Stop", child)
	}
}

// a program that dies on its own must not leave its children behind either
func TestCrashEndsChildren(t *testing.T) {
	spec, logPath := helper(t, "spawn")
	p, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(waitLog(t, logPath, regexp.MustCompile(`child (\d+)`)))
	if err != nil {
		t.Fatal(err)
	}

	p.cmd.Process.Kill()
	waitDone(t, p, 10*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for pidAlive(child) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if pidAlive(child) {
		t.Errorf("child %d survived the crash of its parent", child)
	}
}
