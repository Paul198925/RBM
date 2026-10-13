package runner

import (
	"context"
	"errors"
	"fmt"

	"hashsteer/internal/api"
	"hashsteer/internal/engine"
	"hashsteer/internal/sysfix"
)

// needs turns what the engine says a miner program lacks into what sysfix
// calls it.
func needs(b engine.BrokenMiner) string {
	if b.Needs == engine.NeedsVCRedist {
		return sysfix.VCRedist
	}
	return b.Needs
}

// problems lists the miner programs that cannot run on this machine, each
// with what hashsteer would install for it if asked to.
func (a *App) problems() []api.Problem {
	list := []api.Problem{}
	e := a.currentEngine()
	if e == nil {
		return list
	}
	for _, b := range e.Status().Broken {
		p := api.Problem{Miner: b.Miner, Reason: b.Reason}
		if fix, ok := a.fixer.Find(needs(b)); ok {
			p.Package, p.Command = fix.Package, fix.Command
		}
		list = append(list, p)
	}
	return list
}

// FixProblem installs what the miner program of that name lacks and lets
// it back in. It only runs the command that problems lists for it, and
// only because the user asked for it.
func (a *App) FixProblem(ctx context.Context, miner string) error {
	e := a.currentEngine()
	if e == nil {
		return errors.New("nothing is being mined")
	}
	for _, b := range e.Status().Broken {
		if b.Miner != miner {
			continue
		}
		fix, ok := a.fixer.Find(needs(b))
		if !ok {
			return fmt.Errorf("hashsteer knows no way to install what %s lacks: %s", miner, b.Reason)
		}
		a.logf("installing %s for %s: %s", fix.Package, miner, fix.Command)
		if _, err := a.fixer.Run(ctx, fix); err != nil {
			a.logf("installing %s: %v", fix.Package, err)
			return err
		}
		a.logf("%s is installed", fix.Package)
		e.Mend(miner)
		return nil
	}
	return fmt.Errorf("%s has no problem that is known here", miner)
}
