package runner

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"rbm/internal/config"
	"rbm/internal/device"
	"rbm/internal/miner"
)

// syncBuffer is a console the app can write to while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// testApp is an app with a fixed set of devices and no web interface. Its
// configuration has no wallet, so it never starts an engine or asks a pool.
func testApp(t *testing.T) (*App, *syncBuffer) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.General.API.Listen = ""
	cfg.General.DataDir = filepath.Join(root, "data")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	definitions, err := miner.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	out := &syncBuffer{}
	return &App{
		configDir: filepath.Join(root, "config"),
		out:       out,
		devices: []device.Device{
			{ID: "GPU#00", Kind: device.GPU, Vendor: "NVIDIA", Model: "GeForce RTX 3060", MemoryMiB: 12288},
			{ID: "CPU#00", Kind: device.CPU, Threads: 20},
		},
		definitions: definitions,
		reload:      make(chan struct{}, 1),
		cfg:         cfg,
		waiting:     "starting",
	}, out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAppWaitsForAConfigurationAndReloads(t *testing.T) {
	app, out := testApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	waitFor(t, "the app to report what is missing", func() bool { return strings.Contains(app.Waiting(), "wallet") })
	if len(app.Status().Groups) != 0 || app.Offers() != nil {
		t.Error("status without an engine is not empty")
	}

	// saving applies the configuration at once; it is still not enough to mine
	cfg := app.Config()
	cfg.General.Worker = "rig7"
	cfg.Pools.Zpool.Enabled = false
	if err := app.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the new reason", func() bool { return strings.Contains(app.Waiting(), "no pool is enabled") })
	if !strings.Contains(out.String(), "configuration changed") {
		t.Errorf("console:\n%s", out.String())
	}
	// and it is on disk
	loaded, err := config.LoadDir(app.configDir)
	if err != nil || loaded.General.Worker != "rig7" || loaded.Pools.Zpool.Enabled {
		t.Errorf("on disk: %+v, %v", loaded, err)
	}
	if lines := app.Log(); len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "not mining") {
		t.Errorf("log = %v", lines)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestSaveConfigRejects(t *testing.T) {
	app, _ := testApp(t)
	edits := map[string]func(*config.Config){
		"invalid value":  func(c *config.Config) { c.General.Worker = "no good" },
		"unknown miner":  func(c *config.Config) { c.Miners["nope"] = config.Miner{Disabled: true} },
		"unknown device": func(c *config.Config) { c.Devices.Exclude = []string{"GPU#07"} },
	}
	for name, edit := range edits {
		cfg := app.Config()
		edit(&cfg)
		if err := app.SaveConfig(cfg); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	if config.Exists(app.configDir) {
		t.Error("a rejected configuration was written")
	}

	// what the app knows is accepted
	cfg := app.Config()
	cfg.Miners["wildrig"] = config.Miner{Disabled: true}
	cfg.Devices.Exclude = []string{"GPU#00"}
	if err := app.SaveConfig(cfg); err != nil {
		t.Error(err)
	}
}

func TestConfigIsACopy(t *testing.T) {
	app, _ := testApp(t)
	app.cfg.Pools.Zpool.ExcludeCoins = []string{"SCC"}
	app.cfg.Miners["rigel"] = config.Miner{ExtraArgs: []string{"-x"}}

	cfg := app.Config()
	cfg.Pools.Zpool.ExcludeCoins[0] = "CHANGED"
	cfg.Miners["rigel"].ExtraArgs[0] = "CHANGED"
	cfg.Miners["new"] = config.Miner{}

	again := app.Config()
	if again.Pools.Zpool.ExcludeCoins[0] != "SCC" || again.Miners["rigel"].ExtraArgs[0] != "-x" || len(again.Miners) != 1 {
		t.Errorf("the caller changed the app's configuration: %+v", again)
	}
}

func TestMinerLogs(t *testing.T) {
	app, _ := testApp(t)
	if files, err := app.MinerLogs(); err != nil || len(files) != 0 {
		t.Errorf("without a log directory: %v, %v", files, err)
	}

	dir := app.logDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string, age time.Duration) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		os.Chtimes(path, when, when)
	}
	write("old.log", "old", time.Hour)
	write("new.log", "\x1b[38;5;2m[pool] GPU#0 solution accepted\x1b[0m\n", time.Minute)
	write("notes.txt", "not a log", 0)
	write("big.log", strings.Repeat("x", minerLogTail)+"THE END", 2*time.Hour)

	files, err := app.MinerLogs()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	if strings.Join(names, " ") != "new.log old.log big.log" {
		t.Errorf("logs = %v", names)
	}

	// colours are removed, long logs cut to their end
	if text, err := app.MinerLog("new.log"); err != nil || text != "[pool] GPU#0 solution accepted\n" {
		t.Errorf("new.log = %q, %v", text, err)
	}
	if text, _ := app.MinerLog("big.log"); len(text) != minerLogTail || !strings.HasSuffix(text, "THE END") {
		t.Errorf("big.log: %d bytes", len(text))
	}

	// only plain names of logs are read
	secret := filepath.Join(filepath.Dir(dir), "secret.log")
	os.WriteFile(secret, []byte("secret"), 0o600)
	for _, name := range []string{"../secret.log", `..\secret.log`, "notes.txt", "", "sub/x.log", filepath.Join(dir, "new.log")} {
		if text, err := app.MinerLog(name); err == nil {
			t.Errorf("%q was read: %q", name, text)
		}
	}
	if _, err := app.MinerLog("missing.log"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing log: %v", err)
	}
}

func TestMetaListsTheMiners(t *testing.T) {
	app, _ := testApp(t)
	// keep the test off the network: pretend the pool was asked just now
	app.currenciesAt = time.Now()
	meta := app.Meta(context.Background())
	var names []string
	for _, m := range meta.Miners {
		names = append(names, m.Name)
		if len(m.Algorithms) == 0 || m.Version == "" {
			t.Errorf("miner = %+v", m)
		}
	}
	if strings.Join(names, " ") != "ccminer-tpfuemp rigel srbminer wildrig xmrig" || !filepath.IsAbs(meta.ConfigDir) || len(meta.Regions) != 4 {
		t.Errorf("meta = %+v", meta)
	}
	if meta.Currencies == nil {
		t.Error("currencies must be a list, even if empty")
	}
}
