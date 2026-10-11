package proc

import (
	"os/exec"
	"syscall"
)

// guard is the process group the program leads. Signals go to the whole
// group, so forked workers are reached as well.
type guard struct {
	pgid int
}

func configure(cmd *exec.Cmd, spec Spec) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		// the kernel kills the program when the thread that started it
		// exits, which covers a crash or kill of this process
		Pdeathsig: syscall.SIGKILL,
	}
}

func (g *guard) attach(cmd *exec.Cmd, spec Spec) error {
	g.pgid = cmd.Process.Pid
	if spec.LowPriority {
		// best effort: a failed renice is no reason not to mine
		syscall.Setpriority(syscall.PRIO_PGRP, g.pgid, 10)
	}
	return nil
}

func (g *guard) interrupt(cmd *exec.Cmd) bool {
	if g.pgid == 0 {
		return false
	}
	return syscall.Kill(-g.pgid, syscall.SIGINT) == nil
}

func (g *guard) kill(cmd *exec.Cmd) {
	if g.pgid != 0 {
		syscall.Kill(-g.pgid, syscall.SIGKILL)
	}
	cmd.Process.Kill()
}

func (g *guard) release() { g.pgid = 0 }
