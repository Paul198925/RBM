package rates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const coinGeckoURL = "https://api.coingecko.com/api/v3/simple/price"

const (
	// how long an answer is used before the service is asked again
	coinPriceAge = 5 * time.Minute
	// how long an old answer stands in when the service does not answer
	coinPriceMaxAge = time.Hour
)

// CoinGecko reads what coins cost in bitcoin from the public API of
// coingecko.com. It needs no account. The zero value is ready to use.
type CoinGecko struct {
	// URL and Client are replaceable for tests.
	URL    string
	Client *http.Client

	mu     sync.Mutex
	prices map[string]coinPrice
}

// coinPrice is what the service said about a coin, 0 if it has no price.
type coinPrice struct {
	btc float64
	at  time.Time
}

// InBTC returns the price of each coin in BTC, by the service's id of the
// coin, e.g. "ravencoin". A coin the service does not know is left out.
// Answers are kept per coin, so several askers with different coins do not
// make each other's answers worthless.
func (c *CoinGecko) InBTC(ctx context.Context, ids []string) (map[string]float64, error) {
	ids = append([]string(nil), ids...)
	sort.Strings(ids)

	c.mu.Lock()
	defer c.mu.Unlock()
	age := func(id string) time.Duration {
		if p, ok := c.prices[id]; ok {
			return time.Since(p.at)
		}
		return coinPriceMaxAge
	}
	due := false
	for _, id := range ids {
		due = due || age(id) >= coinPriceAge
	}
	if due {
		fresh, err := c.fetch(ctx, strings.Join(ids, ","))
		if err != nil {
			// an old answer stands in, as long as there is one for every coin
			for _, id := range ids {
				if age(id) >= coinPriceMaxAge {
					return nil, err
				}
			}
		} else {
			if c.prices == nil {
				c.prices = map[string]coinPrice{}
			}
			now := time.Now()
			for _, id := range ids {
				c.prices[id] = coinPrice{btc: fresh[id], at: now}
			}
		}
	}
	prices := make(map[string]float64, len(ids))
	for _, id := range ids {
		if p := c.prices[id]; p.btc > 0 {
			prices[id] = p.btc
		}
	}
	return prices, nil
}

func (c *CoinGecko) fetch(ctx context.Context, ids string) (map[string]float64, error) {
	base := c.URL
	if base == "" {
		base = coinGeckoURL
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	query := url.Values{"ids": {ids}, "vs_currencies": {"btc"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "rbm")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coin prices: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coin prices: status %s", resp.Status)
	}
	var body map[string]struct {
		BTC float64 `json:"btc"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("coin prices: %w", err)
	}
	prices := make(map[string]float64, len(body))
	for id, p := range body {
		if p.BTC > 0 {
			prices[id] = p.BTC
		}
	}
	if len(prices) == 0 {
		return nil, errors.New("coin prices: the answer holds no price")
	}
	return prices, nil
}
