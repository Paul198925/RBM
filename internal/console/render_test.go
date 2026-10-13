package console

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hashsteer/internal/api"
	"hashsteer/internal/device"
	"hashsteer/internal/engine"
	"hashsteer/internal/pool"
	"hashsteer/internal/wallet"
)

func sample() Snapshot {
	now := time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)
	since := now.Add(-83 * time.Minute)
	started := now.Add(-26 * time.Hour)
	log := make([]string, 60)
	for i := range log {
		log[i] = "13:59:00 log line " + strings.Repeat("x", i)
	}
	return Snapshot{
		Version: "0.5.0", Worker: "rig1", URL: "http://127.0.0.1:4000/", Now: now, Keys: true,
		System: api.System{Started: started, Notices: []string{"nvidia-smi was not found"}},
		Update: api.UpdateInfo{Available: true, Latest: "0.6.0"},
		Status: engine.Status{BTCPrice: 100000, Currency: "EUR", BenchmarkSeconds: 180, Groups: []engine.GroupStatus{
			{Name: "RTX 3060", State: "mining", Miner: "rigel", Algorithm: "kawpow", Pool: "zpool", Running: "a",
				Since: &since, Hashrate: 23.4e6, Power: 118, Accepted: 41, Rejected: 1, ProfitBTC: 0.0000123,
				Candidates: []engine.CandidateStatus{{ID: "a", Estimated: true}}},
			{Name: "RTX 2080 SUPER", State: "benchmarking", Miner: "wildrig", Algorithm: "x16rv2", Pool: "2miners", Coin: "RVN",
				Since: &since, Hashrate: 31.2e6, Power: 205},
			{Name: "CPU", State: "idle", Reason: "nothing pays more than the power costs"},
		}},
		Devices: []api.DeviceStatus{
			{Device: device.Device{ID: "GPU#00", Model: "RTX 3060"}, Telemetry: &device.Telemetry{TemperatureC: 61, FanPercent: 55, PowerWatts: 118, UtilizationPercent: 100}},
			{Device: device.Device{ID: "GPU#01", Model: "RTX 2080 SUPER"}, Telemetry: &device.Telemetry{TemperatureC: 84, FanPercent: 90, PowerWatts: 205, UtilizationPercent: 99}},
			{Device: device.Device{ID: "CPU#00", Model: "Ryzen 5"}},
		},
		Balances: api.BalanceInfo{Balances: []pool.Balance{{Pool: "zpool", Currency: "BTC", Balance: 0.0001, Unpaid: 0.00012, ValueBTC: 0.00012, Paid24h: 0}},
			Wallets: []wallet.Balance{{Currency: "RVN", Address: "RDemoWalletAddressForTheInterface01", Balance: 1840.5, Pending: 56.68, ValueBTC: 0.0004}}},
		Benchmarks: []engine.BenchmarkStatus{
			{Group: "RTX 3060", Status: engine.BenchPending},
			{Group: "RTX 3060", Status: engine.BenchPending},
			{Group: "RTX 2080 SUPER", Status: engine.BenchRunning, MeasuredSeconds: 60},
			{Group: "RTX 2080 SUPER", Status: engine.BenchOK, MeasuredSeconds: 180},
		},
		Log: log,
	}
}

func TestRenderShowsWhatMatters(t *testing.T) {
	lines := Render(sample(), 120, 44, false)
	text := strings.Join(lines, "\n")
	if os.Getenv("HASHSTEER_SHOW_CONSOLE") != "" {
		t.Log("\n" + text)
	}
	for _, want := range []string{
		"hashsteer 0.5.0   worker rig1   up 1d 2h",
		"web interface: http://127.0.0.1:4000/",
		"hashsteer 0.6.0 is available",
		"nvidia-smi was not found",
		"Profit 0.00001230 BTC/day = 1.23 EUR/day   Power 323 W   1 BTC = 100000 EUR",
		"23.40 MH/s", "0.00001230 est.", "41/1", "1h 23m", "pool picks",
		"benchmarking", "RVN",
		"CPU: idle - nothing pays more than the power costs",
		"3 benchmarks are open, about 10m to go.",
		"84 °C", "zpool  0.00012 BTC  12.00 EUR  0.0001 BTC",
		"RVN   RDemoWa...face01  1840.5 RVN  40.00 EUR    56.68 RVN",
		"[P] pause   [Q] quit",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the dashboard lacks %q", want)
		}
	}
	if strings.Contains(text, "CPU#00") {
		t.Error("a device without readings is listed")
	}
	if strings.Contains(text, "\x1b") {
		t.Error("colour codes although none were asked for")
	}
	if len(lines) != 43 {
		t.Errorf("%d lines, want the window of 44 filled but for the last line", len(lines))
	}
	// the log is cut from the front: the newest lines stay
	if !strings.Contains(text, "log line "+strings.Repeat("x", 59)[:20]) || !strings.HasPrefix(lines[len(lines)-3], "13:59:00 log line xxxxx") {
		t.Errorf("the newest log line is not the last before the keys: %q", lines[len(lines)-3])
	}
}

func TestRenderFitsTheWindow(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 25}, {60, 12}, {40, 6}, {200, 80}, {10, 3}} {
		for _, colour := range []bool{false, true} {
			lines := Render(sample(), size[0], size[1], colour)
			if size[1] > 4 && len(lines) > size[1]-1 {
				t.Errorf("%v: %d lines", size, len(lines))
			}
			for _, line := range lines {
				if n := utf8.RuneCountInString(strip(line)); n > max(size[0], 40)-1 {
					t.Errorf("%v: a line of %d characters: %q", size, n, line)
				}
				if colour && strings.Contains(line, "\x1b") && !strings.HasSuffix(line, reset) {
					t.Errorf("%v: a coloured line does not end its colour: %q", size, line)
				}
			}
		}
	}
}

func TestRenderPausedAndWaiting(t *testing.T) {
	s := sample()
	s.System.Paused = true
	s.Waiting = "no wallet is set"
	s.Status.Groups = nil
	s.Update = api.UpdateInfo{Downloaded: "0.6.0", Available: true, Latest: "0.6.0"}
	text := strings.Join(Render(s, 120, 40, false), "\n")
	for _, want := range []string{"Not mining: no wallet is set", "Mining is paused.", "[P] resume", "0.6.0 is fetched", "none"} {
		if !strings.Contains(text, want) {
			t.Errorf("the dashboard lacks %q", want)
		}
	}
	if strings.Contains(text, "is available") {
		t.Error("an update that is fetched is still offered")
	}
}

// strip takes the colour codes out of a line.
func strip(line string) string {
	var b strings.Builder
	inCode := false
	for _, r := range line {
		switch {
		case inCode:
			inCode = r != 'm'
		case r == '\x1b':
			inCode = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
