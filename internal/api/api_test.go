package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rbm/internal/config"
	"rbm/internal/device"
	"rbm/internal/engine"
	"rbm/internal/pool"
	"rbm/internal/stats"
	"rbm/internal/sysinfo"
)

// fakeBackend records what the API asks of the application.
type fakeBackend struct {
	cfg            config.Config
	saved          *config.Config
	saveErr        error
	waiting        string
	paused         bool
	watchdogResets int
	reset          []string
	enabled        []string
}

func newBackend() *fakeBackend {
	cfg := config.Default()
	cfg.Pools.Zpool.Wallet = "wallet1"
	cfg.Validate()
	return &fakeBackend{cfg: *cfg}
}

func (f *fakeBackend) Waiting() string { return f.waiting }

func (f *fakeBackend) Status() engine.Status {
	since := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	return engine.Status{Time: since.Add(time.Minute), Groups: []engine.GroupStatus{{
		Name: "RTX3060", Devices: []string{"GPU#00"}, State: "mining", Miner: "rigel", Algorithm: "kawpow",
		Pool: "zpool", Coin: "CLORE", Since: &since, Hashrate: 25e6, Power: 120, ProfitBTC: 3e-6,
		Candidates: []engine.CandidateStatus{{ID: "hashrate/rigel/GPU#00/kawpow@zpool:CLORE", HashKey: "hashrate/rigel/GPU#00/kawpow", Benchmarked: true}},
		Disabled:   []engine.DisabledStatus{},
	}}}
}

func (f *fakeBackend) Devices() []DeviceStatus {
	return []DeviceStatus{
		{Device: device.Device{ID: "GPU#00", Kind: device.GPU, Vendor: "NVIDIA", Model: "GeForce RTX 3060", MemoryMiB: 12288},
			Telemetry: &device.Telemetry{TemperatureC: 61, FanPercent: 73, PowerWatts: 170, PowerLimitWatts: 170, ClockMHz: 1717, MemoryClockMHz: 7301}, MaxTemperatureC: 78},
		{Device: device.Device{ID: "CPU#00", Kind: device.CPU, Model: "Intel(R) Core(TM) i9-10900K", Threads: 20}},
	}
}

func (f *fakeBackend) System() System {
	return System{Started: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC), Paused: f.paused,
		Memory: &sysinfo.Memory{TotalMiB: 32000, FreeMiB: 7700, CommitLimitMiB: 54000, CommitFreeMiB: 11000}}
}

func (f *fakeBackend) SetPaused(paused bool) { f.paused = paused }

func (f *fakeBackend) Balances() BalanceInfo {
	return BalanceInfo{Balances: []pool.Balance{{Pool: "zpool", Currency: "RVN", Balance: 152.09, Unsold: 12.5, Unpaid: 164.59, Paid24h: 217.2, Total: 727.8,
		Payouts: []pool.Payout{{Time: time.Date(2026, 10, 11, 5, 34, 19, 0, time.UTC), Amount: 56.68336412, Tx: "7c66"}}}}}
}

func (f *fakeBackend) Activity() ([]stats.Run, error) {
	start := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	return []stats.Run{
		{ID: 2, Group: "RTX3060", Miner: "ccminer-tpfuemp", Algorithm: "rinhash", Pool: "zpool", Coin: "RIN", Started: start.Add(time.Hour), Ended: start.Add(2 * time.Hour),
			End: stats.EndSwitched, Hashrate: 1.2e6, EarnedBTC: 2.04e-5, EnergyKWh: 0.193, Accepted: 410, Rejected: 1},
		{ID: 1, Group: "RTX3060", Miner: "rigel", Algorithm: "kawpow", Pool: "zpool", Started: start, Ended: start.Add(3 * time.Minute),
			Benchmark: true, End: stats.EndCrashed, Hashrate: 19.9e6, EarnedBTC: 1e-8, EnergyKWh: 0.008},
	}, nil
}

func (f *fakeBackend) Offers() []pool.Offer {
	return []pool.Offer{{Pool: "zpool", Algorithm: "kawpow", Coin: "CLORE", CoinName: "Clore", Price: 1.1e-13, LastBlockSeconds: 396, Blocks24h: 211,
		User: "wallet1", Pass: "rig1,c=BTC,zap=CLORE", Host: "kawpow.eu.mine.zpool.ca", Port: 1325}}
}

