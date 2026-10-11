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
	calls, status, asked := 0, http.StatusOK, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		asked = r.URL.Query().Get("ids")
		if r.URL.Query().Get("vs_currencies") != "btc" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.WriteHeader(status)
		w.Write([]byte(`{"ravencoin":{"btc":2.4622e-08},"neurai":{"btc":0},"ergo":{"btc":3.96e-06}}`))
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

	if asked != "neurai,ravencoin" {
		t.Errorf("asked for %q", asked)
	}
	// another asker with other coins gets its own answer and spoils nothing
	if prices, err := c.InBTC(ctx, []string{"ergo"}); err != nil || prices["ergo"] != 3.96e-06 || calls != 2 || asked != "ergo" {
		t.Errorf("ergo: %v, %v, %d calls, asked %q", prices, err, calls, asked)
	}
	if _, err := c.InBTC(ctx, []string{"ravencoin", "neurai"}); err != nil || calls != 2 {
		t.Errorf("%d calls, %v", calls, err)
	}

	// an old answer stands in during an outage, but not for ever
	age := func(d time.Duration) {
		for id, p := range c.prices {
			p.at = time.Now().Add(-d)
			c.prices[id] = p
		}
	}
	status = http.StatusTooManyRequests
	age(coinPriceAge + time.Second)
	if prices, err := c.InBTC(ctx, []string{"ravencoin", "neurai"}); err != nil || prices["ravencoin"] == 0 || calls != 3 {
		t.Errorf("outage: %v, %v, %d calls", prices, err, calls)
	}
	age(coinPriceMaxAge + time.Second)
	if _, err := c.InBTC(ctx, []string{"ravencoin", "neurai"}); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("long outage: %v", err)
	}
}
