package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"rbm/internal/device"
	"rbm/internal/engine"
)

func start(t *testing.T) *Server {
	t.Helper()
	since := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	status := func() engine.Status {
		return engine.Status{
			Time: since.Add(time.Minute),
			Groups: []engine.GroupStatus{{
				Name: "RTX3060", Devices: []string{"GPU#00"}, State: "mining", Reason: "most profitable",
				Miner: "rigel", Algorithm: "kawpow", Pool: "zpool", Since: &since,
				Hashrate: 25e6, Power: 120, Accepted: 7, ProfitBTC: 3e-6,
				Candidates: []engine.CandidateStatus{{ID: "hashrate/rigel/GPU#00/kawpow", Miner: "rigel", Algorithm: "kawpow", Benchmarked: true}},
				Disabled:   []engine.DisabledStatus{},
			}},
		}
	}
	devices := []device.Device{{ID: "GPU#00", Kind: device.GPU, Vendor: "NVIDIA", Model: "GeForce RTX 3060", MemoryMiB: 12288}}
	srv, err := Start("127.0.0.1:0", Handler(status, devices))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func get(t *testing.T, srv *Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get("http://" + srv.Addr() + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestStatus(t *testing.T) {
	srv := start(t)
	code, body := get(t, srv, "/api/status")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	var status struct {
		Groups []struct {
			Name       string  `json:"name"`
			State      string  `json:"state"`
			Algorithm  string  `json:"algorithm"`
			Hashrate   float64 `json:"hashrate"`
			Power      float64 `json:"power_watts"`
			Profit     float64 `json:"profit_btc_per_day"`
			Candidates []struct {
				ID string `json:"id"`
			} `json:"candidates"`
			Disabled []any `json:"disabled"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("%v in %s", err, body)
	}
	if len(status.Groups) != 1 {
		t.Fatalf("groups: %s", body)
	}
	g := status.Groups[0]
	if g.Name != "RTX3060" || g.State != "mining" || g.Algorithm != "kawpow" || g.Hashrate != 25e6 || g.Power != 120 || g.Profit != 3e-6 {
		t.Errorf("group = %+v", g)
	}
	if len(g.Candidates) != 1 || g.Disabled == nil {
		t.Errorf("lists: %s", body)
	}
}

func TestDevicesAndIndex(t *testing.T) {
	srv := start(t)
	code, body := get(t, srv, "/api/devices")
	if code != http.StatusOK || !strings.Contains(body, `"id": "GPU#00"`) || !strings.Contains(body, `"memory_mib": 12288`) {
		t.Errorf("devices %d: %s", code, body)
	}
	if code, body := get(t, srv, "/"); code != http.StatusOK || !strings.Contains(body, "/api/status") {
		t.Errorf("index %d: %s", code, body)
	}
}

func TestOnlyKnownRoutesAndReads(t *testing.T) {
	srv := start(t)
	if code, _ := get(t, srv, "/api/nothing"); code != http.StatusNotFound {
		t.Errorf("unknown path: %d", code)
	}
	resp, err := http.Post("http://"+srv.Addr()+"/api/status", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}
}

func TestStartReportsABusyPort(t *testing.T) {
	srv := start(t)
	if _, err := Start(srv.Addr(), http.NotFoundHandler()); err == nil {
		t.Error("no error for a port that is in use")
	}
}