func (f *fakeBackend) Config() config.Config { return f.cfg }

func (f *fakeBackend) SaveConfig(cfg config.Config) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	f.saved, f.cfg = &cfg, cfg
	return nil
}

func (f *fakeBackend) Meta(ctx context.Context) Meta {
	return Meta{Version: "test", ConfigDir: "config", Regions: config.ZpoolRegions,
		Miners:           []MinerInfo{{Name: "rigel", Version: "1.23.2", Vendor: "NVIDIA", Algorithms: []string{"kawpow"}}},
		Currencies:       []pool.PayoutCurrency{{Symbol: "BTC", Name: "Bitcoin"}},
		TwoMinersRegions: config.TwoMinersRegions, TwoMinersCoins: pool.TwoMinersCoins()}
}

func (f *fakeBackend) ResetBenchmarks(which string, hashKeys []string) {
	if which != "" {
		f.reset = append(f.reset, "class:"+which)
	}
	f.reset = append(f.reset, hashKeys...)
}

func (f *fakeBackend) Enable(hashKey string) { f.enabled = append(f.enabled, hashKey) }
func (f *fakeBackend) ResetWatchdog()        { f.watchdogResets++ }

func (f *fakeBackend) Totals() ([]stats.Total, error) {
	return []stats.Total{{Pool: "zpool", Runs: 42, EarnedBTC: 0.00012819, EnergyKWh: 4.616, Minutes: 3052.77,
		Started: time.Date(2026, 10, 10, 5, 0, 50, 0, time.UTC), Updated: time.Date(2026, 10, 11, 8, 41, 35, 0, time.UTC)}}, nil
}

func (f *fakeBackend) Benchmarks() []engine.BenchmarkStatus {
	updated := time.Date(2026, 10, 11, 10, 30, 0, 0, time.UTC)
	zero, some := 0.0, 2.5
	until := time.Now().Add(5 * time.Hour)
	entry := func(miner, version, algorithm, group, status string, hashrate, power float64) engine.BenchmarkStatus {
		b := engine.BenchmarkStatus{HashKey: "hashrate/" + miner + "/" + group + "/" + algorithm, Group: group, Devices: []string{"GPU#00"},
			Miner: miner, Version: version, Algorithm: algorithm, Status: status, Hashrate: hashrate, PowerWatts: power}
		if hashrate > 0 {
			b.MeasuredSeconds, b.Updated, b.RejectedPercent = 213, &updated, &zero
			b.Log = miner + "-" + algorithm + "-" + group + "-20261011-103000.log"
		}
		return b
	}
	list := []engine.BenchmarkStatus{
		entry("rigel", "1.23.2", "kawpow", "RTX3060", engine.BenchOK, 19.8e6, 156),
		entry("rigel", "1.23.2", "sha512256d", "RTX3060", engine.BenchOK, 559.6e6, 150),
		entry("wildrig", "0.51.4", "anime", "RTX3060", engine.BenchOutdated, 28.4e6, 160),
		entry("wildrig", "0.51.4", "curve", "RTX3060", engine.BenchRunning, 0, 0),
		entry("wildrig", "0.51.4", "firopow", "RTX3060", engine.BenchFailed, 0, 0),
		entry("ccminer-tpfuemp", "2026.09.2", "rinhash", "RTX3060", engine.BenchPending, 0, 0),
		entry("srbminer", "3.7.5", "phihash", "RTX3060", engine.BenchNoOffer, 0, 0),
		entry("gminer", "3.44", "kawpow", "RTX2080SUPER", engine.BenchExcluded, 0, 0),
	}
	list[2].MeasuredVersion, list[2].RejectedPercent = "0.50.9", &some
	list[3].MeasuredSeconds = 32
	list[4].FailedUntil = &until
	return list
}
func (f *fakeBackend) Log() []string { return []string{"12:00:00 started"} }

func (f *fakeBackend) MinerLogs() ([]LogFile, error) {
	return []LogFile{{Name: "rigel-kawpow.log", Size: 2048}}, nil
}

func (f *fakeBackend) MinerLog(name string) (string, error) {
	switch name {
	case "rigel-kawpow.log":
		return "GPU#0 solution accepted\n", nil
	case "bad name":
		return "", errors.New("not a log file name")
	}
	return "", fs.ErrNotExist
}

