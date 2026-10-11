package share

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rbm/internal/estimate"
)

// fakeServer stands in for the share server: it keeps what is sent to it
// and hands out what it is given.
type fakeServer struct {
	received []submission
	serve    string
	status   int
}

func (f *fakeServer) client(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case f.status != 0:
			w.WriteHeader(f.status)
			io.WriteString(w, `{"error":"too many reports, try again in an hour"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/reports":
			var sub submission
			if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.received = append(f.received, sub)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/estimates":
			io.WriteString(w, f.serve)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	// a slash too many in the address does no harm
	return &Client{URL: srv.URL + "/"}
}

const id1 = "0123456789abcdef0123456789abcdef"

func TestEstimatesAreFetchedAndChecked(t *testing.T) {
	f := &fakeServer{serve: `{"estimates":[
		{"device":"GeForce RTX 3060","miner":"rigel","version":"1.23.2","algorithm":"kawpow","hashrate":20000000,"power_watts":151,"reports":3},
		{"device":"GeForce RTX 3060","miner":"rigel","algorithm":"ethash","hashrate":-5},
		{"device":"<script>alert(1)</script>","miner":"rigel","algorithm":"kawpow","hashrate":1},
		{"device":"GeForce RTX 3060","miner":"rigel","algorithm":"sha512256d","hashrate":1e40}]}`}
	c := f.client(t)
	entries, err := c.Estimates(context.Background())
	// what a rig could not have measured is not taken, wherever it comes from
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	e := entries[0]
	if e.Device != "GeForce RTX 3060" || e.Miner != "rigel" || e.Algorithm != "kawpow" || e.Hashrate != 20e6 || e.PowerWatts != 151 || e.Version != "1.23.2" {
		t.Errorf("entry = %+v", e)
	}

	f.serve = `{"estimates":[]}`
	if entries, err := c.Estimates(context.Background()); err != nil || len(entries) != 0 {
		t.Errorf("an empty list: %+v, %v", entries, err)
	}
	f.serve = `not json`
	if _, err := c.Estimates(context.Background()); err == nil {
		t.Error("nonsense was accepted")
	}
	f.status = http.StatusBadGateway
	if _, err := c.Estimates(context.Background()); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("an outage: %v", err)
	}
	if _, err := (&Client{URL: "http://127.0.0.1:1"}).Estimates(context.Background()); err == nil {
		t.Error("no server, no error")
	}
}

func TestReportSendsWhatFits(t *testing.T) {
	f := &fakeServer{}
	c := f.client(t)
	ctx := context.Background()
	entries := []estimate.Entry{
		{Device: "GeForce RTX 3060", Miner: "rigel", Version: "1.23.2", Algorithm: "kawpow", Hashrate: 19.8e6, PowerWatts: 160},
		{Device: "Intel(R) Core(TM) i9-10900K CPU @ 3.70GHz", Miner: "xmrig", Algorithm: "ghostrider", Hashrate: 2150},
		// a name with a sign the server does not take stays here
		{Device: "AMD Ryzen™ 9", Miner: "xmrig", Algorithm: "ghostrider", Hashrate: 3000},
	}
	if err := c.Report(ctx, id1, entries); err != nil {
		t.Fatal(err)
	}
	if len(f.received) != 1 || f.received[0].Client != id1 || len(f.received[0].Reports) != 2 {
		t.Fatalf("received = %+v", f.received)
	}
	r := f.received[0].Reports[0]
	if r.Device != "GeForce RTX 3060" || r.Miner != "rigel" || r.Version != "1.23.2" || r.Algorithm != "kawpow" || r.Hashrate != 19.8e6 || r.PowerWatts != 160 {
		t.Errorf("report = %+v", r)
	}
	// only these fields go out: nothing about the rig
	raw, _ := json.Marshal(f.received[0].Reports)
	var fields []map[string]any
	json.Unmarshal(raw, &fields)
	allowed := map[string]bool{"device": true, "miner": true, "version": true, "algorithm": true, "hashrate": true, "power_watts": true}
	for _, report := range fields {
		for field := range report {
			if !allowed[field] {
				t.Errorf("the report holds %q: %s", field, raw)
			}
		}
	}

	// nothing to send is no request
	if err := c.Report(ctx, id1, nil); err != nil || len(f.received) != 1 {
		t.Errorf("an empty report: %v, %d requests", err, len(f.received))
	}
	// the server's refusal comes back with its reason
	f.status = http.StatusTooManyRequests
	if err := c.Report(ctx, id1, entries); err == nil || !strings.Contains(err.Error(), "try again in an hour") {
		t.Errorf("refused: %v", err)
	}
}

func TestClientIDAndCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	first, err := ClientID(dir)
	if err != nil || !clientRE.MatchString(first) {
		t.Fatalf("id = %q, %v", first, err)
	}
	if again, _ := ClientID(dir); again != first {
		t.Errorf("the id changed: %q, %q", first, again)
	}
	// a damaged file gets a new id
	os.WriteFile(filepath.Join(dir, "share-id"), []byte("not an id"), 0o600)
	if fresh, err := ClientID(dir); err != nil || fresh == first || !clientRE.MatchString(fresh) {
		t.Errorf("after damage: %q, %v", fresh, err)
	}

	if entries, at := LoadCache(dir); entries != nil || !at.IsZero() {
		t.Errorf("a cache before the first fetch: %v, %v", entries, at)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	kawpow := []estimate.Entry{{Device: "GeForce RTX 3060", Miner: "rigel", Algorithm: "kawpow", Hashrate: 20e6, PowerWatts: 150}}
	if err := SaveCache(dir, kawpow, now); err != nil {
		t.Fatal(err)
	}
	entries, at := LoadCache(dir)
	if len(entries) != 1 || entries[0].Hashrate != 20e6 || entries[0].Device != "GeForce RTX 3060" || !at.Equal(now) {
		t.Errorf("cache = %+v, %v", entries, at)
	}
}
