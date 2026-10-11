package rates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCoinGecko(t *testing.T) {
	calls, status := 0, http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.URL.Query().Get("ids"); got != "neurai,ravencoin" || r.URL.Query().Get("vs_currencies") != "btc" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.WriteHeader(status)
		w.Write([]byte(`{"ravencoin":{"btc":2.4622e-08},"neurai":{"btc":0}}`))
	}))
	defer srv.Close()
	c := &CoinGecko{URL: srv.URL}
	ctx := context.Background()

	prices, err := c.InBTC(ctx, []string{"ravencoin", "neurai"})
	// a coin without a price is left out
	if err != nil || len(prices) != 1 || prices["ravencoin"] != 2.4622e-08 {
		t.Fatalf("prices = %v, %v", prices, err)
	}
	// the answer is used for a while, whatever the order of the question
	if _, err := c.InBTC(ctx, []string{"neurai", "ravencoin"}); err != nil || calls != 1 {
		t.Errorf("%d calls, %v", calls, err)
	}
	if prices, err := c.InBTC(ctx, nil); err != nil || len(prices) != 0 || calls != 1 {
		t.Errorf("nothing asked: %v, %v, %d calls", prices, err, calls)
	}

	// an old answer stands in during an outage, but not for ever
	status = http.StatusTooManyRequests
	c.at = time.Now().Add(-coinPriceAge - time.Second)
	if prices, err := c.InBTC(ctx, []string{"ravencoin", "neurai"}); err != nil || prices["ravencoin"] == 0 || calls != 2 {
		t.Errorf("outage: %v, %v, %d calls", prices, err, calls)
	}
	c.at = time.Now().Add(-coinPriceMaxAge - time.Second)
	if _, err := c.InBTC(ctx, []string{"ravencoin", "neurai"}); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("long outage: %v", err)
	}
}