// request is one call to the interface as it would come from a browser on
// the same machine, unless the fields say otherwise.
type request struct {
	method, path, body string
	remote             string // default: this machine
	host               string // default: 127.0.0.1:4000
	origin             string
	noHeader           bool // leave out the header that marks a change
	password           string
}

func do(b Backend, rq request) *httptest.ResponseRecorder {
	if rq.method == "" {
		rq.method = http.MethodGet
	}
	r := httptest.NewRequest(rq.method, rq.path, strings.NewReader(rq.body))
	r.RemoteAddr = "127.0.0.1:50000"
	if rq.remote != "" {
		r.RemoteAddr = rq.remote
	}
	r.Host = "127.0.0.1:4000"
	if rq.host != "" {
		r.Host = rq.host
	}
	if rq.origin != "" {
		r.Header.Set("Origin", rq.origin)
	}
	if rq.method != http.MethodGet && !rq.noHeader {
		r.Header.Set("X-RBM-Request", "1")
	}
	if rq.password != "" {
		r.SetBasicAuth("anyone", rq.password)
	}
	w := httptest.NewRecorder()
	Handler(b).ServeHTTP(w, r)
	return w
}

func TestStatus(t *testing.T) {
	b := newBackend()
	b.waiting = "the zpool wallet address is not set"
	w := do(b, request{path: "/api/status"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var status struct {
		Waiting string `json:"waiting"`
		Groups  []struct {
			Name, State, Coin string
			Hashrate          float64
			Candidates        []struct {
				HashKey string `json:"hash_key"`
			}
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("%v in %s", err, w.Body)
	}
	if status.Waiting == "" || len(status.Groups) != 1 {
		t.Fatalf("status = %s", w.Body)
	}
	g := status.Groups[0]
	if g.Name != "RTX3060" || g.State != "mining" || g.Coin != "CLORE" || g.Hashrate != 25e6 || g.Candidates[0].HashKey != "hashrate/rigel/GPU#00/kawpow" {
		t.Errorf("group = %+v", g)
	}
}

func TestReadEndpoints(t *testing.T) {
	b := newBackend()
	for path, want := range map[string]string{
		"/api/devices":                    `"id":"GPU#00"`,
		"/api/offers":                     `"coin":"CLORE"`,
		"/api/log":                        `12:00:00 started`,
		"/api/minerlogs":                  `"name":"rigel-kawpow.log"`,
		"/api/minerlogs/rigel-kawpow.log": `solution accepted`,
		"/":                               `<title>rbm</title>`,
		"/app.js":                         `refreshStatus`,
		"/style.css":                      `--accent`,
	} {
		w := do(b, request{path: path})
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d, %q not in %.200s", path, w.Code, want, w.Body)
		}
	}
	// what a miner is told to log in with stays inside
	if body := do(b, request{path: "/api/offers"}).Body.String(); strings.Contains(body, "wallet1") || strings.Contains(body, "zap=") {
		t.Errorf("offers leak the login: %s", body)
	}
	for path, want := range map[string]int{
		"/api/minerlogs/other.log": http.StatusNotFound,
		"/api/minerlogs/bad name":  http.StatusBadRequest,
		"/api/nothing":             http.StatusNotFound,
		"/nothing.html":            http.StatusNotFound,
	} {
		if w := do(b, request{path: strings.ReplaceAll(path, " ", "%20")}); w.Code != want {
			t.Errorf("%s: %d, want %d", path, w.Code, want)
		}
	}
}

func TestConfigReadHidesThePassword(t *testing.T) {
	b := newBackend()
	b.cfg.General.API.Password = "s3cret"
	w := do(b, request{path: "/api/config", password: "s3cret"})
	body := w.Body.String()
	if w.Code != http.StatusOK || strings.Contains(body, "s3cret") || !strings.Contains(body, `"password":"********"`) {
		t.Errorf("%d: %s", w.Code, body)
	}
	for _, want := range []string{`"wallet":"wallet1"`, `"zpool_regions":["na","eu","sea","jp"]`, `"name":"rigel"`, `"symbol":"BTC"`} {
		if !strings.Contains(body, want) {
			t.Errorf("config misses %s: %s", want, body)
		}
	}
}

func TestConfigSave(t *testing.T) {
	b := newBackend()
	b.cfg.General.API.Password = "s3cret"

	// a partial document changes only what it names; the mask keeps the password
	body := `{"general":{"worker":"rig9","api":{"listen":"127.0.0.1:4000","password":"********"}},
		"pools":{"zpool":{"currency":"ltc","exclude_coins":["scc"]}},
		"miners":{"wildrig":{"disabled":true,"exclude_algorithms":[],"extra_args":[]}}}`
	w := do(b, request{method: http.MethodPut, path: "/api/config", body: body, password: "s3cret"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("%d: %s", w.Code, w.Body)
	}
	got := b.saved
	if got.General.Worker != "rig9" || got.General.API.Password != "s3cret" || got.Pools.Zpool.Wallet != "wallet1" {
		t.Errorf("general %+v, zpool %+v", got.General, got.Pools.Zpool)
	}
	if got.Pools.Zpool.Currency != "LTC" || len(got.Pools.Zpool.ExcludeCoins) != 1 || !got.Miners["wildrig"].Disabled {
		t.Errorf("zpool %+v, miners %+v", got.Pools.Zpool, got.Miners)
	}

	// an empty password clears it, a new one replaces it
	w = do(b, request{method: http.MethodPut, path: "/api/config", body: `{"general":{"api":{"password":""}}}`, password: "s3cret"})
	if w.Code != http.StatusNoContent || b.saved.General.API.Password != "" {
		t.Errorf("clearing: %d, password %q", w.Code, b.saved.General.API.Password)
	}
}

func TestConfigSaveRejects(t *testing.T) {
	b := newBackend()
	for name, body := range map[string]string{
		"invalid value": `{"general":{"worker":"no good"}}`,
		"unknown field": `{"general":{"wroker":"rig1"}}`,
		"not json":      `worker: rig1`,
		"bad duration":  `{"general":{"switching":{"interval":"soon"}}}`,
	} {
		w := do(b, request{method: http.MethodPut, path: "/api/config", body: body})
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error"`) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if b.saved != nil {
		t.Error("an invalid configuration was saved")
	}
	b.saveErr = errors.New("unknown miner \"nope\"")
	if w := do(b, request{method: http.MethodPut, path: "/api/config", body: `{}`}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unknown miner") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestBenchmarks(t *testing.T) {
	b := newBackend()
	body := do(b, request{path: "/api/benchmarks"}).Body.String()
	for _, want := range []string{`"status":"outdated"`, `"measured_version":"0.50.9"`, `"rejected_percent":2.5`, `"status":"no offer"`,
		`"log":"rigel-kawpow-RTX3060-20261011-103000.log"`, `"failed_until":"`} {
		if !strings.Contains(body, want) {
			t.Errorf("benchmarks miss %s: %.300s", want, body)
		}
	}

	post := func(body string) int {
		return do(b, request{method: http.MethodPost, path: "/api/benchmarks/reset", body: body}).Code
	}
	if code := post(`{"hash_keys":["a","b"]}`); code != http.StatusNoContent {
		t.Errorf("several keys: %d", code)
	}
	for _, which := range []string{"failed", "outdated", "all"} {
		if code := post(`{"which":"` + which + `"}`); code != http.StatusNoContent {
			t.Errorf("class %s: %d", which, code)
		}
	}
	if got := strings.Join(b.reset, " "); got != "a b class:failed class:outdated class:all" {
		t.Errorf("resets = %q", got)
	}
	for name, body := range map[string]string{
		"nothing":       `{}`,
		"unknown class": `{"which":"everything"}`,
		"both":          `{"which":"all","hash_key":"a"}`,
	} {
		if code := post(body); code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, code)
		}
	}
}

func TestActions(t *testing.T) {
	b := newBackend()
	key := `{"hash_key":"hashrate/rigel/GPU#00/kawpow"}`
	if w := do(b, request{method: http.MethodPost, path: "/api/benchmarks/reset", body: key}); w.Code != http.StatusNoContent {
		t.Errorf("reset: %d %s", w.Code, w.Body)
	}
	if w := do(b, request{method: http.MethodPost, path: "/api/candidates/enable", body: key}); w.Code != http.StatusNoContent {
		t.Errorf("enable: %d %s", w.Code, w.Body)
	}
	if len(b.reset) != 1 || len(b.enabled) != 1 || b.reset[0] != "hashrate/rigel/GPU#00/kawpow" {
		t.Errorf("reset %v, enabled %v", b.reset, b.enabled)
	}
	if w := do(b, request{method: http.MethodPost, path: "/api/benchmarks/reset", body: `{}`}); w.Code != http.StatusBadRequest {
		t.Errorf("empty key: %d", w.Code)
	}
	if w := do(b, request{method: http.MethodGet, path: "/api/benchmarks/reset"}); w.Code == http.StatusNoContent {
		t.Error("an action ran on GET")
	}
}

func TestSystemPauseBalancesActivity(t *testing.T) {
	b := newBackend()
	// the status carries what the header of the interface shows
	body := do(b, request{path: "/api/status"}).Body.String()
	for _, want := range []string{`"started":"2026-10-11T09:00:00Z"`, `"paused":false`, `"commit_free_mib":11000`} {
		if !strings.Contains(body, want) {
			t.Errorf("status misses %s: %s", want, body)
		}
	}
	body = do(b, request{path: "/api/devices"}).Body.String()
	for _, want := range []string{`"temperature_c":61`, `"max_temperature_c":78`, `"id":"CPU#00"`} {
		if !strings.Contains(body, want) {
			t.Errorf("devices miss %s: %s", want, body)
		}
	}

	if w := do(b, request{method: http.MethodPost, path: "/api/pause", body: `{"paused":true}`}); w.Code != http.StatusNoContent || !b.paused {
		t.Errorf("pause: %d, paused %v", w.Code, b.paused)
	}
	if w := do(b, request{method: http.MethodPost, path: "/api/pause", body: `{"paused":false}`}); w.Code != http.StatusNoContent || b.paused {
		t.Errorf("resume: %d, paused %v", w.Code, b.paused)
	}
	if w := do(b, request{method: http.MethodPost, path: "/api/pause", body: `{}`}); w.Code != http.StatusBadRequest {
		t.Errorf("pause without a value: %d", w.Code)
	}

	body = do(b, request{path: "/api/balances"}).Body.String()
	for _, want := range []string{`"currency":"RVN"`, `"unpaid":164.59`, `"tx":"7c66"`} {
		if !strings.Contains(body, want) {
			t.Errorf("balances miss %s: %s", want, body)
		}
	}

	body = do(b, request{path: "/api/activity"}).Body.String()
	for _, want := range []string{`"algorithm":"rinhash"`, `"end":"crashed"`, `"earned_btc":0.0000204`} {
		if !strings.Contains(body, want) {
			t.Errorf("activity misses %s: %s", want, body)
		}
	}
	w := do(b, request{path: "/api/activity.csv"})
	lines := strings.Split(strings.TrimSpace(w.Body.String()), string(rune(10)))
	if w.Code != http.StatusOK || len(lines) != 3 || !strings.HasPrefix(lines[0], "started,ended,minutes,group") ||
		!strings.Contains(lines[1], ",60.0,RTX3060,ccminer-tpfuemp,rinhash,zpool,RIN,false,switched,1200000.00,2.04e-05,0.1930,410,1") {
		t.Errorf("csv %d: %q", w.Code, lines)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "rbm-activity.csv") {
		t.Errorf("csv headers: %v", w.Header())
	}
}

func TestWatchdogTotalsAndOfferDetails(t *testing.T) {
	b := newBackend()
	if w := do(b, request{method: http.MethodPost, path: "/api/watchdog/reset"}); w.Code != http.StatusNoContent || b.watchdogResets != 1 {
		t.Errorf("watchdog reset: %d, %d resets", w.Code, b.watchdogResets)
	}
	body := do(b, request{path: "/api/totals"}).Body.String()
	for _, want := range []string{`"pool":"zpool"`, `"runs":42`, `"energy_kwh":4.616`, `"minutes":3052.77`} {
		if !strings.Contains(body, want) {
			t.Errorf("totals miss %s: %s", want, body)
		}
	}
	// where a miner connects to is no secret, what it logs in with is
	body = do(b, request{path: "/api/offers"}).Body.String()
	for _, want := range []string{`"host":"kawpow.eu.mine.zpool.ca"`, `"port":1325`, `"last_block_seconds":396`, `"blocks_24h":211`} {
		if !strings.Contains(body, want) {
			t.Errorf("offers miss %s: %s", want, body)
		}
	}
	if strings.Contains(body, "wallet1") {
		t.Errorf("offers leak the wallet: %s", body)
	}
}

func TestGuardWithoutPassword(t *testing.T) {
	put := request{method: http.MethodPut, path: "/api/config", body: `{}`}
	cases := map[string]struct {
		rq   request
		want int
	}{
		"local read":            {request{path: "/api/status"}, http.StatusOK},
		"local read, localhost": {request{path: "/api/status", host: "localhost:4000"}, http.StatusOK},
		"local read, ipv6":      {request{path: "/api/status", remote: "[::1]:50000", host: "[::1]:4000"}, http.StatusOK},
		"local change":          {put, http.StatusNoContent},
		// from another machine nothing works, not even reading
		"remote read":   {request{path: "/api/status", remote: "192.168.1.20:50000", host: "192.168.1.5:4000"}, http.StatusForbidden},
		"remote change": {request{method: http.MethodPut, path: "/api/config", body: `{}`, remote: "192.168.1.20:50000", host: "192.168.1.5:4000"}, http.StatusForbidden},
		"remote page":   {request{path: "/", remote: "192.168.1.20:50000", host: "192.168.1.5:4000"}, http.StatusForbidden},
		// a foreign name pointed at 127.0.0.1 by a hostile web page
		"rebinding": {request{path: "/api/config", host: "evil.example:4000"}, http.StatusForbidden},
		// a form or script on another site sending the browser here
		"no marker header": {request{method: http.MethodPut, path: "/api/config", body: `{}`, noHeader: true}, http.StatusForbidden},
		"foreign origin":   {request{method: http.MethodPut, path: "/api/config", body: `{}`, origin: "http://evil.example"}, http.StatusForbidden},
		"own origin":       {request{method: http.MethodPut, path: "/api/config", body: `{}`, origin: "http://127.0.0.1:4000"}, http.StatusNoContent},
	}
	for name, c := range cases {
		b := newBackend()
		if w := do(b, c.rq); w.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", name, w.Code, c.want, strings.TrimSpace(w.Body.String()))
		}
	}
}

func TestGuardWithPassword(t *testing.T) {
	remote := func(rq request) request {
		rq.remote, rq.host = "192.168.1.20:50000", "192.168.1.5:4000"
		return rq
	}
	cases := map[string]struct {
		rq   request
		want int
	}{
		"no password":        {request{path: "/api/status"}, http.StatusUnauthorized},
		"wrong password":     {request{path: "/api/status", password: "guess"}, http.StatusUnauthorized},
		"local with it":      {request{path: "/api/status", password: "s3cret"}, http.StatusOK},
		"remote with it":     {remote(request{path: "/api/status", password: "s3cret"}), http.StatusOK},
		"remote without":     {remote(request{path: "/"}), http.StatusUnauthorized},
		"remote change":      {remote(request{method: http.MethodPut, path: "/api/config", body: `{}`, password: "s3cret"}), http.StatusNoContent},
		"change, no marker":  {remote(request{method: http.MethodPut, path: "/api/config", body: `{}`, password: "s3cret", noHeader: true}), http.StatusForbidden},
		"change, other site": {remote(request{method: http.MethodPut, path: "/api/config", body: `{}`, password: "s3cret", origin: "http://evil.example"}), http.StatusForbidden},
	}
	for name, c := range cases {
		b := newBackend()
		b.cfg.General.API.Password = "s3cret"
		w := do(b, c.rq)
		if w.Code != c.want {
			t.Errorf("%s: %d, want %d", name, w.Code, c.want)
		}
		if c.want == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: the browser is not asked for the password", name)
		}
	}
}

func TestServer(t *testing.T) {
	srv, err := Start("127.0.0.1:0", Handler(newBackend()))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	resp, err := http.Get("http://" + srv.Addr() + "/api/devices")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "GPU#00") {
		t.Errorf("%d: %s", resp.StatusCode, body)
	}
	if _, err := Start(srv.Addr(), http.NotFoundHandler()); err == nil {
		t.Error("no error for a port that is in use")
	}
}
