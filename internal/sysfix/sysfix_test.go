package sysfix

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func with(goos string, tools ...string) System {
	return System{GOOS: goos, LookPath: func(name string) (string, error) {
		for _, t := range tools {
			if t == name {
				return "/usr/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}}
}

func TestFind(t *testing.T) {
	for _, c := range []struct {
		system  System
		needs   string
		command string
	}{
		{with("linux", "apt-get"), "libcurl.so.4", "sudo apt-get install -y libcurl4"},
		{with("linux", "apt-get"), "libOpenCL.so.1", "sudo apt-get install -y ocl-icd-libopencl1"},
		{with("linux", "dnf"), "libcurl.so.4", "sudo dnf install -y libcurl"},
		{with("linux", "pacman"), "libOpenCL.so.1", "sudo pacman -S --noconfirm --needed ocl-icd"},
		{with("linux", "zypper"), "libcurl.so.4", "sudo zypper --non-interactive install libcurl4"},
		// the first package manager that is there is the system's own
		{with("linux", "dnf", "apt-get"), "libcurl.so.4", "sudo apt-get install -y libcurl4"},
		{with("windows", "winget"), VCRedist, "winget install --id Microsoft.VCRedist.2015+.x64 -e --accept-package-agreements --accept-source-agreements --disable-interactivity"},
		{with("windows", "winget"), "VCRUNTIME140_1.dll", "winget install --id Microsoft.VCRedist.2015+.x64 -e --accept-package-agreements --accept-source-agreements --disable-interactivity"},
		{with("linux", "apt-get"), "libssl.so.3", "sudo apt-get install -y libssl3"},
		{with("linux", "pacman"), "libgcc_s.so.1", "sudo pacman -S --noconfirm --needed gcc-libs"},
		// nothing to offer
		{with("linux", "apt-get"), "libcuda.so.1", ""},
		{with("windows", "winget"), "nvcuda.dll", ""},
		{with("linux", "apt-get"), "libunheardof.so.9", ""},
		{with("linux"), "libcurl.so.4", ""},
		{with("linux", "apt-get"), VCRedist, ""},
		{with("windows", "winget"), "libcurl.so.4", ""},
		{with("windows"), VCRedist, ""},
		{with("linux", "apt-get"), "", ""},
		// nothing that is not in the table is ever put into a command
		{with("linux", "apt-get"), "libcurl.so.4; rm -rf ~", ""},
	} {
		fix, ok := c.system.Find(c.needs)
		if ok != (c.command != "") || fix.Command != c.command {
			t.Errorf("%s %q: %q, %v; want %q", c.system.GOOS, c.needs, fix.Command, ok, c.command)
		}
	}
}

func TestRun(t *testing.T) {
	var ran [][]string
	answer := func(out string, err error) System {
		s := with("linux", "apt-get")
		s.Exec = func(_ context.Context, argv []string) (string, error) {
			ran = append(ran, argv)
			return out, err
		}
		return s
	}
	ctx := context.Background()

	s := answer("Setting up libcurl4t64 (8.14.1) ...\n", nil)
	fix, _ := s.Find("libcurl.so.4")
	out, err := s.Run(ctx, fix)
	if err != nil || !strings.Contains(out, "Setting up") {
		t.Fatalf("out %q, err %v", out, err)
	}
	// it asks for root without ever waiting for a password
	if got := strings.Join(ran[0], " "); got != "sudo -n apt-get install -y libcurl4" {
		t.Errorf("ran %q", got)
	}

	// sudo wants a password: the user is told what to run
	s = answer("sudo: a password is required\n", errors.New("exit status 1"))
	_, err = s.Run(ctx, fix)
	if !errors.Is(err, ErrNeedsPassword) || !strings.Contains(err.Error(), "sudo apt-get install -y libcurl4") {
		t.Errorf("with a password prompt: %v", err)
	}
	// the package manager fails: its last line says why
	s = answer("Reading package lists...\nE: Unable to locate package libcurl4\n", errors.New("exit status 100"))
	_, err = s.Run(ctx, fix)
	if err == nil || errors.Is(err, ErrNeedsPassword) || !strings.Contains(err.Error(), "Unable to locate package libcurl4") {
		t.Errorf("with a failing package manager: %v", err)
	}
	// a fix that was not found runs nothing
	before := len(ran)
	if _, err := s.Run(ctx, Fix{}); err == nil || len(ran) != before {
		t.Errorf("an empty fix: %v, ran %d", err, len(ran)-before)
	}
}
