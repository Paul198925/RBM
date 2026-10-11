package rates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func coinbase(t *testing.T, status int, body string) *Coinbase {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prices/BTC-EUR/spot" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Coinbase{URL: srv.URL + "/prices/BTC-%s/spot"}
}

func TestBTCPrice(t *testing.T) {
	c := coinbase(t, http.StatusOK, `{"data":{"amount":"57432.125","base":"BTC","currency":"EUR"}}`)
	price, err := c.BTCPrice(context.Background(), "EUR")
	if err != nil || price != 57432.125 {
		t.Errorf("price = %v, %v", price, err)
	}
	if _, err := c.BTCPrice(context.Background(), "XXX"); err == nil {
		t.Error("no error for an unknown currency")
	}
}

func TestBTCPriceErrors(t *testing.T) {
	cases := map[string]*Coinbase{
		"http error": coinbase(t, http.StatusServiceUnavailable, "busy"),
		"not json":   coinbase(t, http.StatusOK, "<html>"),
		"no amount":  coinbase(t, http.StatusOK, `{"data":{}}`),
		"zero":       coinbase(t, http.StatusOK, `{"data":{"amount":"0"}}`),
	}
	for name, c := range cases {
		if _, err := c.BTCPrice(context.Background(), "EUR"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
