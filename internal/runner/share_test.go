package runner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hashsteer/internal/estimate"
	"hashsteer/internal/share"
	"hashsteer/internal/stats"
)

func TestHashratesAreExchangedOnlyAsAsked(t *testing.T) {
	app, out := testApp(t)
	// a stand-in for the share server: another rig has measured this card
	var received []string
	served := `{"estimates":[{"device":"GeForce RTX 3060","miner":"rigel","version":"1.23.2","algorithm":"kawpow","hashrate":21000000,"power_watts":150,"reports":1}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			received = append(received, string(body))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		io.WriteString(w, served)
	}))
	defer srv.Close()
	ctx := context.Background()
	dataDir := app.cfg.General.DataDir

	// this rig has measured something as well
	store, err := stats.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var st stats.Stat
	st.Update(560e6, 5*time.Minute, time.Now(), 0)
	if err := store.Save(stats.HashrateKey("rigel", []string{"GPU#00"}, "sha512256d"), st); err != nil {
		t.Fatal(err)
	}
	app.setStore(store)

	// without a server nothing is exchanged
	app.cfg.General.Sharing.URL = ""
	app.exchangeHashrates(ctx)
	if entries, _ := share.LoadCache(dataDir); entries != nil {
		t.Fatalf("fetched without a server: %+v", entries)
	}

	// with one, what others measured is fetched; this rig's stays here
	app.cfg.General.Sharing.URL = srv.URL
	app.exchangeHashrates(ctx)
	entries, fetched := share.LoadCache(dataDir)
	if len(entries) != 1 || entries[0].Hashrate != 21e6 || fetched.IsZero() {
		t.Fatalf("cache = %+v", entries)
	}
	if len(received) != 0 {
		t.Errorf("sent without being asked to: %v", received)
	}
	// the fetched figure wins over the one that comes with the program
	table := app.estimates(true, dataDir)
	if e, ok := table.Lookup("GeForce RTX 3060", "rigel", "kawpow"); !ok || e.Hashrate != 21e6 {
		t.Errorf("estimate in use = %+v, %v", e, ok)
	}
	if app.estimates(false, dataDir) != nil {
		t.Error("estimates although they are not wanted")
	}

	// only when the user says so is this rig's measurement sent
	app.cfg.General.Sharing.Upload = true
	app.exchangeHashrates(ctx)
	if len(received) != 1 {
		t.Fatalf("after sharing: %v", received)
	}
	var sent struct {
		Client  string `json:"client"`
		Reports []struct {
			Device, Miner, Algorithm string
			Hashrate                 float64
		} `json:"reports"`
	}
	if err := json.Unmarshal([]byte(received[0]), &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.Client) != 32 || len(sent.Reports) != 1 || sent.Reports[0].Device != "GeForce RTX 3060" ||
		sent.Reports[0].Algorithm != "sha512256d" || sent.Reports[0].Hashrate != 560e6 {
		t.Errorf("sent = %+v", sent)
	}
	// neither the wallet nor the name of the rig goes out
	app.cfg.Pools.Wallets["RVN"] = "RSecretWallet"
	app.exchangeHashrates(ctx)
	for _, body := range received {
		if strings.Contains(body, "RSecretWallet") || strings.Contains(body, app.cfg.General.Worker+`"`) {
			t.Errorf("the report names the rig: %s", body)
		}
	}
	if !strings.Contains(out.String(), "shared 1 measured hashrates") {
		t.Errorf("log: %q", out.String())
	}

	// and with fetching turned off the cache is left as it is
	app.cfg.General.Sharing.Download = false
	served = `{"estimates":[{"device":"GeForce RTX 3060","miner":"rigel","algorithm":"kawpow","hashrate":99000000}]}`
	app.exchangeHashrates(ctx)
	if entries, _ := share.LoadCache(dataDir); len(entries) != 1 || entries[0].Hashrate != 21e6 {
		t.Errorf("fetched although turned off: %+v", entries)
	}
}

func TestSharedListsWhatIsKnown(t *testing.T) {
	app, _ := testApp(t)
	app.cfg.General.Sharing.URL = ""
	if err := app.RefreshShared(context.Background()); err == nil {
		t.Error("a refresh without a server")
	}

	// before anything is fetched, the list is what comes with the program
	info, err := app.Shared()
	if err != nil || len(info.Entries) == 0 || info.Fetched != nil || !info.Used || info.Upload {
		t.Fatalf("info = %+v, %v", info, err)
	}
	var mine, others int
	for _, e := range info.Entries {
		if e.Reports != 0 || e.OwnHashrate != 0 {
			t.Errorf("entry = %+v", e)
		}
		// the test rig has an RTX 3060 and no RTX 2080 SUPER
		if e.Mine != (e.Device == "GeForce RTX 3060") {
			t.Errorf("mine is wrong for %+v", e)
		}
		if e.Mine {
			mine++
		} else {
			others++
		}
	}
	if mine == 0 || others == 0 {
		t.Errorf("%d of this rig's cards, %d of others", mine, others)
	}

	// what the server handed out comes on top, with the number of rigs,
	// next to what this rig measured itself
	if err := share.SaveCache(app.cfg.General.DataDir, []estimate.Entry{
		{Device: "GeForce RTX 3060", Miner: "rigel", Version: "1.23.2", Algorithm: "kawpow", Hashrate: 21e6, PowerWatts: 150, Reports: 5}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	store, err := stats.Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var st stats.Stat
	st.Update(19.8e6, 5*time.Minute, time.Now(), 0)
	store.Save(stats.HashrateKey("rigel", []string{"GPU#00"}, "kawpow"), st)
	app.setStore(store)

	info, _ = app.Shared()
	found := false
	for _, e := range info.Entries {
		if e.Device == "GeForce RTX 3060" && e.Miner == "rigel" && e.Algorithm == "kawpow" {
			found = true
			if e.Hashrate != 21e6 || e.Reports != 5 || e.OwnHashrate != 19.8e6 || !e.Mine {
				t.Errorf("entry = %+v", e)
			}
		}
	}
	if !found || info.Fetched == nil {
		t.Errorf("the fetched figure is missing: fetched %v", info.Fetched)
	}
}
