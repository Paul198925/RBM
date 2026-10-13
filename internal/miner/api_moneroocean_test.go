package miner

import (
	"context"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The pool's figures as they are read here: Monero, two coins of one
// algorithm of which the better counts, one that is switched off and one
// that pays nothing.
const moneroOceanCoinsDoc = `{"pool_statistics": {"coins": {
	"18081": {"symbol": "XMR", "algo": "rx/0", "active": true, "profit": 8e-08},
	"9998": {"symbol": "RTM", "algo": "ghostrider", "active": true, "profit": 4e-07},
	"10225": {"symbol": "OTHER", "algo": "ghostrider", "active": true, "profit": 1e-07},
	"11812": {"symbol": "XLA", "algo": "panthera", "active": false, "profit": 9e-05},
	"5110": {"symbol": "FLEX", "algo": "flex", "active": true, "profit": 0}}}}`

// stubs serves a miner's summary and the pool's figures, and points the
// adapter at them.
func moneroOceanStubs(t *testing.T, summary *string, pool *string) int {
	t.Helper()
	minerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/summary" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, *summary)
	}))
	t.Cleanup(minerSrv.Close)
	poolSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *pool == "" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, *pool)
	}))
	t.Cleanup(poolSrv.Close)

	oldURL, oldRates := moneroOceanStatsURL, moneroOceanWorth
	moneroOceanStatsURL, moneroOceanWorth = poolSrv.URL, &moneroOceanRates{}
	t.Cleanup(func() { moneroOceanStatsURL, moneroOceanWorth = oldURL, oldRates })

	_, port, _ := net.SplitHostPort(minerSrv.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	return n
}

func TestMoneroOceanHashrateIsToldInMoneroHashes(t *testing.T) {
	summary, pool := "", moneroOceanCoinsDoc
	port := moneroOceanStubs(t, &summary, &pool)
	api, err := NewAPI("xmrig-mo")
	if err != nil {
		t.Fatal(err)
	}
	poll := func() AlgoSample {
		t.Helper()
		s, err := api.Poll(context.Background(), port)
		if err != nil {
			t.Fatal(err)
		}
		return s.For("moneroocean")
	}

	// mining Monero itself: a hash is a hash
	summary = `{"algo": "rx/0", "hashrate": {"total": [6000, 5900, null]}, "connection": {"uptime": 120, "algo": "rx/0"},
		"results": {"shares_good": 7, "shares_total": 8}}`
	if s := poll(); s.Hashrate != 6000 || s.Accepted != 7 || s.Rejected != 1 {
		t.Errorf("on Monero: %+v", s)
	}
	// switched to another algorithm: far fewer hashes, each worth five of
	// Monero's by the better of the two coins
	summary = `{"algo": "ghostrider", "hashrate": {"total": [null, 2000, null]}, "connection": {"uptime": 300, "algo": "ghostrider"},
		"results": {"shares_good": 9, "shares_total": 9}}`
	if s := poll(); math.Abs(s.Hashrate-10000) > 1e-6 || s.Accepted != 9 {
		t.Errorf("on ghostrider: %+v", s)
	}
	// while it measures itself it is not at the pool: that earns nothing
	summary = `{"algo": "ghostrider", "hashrate": {"total": [2500, null, null]}, "connection": {"uptime": 0, "algo": null}, "results": {}}`
	if s := poll(); s.Hashrate != 0 {
		t.Errorf("while it measures: %+v", s)
	}
	// algorithms the pool has no paying coin for
	for _, algo := range []string{"panthera", "flex", "cn/unheard-of"} {
		summary = `{"hashrate": {"total": [1000]}, "connection": {"uptime": 5, "algo": "` + algo + `"}, "results": {}}`
		if s := poll(); s.Hashrate != 0 {
			t.Errorf("on %s: %+v", algo, s)
		}
	}
}

func TestMoneroOceanWithoutThePoolsFigures(t *testing.T) {
	summary, pool := "", ""
	port := moneroOceanStubs(t, &summary, &pool)
	api, _ := NewAPI("xmrig-mo")

	// Monero's own algorithm needs no figures
	summary = `{"hashrate": {"total": [6000]}, "connection": {"uptime": 60, "algo": "rx/0"}, "results": {}}`
	s, err := api.Poll(context.Background(), port)
	if err != nil || s.For("moneroocean").Hashrate != 6000 {
		t.Errorf("on Monero without figures: %+v, %v", s, err)
	}
	// another one cannot be told: an error, not a hashrate of zero
	summary = `{"hashrate": {"total": [2000]}, "connection": {"uptime": 60, "algo": "ghostrider"}, "results": {}}`
	if _, err := api.Poll(context.Background(), port); err == nil {
		t.Error("a hashrate nobody can price was reported")
	}
	// figures that were read once are used on when the pool goes quiet
	pool = moneroOceanCoinsDoc
	moneroOceanWorth = &moneroOceanRates{}
	if s, err := api.Poll(context.Background(), port); err != nil || s.For("moneroocean").Hashrate != 10000 {
		t.Fatalf("with figures: %+v, %v", s, err)
	}
	pool = ""
	moneroOceanWorth.fetched = moneroOceanWorth.fetched.Add(-2 * moneroOceanStatsFor)
	if s, err := api.Poll(context.Background(), port); err != nil || s.For("moneroocean").Hashrate != 10000 {
		t.Errorf("with old figures: %+v, %v", s, err)
	}
}

func TestSeedFilesAreWrittenOnce(t *testing.T) {
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	def := defs["xmrig-mo"]
	if def == nil || len(def.SeedFiles) != 1 {
		t.Fatalf("xmrig-mo = %+v", def)
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "xmrig.exe")
	if err := def.Seed(exe); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hashsteer-mo.json")
	if got, _ := os.ReadFile(path); string(got) != `{"autosave": true}` {
		t.Fatalf("seed file holds %q", got)
	}
	// what the miner wrote into it since is left alone
	if err := os.WriteFile(path, []byte(`{"algo-perf": {"rx/0": 6000}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := def.Seed(exe); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != `{"algo-perf": {"rx/0": 6000}}` {
		t.Errorf("the miner's file was overwritten: %q", got)
	}
	// the file is named in the arguments, and the miner is started next to it
	args, err := def.BuildArgs(Job{Algorithm: def.Algorithms[0], Host: "gulf.moneroocean.stream", Port: 10128, User: "44wallet", Pass: "rig1", APIPort: 4567})
	if err != nil || args[0] != "-c" || args[1] != "hashsteer-mo.json" {
		t.Errorf("args = %v, %v", args, err)
	}
	for _, a := range args {
		if a == "-a" || a == "moneroocean" {
			t.Errorf("the miner is told an algorithm: %v", args)
		}
	}
	// a name that leaves the folder is refused
	bad := *def
	bad.SeedFiles = []SeedFile{{Name: "../escape.json"}}
	if bad.validate() == nil {
		t.Error("a seed file outside the folder was accepted")
	}
}
