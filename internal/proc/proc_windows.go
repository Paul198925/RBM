package proc

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// guard holds the job object the program lives in. The job is set to kill
// its processes when the last handle to it closes, and we hold the only
// handle: the kernel ends the program and its children when we exit, however
// that happens.
type guard struct {
	job windows.Handle
}

func configure(cmd *exec.Cmd, spec Spec) {
	// own hidden console and process group: the program gets neither our
	// window nor the Ctrl+C meant for us
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP)
	if spec.LowPriority {
		flags |= windows.BELOW_NORMAL_PRIORITY_CLASS
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
}

func (g *guard) attach(cmd *exec.Cmd, spec Spec) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	g.job = job

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return err
	}

	// the program is already running at this point; a child it forked in
	// these first microseconds would be outside the job
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}

// interrupt reports false: a program with its own hidden console cannot be
// sent Ctrl+C from here.
func (g *guard) interrupt(cmd *exec.Cmd) bool { return false }

func (g *guard) kill(cmd *exec.Cmd) {
	if g.job != 0 {
		windows.TerminateJobObject(g.job, 1)
	}
	cmd.Process.Kill()
}

func (g *guard) release() {
	if g.job != 0 {
		windows.CloseHandle(g.job)
		g.job = 0
	}
}
