package pool

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func serve(t *testing.T, status int, body []byte) *Zpool {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return &Zpool{Wallet: "wallet1", Currency: "BTC", Worker: "rig1", Region: "eu", AlgoSwitching: true, URL: srv.URL, CurrenciesURL: srv.URL}
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Abs(b) }

func TestZpoolFetch(t *testing.T) {
	body, err := os.ReadFile("testdata/zpool_status.json")
	if err != nil {
		t.Fatal(err)
	}
	offers, err := serve(t, http.StatusOK, body).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// the entry without a factor cannot be priced and is left out
	if len(offers) != 3 {
		t.Fatalf("got %d offers", len(offers))
	}
	byAlgo := map[string]Offer{}
	for _, o := range offers {
		byAlgo[o.Algorithm] = o
	}

	k := byAlgo["kawpow"]
	if k.Host != "kawpow.eu.mine.zpool.ca" || k.Port != 1325 || k.TLSPort != 51325 {
		t.Errorf("endpoint = %s:%d tls %d", k.Host, k.Port, k.TLSPort)
	}
	if k.User != "wallet1" || k.Pass != "rig1,c=BTC" {
		t.Errorf("login = %q / %q", k.User, k.Pass)
	}
	if k.FeePercent != 1 || k.Workers != 91 || k.Hashrate != 4228081484 {
		t.Errorf("offer = %+v", k)
	}
	// 0.00013124 BTC per day for 1000 MH/s
	if !near(k.Price, 0.00013124/1e9) || !near(k.Price24h, 0.00010723/1e9) || !near(k.Actual24h, 0.00010357/1e9) {
		t.Errorf("prices = %g %g %g", k.Price, k.Price24h, k.Actual24h)
	}

	// 0.00000055 BTC per day for 0.001 MH/s = 1 kH/s
	if g := byAlgo["ghostrider"]; !near(g.Price, 0.00000055/1e3) {
		t.Errorf("ghostrider price = %g", g.Price)
	}
}

func TestZpoolFetchErrors(t *testing.T) {
	cases := map[string]*Zpool{
		"http error": serve(t, http.StatusServiceUnavailable, []byte("busy")),
		"not json":   serve(t, http.StatusOK, []byte("<html>maintenance</html>")),
		"empty":      serve(t, http.StatusOK, []byte("{}")),
	}
	for name, z := range cases {
		if _, err := z.Fetch(context.Background()); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestZpoolPayoutCurrencies(t *testing.T) {
	body, err := os.ReadFile("testdata/zpool_currencies.json")
	if err != nil {
		t.Fatal(err)
	}
	currencies, err := serve(t, http.StatusOK, body).PayoutCurrencies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var symbols []string
	for _, c := range currencies {
		symbols = append(symbols, c.Symbol)
	}
	// sorted, coins without conversion left out, a coin with two algorithms
	// listed once, and BTC present although the pool does not list it
	if got, want := strings.Join(symbols, " "), "BTC DGB DOGE LTC RVN"; got != want {
		t.Fatalf("symbols = %q, want %q", got, want)
	}

	doge, ok := FindPayoutCurrency(currencies, "DOGE")
	if !ok || doge.MinimumPayout != 5 || !strings.Contains(doge.Notice, "short of this currency") {
		t.Errorf("DOGE = %+v", doge)
	}
	if ltc, _ := FindPayoutCurrency(currencies, "LTC"); ltc.Name != "Litecoin" || ltc.Notice != "" {
		t.Errorf("LTC = %+v", ltc)
	}
	if _, ok := FindPayoutCurrency(currencies, "ARG"); ok {
		t.Error("ARG has conversion disabled and must not be offered")
	}
}

func TestZpoolPayoutCurrenciesErrors(t *testing.T) {
	for name, z := range map[string]*Zpool{
		"http error": serve(t, http.StatusBadGateway, nil),
		"empty":      serve(t, http.StatusOK, []byte("{}")),
	} {
		if _, err := z.PayoutCurrencies(context.Background()); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
