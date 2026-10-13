package runner

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"hashsteer/internal/api"
	"hashsteer/internal/miner"
	"hashsteer/internal/sysdeps"
)

// Dependencies says for every miner program that exists for this system
// what it needs from the system, and whether that is there. What is
// missing comes with the package hashsteer would install for it, where it
// knows one.
func (a *App) Dependencies() []api.Dependency {
	list := []api.Dependency{}
	for _, name := range miner.Names(a.definitions) {
		def := a.definitions[name]
		if _, ok := def.Downloads[runtime.GOOS]; !ok {
			continue
		}
		d := api.Dependency{Miner: def.Name, Version: def.Version, Vendor: def.Vendor, Needs: []api.Need{}}
		for _, library := range def.Requires[runtime.GOOS] {
			need := api.Need{Library: library, Present: a.finder.Present(library), FromDriver: sysdeps.FromDriver(library)}
			if !need.Present {
				if fix, ok := a.fixer.Find(library); ok {
					need.Package, need.Command = fix.Package, fix.Command
				}
			}
			d.Needs = append(d.Needs, need)
		}
		list = append(list, d)
	}
	return list
}

// InstallDependency installs the package that holds a library some miner
// program needs and that is missing. It only runs the command that
// Dependencies lists for it, and only because the user asked for it.
// Miner programs that were taken out for lacking something get another try.
func (a *App) InstallDependency(ctx context.Context, library string) error {
	required := false
	for _, def := range a.definitions {
		for _, lib := range def.Requires[runtime.GOOS] {
			required = required || lib == library
		}
	}
	if !required {
		return fmt.Errorf("no miner program needs %s here", library)
	}
	if a.finder.Present(library) {
		return fmt.Errorf("%s is there already", library)
	}
	fix, ok := a.fixer.Find(library)
	if !ok {
		if sysdeps.FromDriver(library) {
			return fmt.Errorf("%s comes with the driver of the graphics cards: install or update the NVIDIA driver", library)
		}
		return fmt.Errorf("hashsteer knows no package for %s", library)
	}
	a.logf("installing %s for %s: %s", fix.Package, library, fix.Command)
	if _, err := a.fixer.Run(ctx, fix); err != nil {
		a.logf("installing %s: %v", fix.Package, err)
		return err
	}
	a.logf("%s is installed", fix.Package)
	if e := a.currentEngine(); e != nil {
		for _, b := range e.Status().Broken {
			e.Mend(b.Miner)
		}
	}
	if !a.finder.Present(library) {
		return errors.New(fix.Package + " was installed, but " + library + " is still not found")
	}
	return nil
}
