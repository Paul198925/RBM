package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"rbm/internal/device"
	"rbm/internal/miner"
	"rbm/internal/pool"
	"rbm/internal/proc"
)

// Miner is a running miner program as the engine sees it.
type Miner interface {
	// Poll reads the current statistics.
	Poll(ctx context.Context) (miner.Sample, error)
	// Done is closed once the program has exited.
	Done() <-chan struct{}
	// Stop ends the program and everything it started.
	Stop(ctx context.Context) error
	// ExitText says how the program ended, for the log.
	ExitText() string
	// Describe names the running program for the log.
	Describe() string
}

// LaunchSpec is everything needed to start a miner.
type LaunchSpec struct {
	Definition *miner.Definition
	Algorithm  miner.Algorithm
	Devices    []device.Device
	Offer      pool.Offer
	TLS        bool
	// Threads limits a CPU miner, 0 lets the miner decide.
	Threads int
	// ExtraArgs are the user's additions to the miner's command line.
	ExtraArgs []string
	DataDir   string
	// Tag tells the log files of several miners apart.
	Tag string
}

// LaunchFunc starts a miner. The engine takes it as a dependency, so that
// tests can run without real miner programs.
type LaunchFunc func(ctx context.Context, spec LaunchSpec) (Miner, error)

type launched struct {
	name    string
	api     miner.API
	port    int
	process *proc.Process
	logPath string
}

// Launch installs the miner program if needed and starts it.
func Launch(ctx context.Context, spec LaunchSpec) (Miner, error) {
	def := spec.Definition
	api, err := miner.NewAPI(def.API)
	if err != nil {
		return nil, err
	}
	exe, err := def.Install(ctx, filepath.Join(spec.DataDir, "bin"), nil)
	if err != nil {
		return nil, err
	}

	port := spec.Offer.Port
	if spec.TLS {
		if spec.Offer.TLSPort == 0 {
			return nil, fmt.Errorf("%s has no TLS port for %s", spec.Offer.Pool, spec.Offer.Algorithm)
		}
		port = spec.Offer.TLSPort
	}
	apiPort, err := freePort()
	if err != nil {
		return nil, err
	}
	indexes := make([]int, len(spec.Devices))
	for i, d := range spec.Devices {
		indexes[i] = d.Index
	}
	args, err := def.BuildArgs(miner.Job{
		Algorithm:     spec.Algorithm,
		DeviceIndexes: indexes,
		APIPort:       apiPort,
		Host:          spec.Offer.Host,
		Port:          port,
		TLS:           spec.TLS,
		User:          spec.Offer.User,
		Pass:          spec.Offer.Pass,
		Threads:       spec.Threads,
	})
	if err != nil {
		return nil, err
	}
	args = append(args, spec.ExtraArgs...)

	logPath := filepath.Join(spec.DataDir, "logs", fmt.Sprintf("%s-%s-%s-%s.log",
		def.Name, spec.Algorithm.Name, spec.Tag, time.Now().Format("20060102-150405")))
	process, err := proc.Start(proc.Spec{Path: exe, Args: args, LogPath: logPath, LowPriority: true})
	if err != nil {
		return nil, err
	}
	return &launched{name: def.Name, api: api, port: apiPort, process: process, logPath: logPath}, nil
}

func (l *launched) Poll(ctx context.Context) (miner.Sample, error) { return l.api.Poll(ctx, l.port) }
func (l *launched) Done() <-chan struct{}                          { return l.process.Done() }
func (l *launched) Stop(ctx context.Context) error                 { return l.process.Stop(ctx, stopGrace) }

func (l *launched) Describe() string {
	return fmt.Sprintf("pid %d, log %s", l.process.PID(), l.logPath)
}

func (l *launched) ExitText() string {
	err := l.process.ExitErr()
	if err == nil {
		return "exit code 0"
	}
	var exitErr interface{ ExitCode() int }
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("exit code %d", exitErr.ExitCode())
	}
	return err.Error()
}

// freePort returns a local TCP port that is free right now.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
