// Package sysfix knows how to install what a miner program lacks on this
// machine: a shared library on Linux, the Visual C++ runtime on Windows. It
// only ever runs a command from its own table, with the package manager the
// system has, and only when asked to.
package sysfix

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// VCRedist is the name of what a Windows program lacks that could not load
// a DLL of the Visual C++ runtime.
const VCRedist = "vcredist"

// Fix is a way to install what is missing.
type Fix struct {
	// Package is what gets installed, by the name the package manager
	// knows it under.
	Package string
	// Command is the whole command, as one would type it.
	Command string
	argv    []string
}

// packages names the package that holds a shared library, per package
// manager. A library that is not listed has no fix: guessing a package name
// would install something else.
var packages = map[string]map[string]string{
	"libcurl.so.4":     {"apt-get": "libcurl4", "dnf": "libcurl", "pacman": "curl", "zypper": "libcurl4"},
	"libOpenCL.so.1":   {"apt-get": "ocl-icd-libopencl1", "dnf": "ocl-icd", "pacman": "ocl-icd", "zypper": "libOpenCL1"},
	"libgomp.so.1":     {"apt-get": "libgomp1", "dnf": "libgomp", "pacman": "gcc-libs", "zypper": "libgomp1"},
	"libjansson.so.4":  {"apt-get": "libjansson4", "dnf": "jansson", "pacman": "jansson", "zypper": "libjansson4"},
	"libhwloc.so.15":   {"apt-get": "libhwloc15", "dnf": "hwloc-libs", "pacman": "hwloc", "zypper": "libhwloc15"},
	"libnuma.so.1":     {"apt-get": "libnuma1", "dnf": "numactl-libs", "pacman": "numactl", "zypper": "libnuma1"},
	"libuv.so.1":       {"apt-get": "libuv1", "dnf": "libuv", "pacman": "libuv", "zypper": "libuv1"},
	"libstdc++.so.6":   {"apt-get": "libstdc++6", "dnf": "libstdc++", "pacman": "gcc-libs", "zypper": "libstdc++6"},
	"libmicrohttpd.so": {"apt-get": "libmicrohttpd12", "dnf": "libmicrohttpd", "pacman": "libmicrohttpd", "zypper": "libmicrohttpd12"},
}

// how each package manager installs a package without asking back
var installs = map[string][]string{
	"apt-get": {"apt-get", "install", "-y"},
	"dnf":     {"dnf", "install", "-y"},
	"pacman":  {"pacman", "-S", "--noconfirm", "--needed"},
	"zypper":  {"zypper", "--non-interactive", "install"},
}

// the order in which the package managers are looked for
var managers = []string{"apt-get", "dnf", "pacman", "zypper"}

// System is what Find and Run need to know about the machine. The zero
// value is this machine.
type System struct {
	// GOOS replaces runtime.GOOS. For tests.
	GOOS string
	// LookPath replaces exec.LookPath. For tests.
	LookPath func(name string) (string, error)
	// Exec replaces running a command; it returns what the command wrote.
	// For tests.
	Exec func(ctx context.Context, argv []string) (string, error)
}

func (s System) goos() string {
	if s.GOOS != "" {
		return s.GOOS
	}
	return runtime.GOOS
}

func (s System) has(name string) bool {
	look := s.LookPath
	if look == nil {
		look = exec.LookPath
	}
	_, err := look(name)
	return err == nil
}

// Find returns the way to install what a miner program lacks: the name of
// a shared library as the loader names it, or VCRedist. It reports false
// where there is none: an unknown library, or no package manager.
func (s System) Find(needs string) (Fix, bool) {
	switch s.goos() {
	case "windows":
		if needs != VCRedist || !s.has("winget") {
			return Fix{}, false
		}
		argv := []string{"winget", "install", "--id", "Microsoft.VCRedist.2015+.x64", "-e",
			"--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"}
		return Fix{Package: "Microsoft Visual C++ Redistributable", Command: strings.Join(argv, " "), argv: argv}, true
	case "linux":
		byManager, known := packages[needs]
		if !known {
			return Fix{}, false
		}
		for _, manager := range managers {
			if pkg := byManager[manager]; pkg != "" && s.has(manager) {
				install := append(append([]string{}, installs[manager]...), pkg)
				// -n: never wait for a password nobody can type here
				argv := append([]string{"sudo", "-n"}, install...)
				return Fix{Package: pkg, Command: "sudo " + strings.Join(install, " "), argv: argv}, true
			}
		}
	}
	return Fix{}, false
}

// ErrNeedsPassword says the installation needs the user at a terminal.
var ErrNeedsPassword = errors.New("needs a password")

// Run installs the package. On Linux that takes root, which is asked for
// with sudo and only works where sudo does not ask for a password; where it
// does, the error is ErrNeedsPassword and Command is for the user to run.
// On Windows the system may ask on the desktop whether to allow it.
func (s System) Run(ctx context.Context, f Fix) (string, error) {
	if len(f.argv) == 0 {
		return "", errors.New("nothing to install")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	run := s.Exec
	if run == nil {
		run = func(ctx context.Context, argv []string) (string, error) {
			out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
			return string(out), err
		}
	}
	out, err := run(ctx, f.argv)
	out = strings.TrimSpace(out)
	if len(out) > 2000 {
		out = out[len(out)-2000:]
	}
	if err != nil {
		lower := strings.ToLower(out)
		if f.argv[0] == "sudo" && (strings.Contains(lower, "password is required") || strings.Contains(lower, "a terminal is required")) {
			return out, fmt.Errorf("%w: sudo asks for one here. Run this in a terminal: %s", ErrNeedsPassword, f.Command)
		}
		if out != "" {
			return out, fmt.Errorf("%s failed: %v: %s", f.argv[0], err, lastLine(out))
		}
		return out, fmt.Errorf("%s failed: %v", f.argv[0], err)
	}
	return out, nil
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
