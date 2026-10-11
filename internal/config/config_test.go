package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// dir writes the given files into a fresh configuration directory.
func dir(t *testing.T, files map[string]string) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestLoadDirAppliesDefaults(t *testing.T) {
	// no files at all is a valid configuration
	cfg, err := LoadDir(dir(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, mustValid(t, Default())) {
		t.Errorf("empty directory differs from the defaults: %+v", cfg)
	}
	z := cfg.Pools.Zpool
	if !z.Enabled || !z.AlgoSwitching || !z.CoinMining || z.Region != "eu" || z.Currency != "BTC" {
		t.Errorf("zpool defaults = %+v", z)
	}
	if cfg.General.CPU.Enabled || cfg.General.API.Listen != "127.0.0.1:4000" {
		t.Errorf("general defaults = %+v", cfg.General)
	}
	if err := cfg.ReadyToMine(); err == nil || !strings.Contains(err.Error(), "wallet") {
		t.Errorf("ready without a wallet: %v", err)
	}
}

func mustValid(t *testing.T, cfg *Config) *Config {
	t.Helper()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLoadDirReadsEveryFile(t *testing.T) {
	cfg, err := LoadDir(dir(t, map[string]string{
		FileGeneral: "worker: rig1\npower: {price_per_kwh: 0.32, currency: usd}\n" +
			"switching: {interval: 90s, min_run_time: 10m, hysteresis_percent: 3, mine_at_loss: true}\n" +
			"cpu: {enabled: true, watts: 125, threads: 16}\napi: {listen: \"\"}\n",
		FilePools:      "zpool:\n  wallet: \" addr1 \"\n  currency: ltc\n  algo_switching: false\n  coins: [rvn, \" clore\", RVN]\n  exclude_coins: [scc]\n",
		FileMiners:     "wildrig:\n  disabled: true\nrigel:\n  exclude_algorithms: [KawPow, sha512256d]\n  extra_args: [\"--no-strict-ssl\"]\n",
		FileAlgorithms: "exclude: [Sky-Doge]\nonly: []\n",
		FileDevices:    "exclude: [\"gpu#01\"]\n",
	}))
	if err != nil {
		t.Fatal(err)
	}

	g := cfg.General
	if g.Worker != "rig1" || g.Power.PricePerKWh != 0.32 || g.Power.Currency != "USD" || g.API.Listen != "" {
		t.Errorf("general = %+v", g)
	}
	wantSw := Switching{Interval: Duration(90 * time.Second), HysteresisPercent: 3, MinRunTime: Duration(10 * time.Minute),
		BenchmarkTime: Duration(3 * time.Minute), MineAtLoss: true}
	if g.Switching != wantSw || g.CPU != (CPU{Enabled: true, Watts: 125, Threads: 16}) {
		t.Errorf("switching = %+v, cpu = %+v", g.Switching, g.CPU)
	}

	// symbols come out upper case, sorted, without duplicates
	z := cfg.Pools.Zpool
	if z.Wallet != "addr1" || z.Currency != "LTC" || z.AlgoSwitching || !z.CoinMining || !z.Enabled {
		t.Errorf("zpool = %+v", z)
	}
	if !reflect.DeepEqual(z.Coins, []string{"CLORE", "RVN"}) || !reflect.DeepEqual(z.ExcludeCoins, []string{"SCC"}) {
		t.Errorf("coins = %v, excluded = %v", z.Coins, z.ExcludeCoins)
	}
	if err := cfg.ReadyToMine(); err != nil {
		t.Error(err)
	}

	if !cfg.Miners["wildrig"].Disabled || cfg.Miners["rigel"].Disabled {
		t.Errorf("miners = %+v", cfg.Miners)
	}
	rigel := cfg.Miners["rigel"]
	if !reflect.DeepEqual(rigel.ExcludeAlgorithms, []string{"kawpow", "sha512256d"}) || !reflect.DeepEqual(rigel.ExtraArgs, []string{"--no-strict-ssl"}) {
		t.Errorf("rigel = %+v", rigel)
	}
	if !reflect.DeepEqual(cfg.Algorithms.Exclude, []string{"skydoge"}) || !reflect.DeepEqual(cfg.Devices.Exclude, []string{"GPU#01"}) {
		t.Errorf("algorithms = %+v, devices = %+v", cfg.Algorithms, cfg.Devices)
	}
}

func TestLoadDirRejects(t *testing.T) {
	cases := map[string][2]string{
		"unknown key":     {FileGeneral, "wroker: rig1\n"},
		"bad worker":      {FileGeneral, "worker: \"rig 1\"\n"},
		"empty datadir":   {FileGeneral, "data_dir: \"\"\n"},
		"negative power":  {FileGeneral, "power: {price_per_kwh: -1}\n"},
		"bad fiat":        {FileGeneral, "power: {currency: euro}\n"},
		"short interval":  {FileGeneral, "switching: {interval: 5s}\n"},
		"bad duration":    {FileGeneral, "switching: {interval: soon}\n"},
		"short benchmark": {FileGeneral, "switching: {benchmark_time: 10s}\n"},
		"negative watts":  {FileGeneral, "cpu: {watts: -1}\n"},
		"no port":         {FileGeneral, "api: {listen: localhost}\n"},
		"bad region":      {FilePools, "zpool: {region: mars}\n"},
		"bad currency":    {FilePools, "zpool: {currency: \"BTC,c=X\"}\n"},
		"bad wallet":      {FilePools, "zpool: {wallet: \"addr,c=XYZ\"}\n"},
		"bad coin":        {FilePools, "zpool: {coins: [\"RVN,zap=X\"]}\n"},
		"unknown pool":    {FilePools, "nicehash: {enabled: true}\n"},
		"bad miner name":  {FileMiners, "\"Rigel Miner\": {disabled: true}\n"},
		"empty extra arg": {FileMiners, "rigel: {extra_args: [\" \"]}\n"},
		"bad device":      {FileDevices, "exclude: [\"GPU1\"]\n"},
	}
	for name, c := range cases {
		if _, err := LoadDir(dir(t, map[string]string{c[0]: c[1]})); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestReadyToMine(t *testing.T) {
	cfg := Default()
	cfg.Pools.Zpool.Wallet = "addr"
	if err := cfg.ReadyToMine(); err != nil {
		t.Error(err)
	}
	cfg.Pools.Zpool.AlgoSwitching, cfg.Pools.Zpool.CoinMining = false, false
	if err := cfg.ReadyToMine(); err == nil {
		t.Error("ready with nothing to mine")
	}
	cfg = Default()
	cfg.Pools.Zpool.Wallet, cfg.Pools.Zpool.Enabled = "addr", false
	if err := cfg.ReadyToMine(); err == nil {
		t.Error("ready without a pool")
	}
}

func TestSaveDirRoundTrip(t *testing.T) {
	d := filepath.Join(t.TempDir(), "new", "config")
	cfg := Default()
	cfg.General.Worker = "rig7"
	cfg.General.Switching.MinRunTime = Duration(90 * time.Second)
	cfg.General.API.Password = "s3cret"
	cfg.Pools.Zpool.Wallet = "addr1"
	cfg.Pools.Zpool.ExcludeCoins = []string{"scc"}
	cfg.Miners["wildrig"] = Miner{Disabled: true, ExtraArgs: []string{"--x", "1"}}
	cfg.Algorithms.Exclude = []string{"Skydoge"}
	cfg.Devices.Exclude = []string{"GPU#01"}

	if err := SaveDir(d, cfg); err != nil {
		t.Fatal(err)
	}
	if !Exists(d) {
		t.Fatal("no configuration after saving")
	}
	got, err := LoadDir(d)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("loaded %+v\nsaved  %+v", got, cfg)
	}

	// readable for a human: durations as text, a header on top
	general, _ := os.ReadFile(filepath.Join(d, FileGeneral))
	for _, want := range []string{"# General settings", "min_run_time: 1m30s", "interval: 1m\n", "benchmark_time: 3m\n"} {
		if !strings.Contains(string(general), want) {
			t.Errorf("%s misses %q:\n%s", FileGeneral, want, general)
		}
	}
	// no temporary files are left behind
	entries, _ := os.ReadDir(d)
	if len(entries) != 5 {
		t.Errorf("%d files in the directory", len(entries))
	}
}

func TestSaveDirRefusesInvalid(t *testing.T) {
	d := filepath.Join(t.TempDir(), "config")
	cfg := Default()
	cfg.General.Worker = "no good"
	if err := SaveDir(d, cfg); err == nil {
		t.Fatal("an invalid configuration was saved")
	}
	if Exists(d) {
		t.Error("files were written")
	}
}

func TestJSONFormForTheInterface(t *testing.T) {
	cfg := mustValid(t, Default())
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"interval":"1m"`, `"algo_switching":true`, `"exclude_coins":[]`, `"miners":{}`, `"exclude":[]`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("json misses %s: %s", want, data)
		}
	}
	var back Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mustValid(t, &back), cfg) {
		t.Errorf("json round trip: %+v", back)
	}
	if err := json.Unmarshal([]byte(`{"general":{"switching":{"interval":"soon"}}}`), &back); err == nil {
		t.Error("a bad duration was accepted")
	}
}

func TestMigrateLegacy(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "config.yaml")
	d := filepath.Join(root, "config")
	old := "worker: GoTest\nzpool:\n  wallet: RWallet1\n  currency: RVN\n  region: na\n" +
		"power:\n  price_per_kwh: 0.3\n  currency: EUR\ncpu:\n  enabled: true\n"
	if err := os.WriteFile(file, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateLegacy(file, d)
	if err != nil || !migrated {
		t.Fatalf("migrated=%v err=%v", migrated, err)
	}
	cfg, err := LoadDir(d)
	if err != nil {
		t.Fatal(err)
	}
	z := cfg.Pools.Zpool
	if cfg.General.Worker != "GoTest" || z.Wallet != "RWallet1" || z.Currency != "RVN" || z.Region != "na" {
		t.Errorf("general %+v, zpool %+v", cfg.General, z)
	}
	if cfg.General.Power.PricePerKWh != 0.3 || !cfg.General.CPU.Enabled || cfg.General.API.Listen != "127.0.0.1:4000" {
		t.Errorf("general = %+v", cfg.General)
	}
	// what the old file did not say keeps its default
	if cfg.General.Switching.Interval != Duration(time.Minute) || !z.CoinMining {
		t.Errorf("defaults lost: %+v", cfg.General.Switching)
	}
	// the old file is kept, under another name
	if _, err := os.Stat(file); err == nil {
		t.Error("the old file is still in place")
	}
	if data, _ := os.ReadFile(file + ".old"); string(data) != old {
		t.Error("the old file was not kept")
	}

	// nothing happens the second time, or without an old file
	if migrated, err := MigrateLegacy(file, d); migrated || err != nil {
		t.Errorf("second run: migrated=%v err=%v", migrated, err)
	}
	if migrated, err := MigrateLegacy(filepath.Join(root, "none.yaml"), filepath.Join(root, "other")); migrated || err != nil {
		t.Errorf("no old file: migrated=%v err=%v", migrated, err)
	}
}

func TestDurationText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0s", 90 * time.Second: "1m30s", 5 * time.Minute: "5m", time.Hour: "1h", 90 * time.Minute: "1h30m", 45 * time.Second: "45s",
	} {
		if got := Duration(d).String(); got != want {
			t.Errorf("%v prints as %q, want %q", d, got, want)
		}
		var back Duration
		if err := back.UnmarshalText([]byte(want)); err != nil || time.Duration(back) != d {
			t.Errorf("%q reads as %v, %v", want, back, err)
		}
	}
}
