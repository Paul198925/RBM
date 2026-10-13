package runner

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"hashsteer/internal/miner"
	"hashsteer/internal/sysdeps"
	"hashsteer/internal/sysfix"
)

// dependencyApp is an app with one miner program that needs three
// libraries of a made-up system: one that is there, one that can be
// installed and one that comes with the driver.
func dependencyApp(t *testing.T) (*App, *[]string, map[string]bool) {
	t.Helper()
	app, _ := testApp(t)
	def := *app.definitions["rigel"]
	def.Name = "needy"
	def.Requires = map[string][]string{runtime.GOOS: {"libstdc++.so.6", "libcurl.so.4", "libcuda.so.1"}}
	plain := *app.definitions["rigel"]
	plain.Requires = nil
	app.definitions = map[string]*miner.Definition{"needy": &def, "rigel": &plain}

	have := map[string]bool{"libstdc++.so.6": true}
	app.finder = sysdeps.Finder{GOOS: "linux", Cache: func() string { return "" },
		Exists: func(path string) bool { return have[filepath.Base(path)] }}
	var ran []string
	app.fixer = sysfix.System{GOOS: "linux",
		LookPath: func(name string) (string, error) {
			if name == "apt-get" {
				return "/usr/bin/apt-get", nil
			}
			return "", errors.New("not found")
		},
		Exec: func(_ context.Context, argv []string) (string, error) {
			ran = append(ran, strings.Join(argv, " "))
			// the package brings the library
			have["libcurl.so.4"] = true
			return "", nil
		}}
	return app, &ran, have
}

func TestDependenciesAreListed(t *testing.T) {
	app, _, _ := dependencyApp(t)
	list := app.Dependencies()
	if len(list) != 2 || list[0].Miner != "needy" || list[1].Miner != "rigel" {
		t.Fatalf("dependencies = %+v", list)
	}
	if list[1].Needs == nil || len(list[1].Needs) != 0 {
		t.Errorf("a program that needs nothing: %+v", list[1].Needs)
	}
	needs := list[0].Needs
	if len(needs) != 3 {
		t.Fatalf("needs = %+v", needs)
	}
	// there already: nothing to offer
	if n := needs[0]; n.Library != "libstdc++.so.6" || !n.Present || n.Command != "" {
		t.Errorf("a library that is there: %+v", n)
	}
	// missing, and there is a package for it
	if n := needs[1]; n.Present || n.Package != "libcurl4" || n.Command != "sudo apt-get install -y libcurl4" || n.FromDriver {
		t.Errorf("a library that can be installed: %+v", n)
	}
	// missing, and only the driver brings it
	if n := needs[2]; n.Present || n.Command != "" || !n.FromDriver {
		t.Errorf("a library of the driver: %+v", n)
	}
}

func TestInstallDependency(t *testing.T) {
	app, ran, _ := dependencyApp(t)
	ctx := context.Background()

	// only what a miner program needs, only what is missing, only what has a package
	for library, want := range map[string]string{
		"libevil.so.1; rm -rf ~": "no miner program needs",
		"libstdc++.so.6":         "is there already",
		"libcuda.so.1":           "comes with the driver",
	} {
		err := app.InstallDependency(ctx, library)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", library, err)
		}
	}
	if len(*ran) != 0 {
		t.Fatalf("ran %v", *ran)
	}

	if err := app.InstallDependency(ctx, "libcurl.so.4"); err != nil {
		t.Fatal(err)
	}
	if len(*ran) != 1 || (*ran)[0] != "sudo -n apt-get install -y libcurl4" {
		t.Errorf("ran %v", *ran)
	}
	if n := app.Dependencies()[0].Needs[1]; !n.Present || n.Command != "" {
		t.Errorf("after the installation: %+v", n)
	}
}

// The definitions that come with the program name nothing twice and
// nothing that every system has anyway.
func TestBuiltinRequirements(t *testing.T) {
	defs, err := miner.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for name, def := range defs {
		for goos, libs := range def.Requires {
			if _, ok := def.Downloads[goos]; !ok {
				t.Errorf("%s requires something for %s, for which it has no download", name, goos)
			}
			seen := map[string]bool{}
			for _, lib := range libs {
				if seen[lib] || !sysdeps.Notable(goos, lib) {
					t.Errorf("%s for %s: %s is listed twice or is part of every system", name, goos, lib)
				}
				seen[lib] = true
				// what is missing can either be installed or comes with the driver
				_, linux := (sysfix.System{GOOS: goos, LookPath: func(string) (string, error) { return "x", nil }}).Find(lib)
				if !linux && !sysdeps.FromDriver(lib) {
					t.Errorf("%s for %s: nothing is known to bring %s", name, goos, lib)
				}
			}
		}
	}
}
