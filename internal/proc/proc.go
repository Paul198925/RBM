// Package proc starts and stops miner programs. A started program and
// everything it forks is tied to this process: if we die, it dies with us.
package proc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// killWait bounds how long Stop waits after a forced kill.
const killWait = 10 * time.Second

// Spec describes a program to run.
type Spec struct {
	Path string
	Args []string
	// Dir is the working directory, the directory of Path if empty.
	Dir string
	// Env holds additional NAME=value pairs.
	Env []string
	// LogPath receives stdout and stderr. Empty discards the output.
	LogPath string
	// LowPriority runs the program below normal priority.
	LowPriority bool
	// Shutdown asks the program to exit on its own, e.g. through its API.
	// Optional; Stop falls back to signals and finally a kill.
	Shutdown func(ctx context.Context) error
}

// Process is a running or finished program.
type Process struct {
	spec    Spec
	cmd     *exec.Cmd
	started time.Time
	done    chan struct{}

	mu      sync.Mutex
	exitErr error
	guard   guard
}

// Start runs the program described by spec.
func Start(spec Spec) (*Process, error) {
	// a relative program path would be looked up relative to the working
	// directory set below, not to ours
	path, err := filepath.Abs(spec.Path)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, spec.Args...)
	cmd.Dir = spec.Dir
	if cmd.Dir == "" {
		cmd.Dir = filepath.Dir(path)
	}
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}

	// the log file is handed to the program directly: no pipe that could
	// fill up and stall it, and nothing left open by forked workers
	var logFile *os.File
	if spec.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		logFile = f
		cmd.Stdout = f
		cmd.Stderr = f
	}
	closeLog := func() {
		if logFile != nil {
			logFile.Close()
		}
	}

	p := &Process{spec: spec, cmd: cmd, done: make(chan struct{})}
	configure(cmd, spec)
	if err := cmd.Start(); err != nil {
		closeLog()
		return nil, fmt.Errorf("start %s: %w", spec.Path, err)
	}
	p.started = time.Now()

	if err := p.guard.attach(cmd, spec); err != nil {
		// never leave a program running that is not tied to us
		cmd.Process.Kill()
		cmd.Wait()
		p.guard.release()
		closeLog()
		return nil, fmt.Errorf("guard %s: %w", spec.Path, err)
	}

	if spec.LogPath != "" {
		go watchLog(spec.LogPath, p.done)
	}
	go func() {
		err := cmd.Wait()
		// whatever the program forked must not outlive it
		p.mu.Lock()
		p.guard.kill(cmd)
		p.guard.release()
		p.exitErr = err
		p.mu.Unlock()
		closeLog()
		close(p.done)
	}()
	return p, nil
}

// PID returns the process id of the program.
func (p *Process) PID() int { return p.cmd.Process.Pid }

// Started returns when the program was started.
func (p *Process) Started() time.Time { return p.started }

// Done is closed once the program has exited.
func (p *Process) Done() <-chan struct{} { return p.done }

// Running reports whether the program is still running.
func (p *Process) Running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// ExitErr returns how the program ended: nil for exit code 0, otherwise an
// *exec.ExitError. It is only meaningful after Done is closed.
func (p *Process) ExitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

// Stop ends the program and everything it forked. It first asks nicely
// (Spec.Shutdown, then an interrupt where the platform has one) and waits up
// to grace, then kills.
func (p *Process) Stop(ctx context.Context, grace time.Duration) error {
	if !p.Running() {
		return nil
	}

	asked := false
	if p.spec.Shutdown != nil {
		sctx, cancel := context.WithTimeout(ctx, grace)
		asked = p.spec.Shutdown(sctx) == nil
		cancel()
	}
	if !asked {
		p.mu.Lock()
		asked = p.guard.interrupt(p.cmd)
		p.mu.Unlock()
	}
	if asked && p.wait(ctx, grace) {
		return nil
	}

	p.mu.Lock()
	p.guard.kill(p.cmd)
	p.mu.Unlock()
	if p.wait(ctx, killWait) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("process %d did not exit after kill", p.PID())
}

func (p *Process) wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.done:
		return true
	case <-t.C:
		return false
	case <-ctx.Done():
		return false
	}
}
