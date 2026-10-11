package pool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const coinList = `{
  "CLORE": {"algo": "kawpow", "port": 1325, "ssl_port": 51325, "name": "Clore", "hashrate": 3100000000, "workers": 60, "estimate": "0.0001100000", "conversion_disabled": 0, "minimum_payout": 0.5, "error": ""},
  "RVN": {"algo": "kawpow", "port": 1325, "ssl_port": 51325, "name": "Raven", "hashrate": 0, "workers": 0, "estimate": "0.0000557650", "conversion_disabled": 0, "minimum_payout": 0.05, "error": ""},
  "NEOX": {"algo": "kawpow", "port": 1325, "ssl_port": 51325, "name": "Neoxa", "hashrate": 900000000, "workers": 20, "estimate": "0.0000000000", "conversion_disabled": 1, "error": ""},
  "MEWC": {"algo": "kawpow", "port": 1325, "ssl_port": 51325, "name": "Meowcoin", "hashrate": 200000000, "workers": 11, "estimate": "0.0000900000", "conversion_disabled": 0, "error": ""},
  "DGB-sha512": {"algo": "sha512256d", "port": 3342, "name": "Digibyte", "symbol": "DGB", "hashrate": 800000000000, "workers": 8, "estimate": "0.0000083000", "conversion_disabled": 0, "error": ""},
  "ODD": {"algo": "unknownalgo", "port": 9999, "name": "Odd", "hashrate": 5, "workers": 1, "estimate": "1", "conversion_disabled": 1, "error": ""}
}`

// zpoolServer serves the status fixture and the coin list above.
func zpoolServer(t *testing.T, coinsStatus int) *Zpool {
	t.Helper()
	status, err := os.ReadFile("testdata/zpool_status.json")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) { w.Write(status) })
	mux.HandleFunc("/currencies", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(coinsStatus)
		w.Write([]byte(coinList))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Zpool{Wallet: "wallet1", Currency: "LTC", Worker: "rig1", Region: "eu",
		URL: srv.URL + "/status", CurrenciesURL: srv.URL + "/currencies"}
}

func labels(offers []Offer) string {
	var out []string
	for _, o := range offers {
		out = append(out, o.Algorithm+"@"+o.Label())
	}
	return strings.Join(out, " ")
}

func TestZpoolCoinOffers(t *testing.T) {
	z := zpoolServer(t, http.StatusOK)
	z.CoinMining = true
	offers, err := z.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// left out: RVN (nobody mines it), NEOX (pays nothing), ODD (unknown algorithm)
	if got, want := labels(offers), "kawpow@zpool:CLORE kawpow@zpool:MEWC sha512256d@zpool:DGB"; got != want {
		t.Fatalf("offers = %q, want %q", got, want)
	}

	clore := offers[0]
	if clore.Host != "kawpow.eu.mine.zpool.ca" || clore.Port != 1325 || clore.TLSPort != 51325 || clore.CoinName != "Clore" {
		t.Errorf("clore = %+v", clore)
	}
	// payout in LTC, mining pinned to the one coin
	if clore.User != "wallet1" || clore.Pass != "rig1,c=LTC,zap=CLORE" {
		t.Errorf("login = %q / %q", clore.User, clore.Pass)
	}
	// 0.00011 BTC per day for 1000 MH/s, the factor of the kawpow port
	if !near(clore.Price, 0.00011/1e9) || clore.FeePercent != 1 || clore.Hashrate != 3.1e9 || clore.Workers != 60 {
		t.Errorf("clore figures = %+v", clore)
	}
	// the pool's key goes into the password, the symbol is what we show
	if dgb := offers[2]; dgb.Coin != "DGB" || dgb.Pass != "rig1,c=LTC,zap=DGB-sha512" || !near(dgb.Price, 0.0000083/1e12) {
		t.Errorf("dgb = %+v", dgb)
	}
}

func TestZpoolBothKindsOfOffers(t *testing.T) {
	z := zpoolServer(t, http.StatusOK)
	z.CoinMining, z.AlgoSwitching = true, true
	offers, err := z.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// per algorithm the port offer first, then its coins
	want := "ghostrider@zpool kawpow@zpool kawpow@zpool:CLORE kawpow@zpool:MEWC sha512256d@zpool sha512256d@zpool:DGB"
	if got := labels(offers); got != want {
		t.Errorf("offers = %q, want %q", got, want)
	}
	for _, o := range offers {
		if o.Coin == "" && o.Pass != "rig1,c=LTC" {
			t.Errorf("port offer has pass %q", o.Pass)
		}
	}
}

func TestZpoolCoinFilters(t *testing.T) {
	z := zpoolServer(t, http.StatusOK)
	z.CoinMining = true

	z.Coins = []string{"MEWC", "DGB"}
	offers, _ := z.Fetch(context.Background())
	if got := labels(offers); got != "kawpow@zpool:MEWC sha512256d@zpool:DGB" {
		t.Errorf("only list: %q", got)
	}

	z.Coins, z.ExcludeCoins = nil, []string{"CLORE"}
	offers, _ = z.Fetch(context.Background())
	if got := labels(offers); got != "kawpow@zpool:MEWC sha512256d@zpool:DGB" {
		t.Errorf("exclude list: %q", got)
	}
}

func TestZpoolCoinListOutage(t *testing.T) {
	// with the ports as a fallback, a missing coin list is not fatal
	z := zpoolServer(t, http.StatusBadGateway)
	z.CoinMining, z.AlgoSwitching = true, true
	offers, err := z.Fetch(context.Background())
	if err != nil || labels(offers) != "ghostrider@zpool kawpow@zpool sha512256d@zpool" {
		t.Errorf("offers = %q, err = %v", labels(offers), err)
	}

	// without them there is nothing to offer
	z.AlgoSwitching = false
	if _, err := z.Fetch(context.Background()); err == nil {
		t.Error("no error without any offers")
	}

	// neither kind enabled: nothing, and no request for the coin list
	z.CoinMining = false
	offers, err = z.Fetch(context.Background())
	if err != nil || len(offers) != 0 {
		t.Errorf("offers = %q, err = %v", labels(offers), err)
	}
}
