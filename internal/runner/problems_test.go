package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hashsteer/internal/engine"
	"hashsteer/internal/sysfix"
)

func TestNeedsSpeaksTheFixersLanguage(t *testing.T) {
	if needs(engine.BrokenMiner{Needs: engine.NeedsVCRedist}) != sysfix.VCRedist {
		t.Error("the Visual C++ runtime is not passed on")
	}
	if needs(engine.BrokenMiner{Needs: "libcurl.so.4"}) != "libcurl.so.4" || needs(engine.BrokenMiner{}) != "" {
		t.Error("a library is not passed on as it is")
	}
}

func TestProblemsWithoutAnEngine(t *testing.T) {
	app, _ := testApp(t)
	if p := app.problems(); p == nil || len(p) != 0 {
		t.Errorf("problems = %+v", p)
	}
	if got := app.System().Problems; got == nil || len(got) != 0 {
		t.Errorf("system problems = %+v", got)
	}
	err := app.FixProblem(context.Background(), "ccminer-tpfuemp")
	if err == nil || !strings.Contains(err.Error(), "nothing is being mined") {
		t.Errorf("fixing without an engine: %v", err)
	}
}

// What the fixer is asked to run is what the user was shown, and nothing
// else: here for a machine with apt-get.
func TestTheFixIsTheCommandThatWasShown(t *testing.T) {
	var ran []string
	fixer := sysfix.System{GOOS: "linux",
		LookPath: func(name string) (string, error) {
			if name == "apt-get" {
				return "/usr/bin/apt-get", nil
			}
			return "", errors.New("not found")
		},
		Exec: func(_ context.Context, argv []string) (string, error) {
			ran = append(ran, strings.Join(argv, " "))
			return "", nil
		}}
	fix, ok := fixer.Find(needs(engine.BrokenMiner{Miner: "ccminer-tpfuemp", Needs: "libcurl.so.4"}))
	if !ok || fix.Command != "sudo apt-get install -y libcurl4" {
		t.Fatalf("fix = %+v, %v", fix, ok)
	}
	if _, err := fixer.Run(context.Background(), fix); err != nil || len(ran) != 1 || ran[0] != "sudo -n apt-get install -y libcurl4" {
		t.Errorf("ran %v, %v", ran, err)
	}
	// a driver that is too old has nothing to install
	if _, ok := fixer.Find(needs(engine.BrokenMiner{Miner: "trex", Reason: "the NVIDIA driver is too old"})); ok {
		t.Error("a fix for a driver")
	}
}
