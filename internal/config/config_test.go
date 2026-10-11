package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(write(t, "worker: rig1\nzpool:\n  wallet: addr\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker != "rig1" || cfg.DataDir != "data" || cfg.Zpool.Region != "eu" || cfg.Zpool.Currency != "BTC" {
		t.Errorf("cfg = %+v", cfg)
	}
	if err := cfg.ValidateMining(); err != nil {
		t.Error(err)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"unknown key":     "wroker: rig1\n",
		"bad worker":      "worker: \"rig 1\"\n",
		"bad region":      "zpool:\n  region: mars\n",
		"empty datadir":   "data_dir: \"\"\n",
		"negative power":  "power:\n  price_per_kwh: -1\n",
		"bad fiat":        "power:\n  currency: euro\n",
		"short interval":  "switching:\n  interval: 5s\n",
		"bad duration":    "switching:\n  interval: soon\n",
		"short benchmark": "switching:\n  benchmark_time: 10s\n",
		"bad currency":    "zpool:\n  currency: \"BTC,c=X\"\n",
	}
	for name, content := range cases {
		if _, err := Load(write(t, content)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestValidateMiningNeedsWallet(t *testing.T) {
	err := Default().ValidateMining()
	if err == nil || !strings.Contains(err.Error(), "wallet") {
		t.Errorf("err = %v", err)
	}
}

func TestCurrencyIsNormalised(t *testing.T) {
	cfg, err := Load(write(t, "zpool:\n  wallet: \" addr \"\n  currency: ltc\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Zpool.Currency != "LTC" || cfg.Zpool.Wallet != "addr" {
		t.Errorf("zpool = %+v", cfg.Zpool)
	}
}

func TestCPUAndAPI(t *testing.T) {
	cfg := Default()
	if cfg.CPU.Enabled || cfg.API.Listen != "127.0.0.1:4000" {
		t.Errorf("defaults: cpu %+v, api %+v", cfg.CPU, cfg.API)
	}
	cfg, err := Load(write(t, "cpu: {enabled: true, watts: 125, threads: 16}\napi: {listen: \"\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CPU != (CPU{Enabled: true, Watts: 125, Threads: 16}) || cfg.API.Listen != "" {
		t.Errorf("cpu %+v, api %+v", cfg.CPU, cfg.API)
	}
	for name, content := range map[string]string{
		"negative watts": "cpu: {watts: -1}",
		"no port":        "api: {listen: localhost}",
	} {
		if _, err := Load(write(t, content)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestSwitchingAndPower(t *testing.T) {
	cfg, err := Load(write(t, "power:\n  price_per_kwh: 0.32\n  currency: usd\n"+
		"switching:\n  interval: 90s\n  min_run_time: 10m\n  hysteresis_percent: 3\n  mine_at_loss: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Power.PricePerKWh != 0.32 || cfg.Power.Currency != "USD" {
		t.Errorf("power = %+v", cfg.Power)
	}
	want := Switching{Interval: 90 * time.Second, HysteresisPercent: 3, MinRunTime: 10 * time.Minute, BenchmarkTime: 3 * time.Minute, MineAtLoss: true}
	if cfg.Switching != want {
		t.Errorf("switching = %+v, want %+v", cfg.Switching, want)
	}
}
