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
	key    string
	prices map[string]float64
	at     time.Time
}

// InBTC returns the price of each coin in BTC, by the service's id of the
// coin, e.g. "ravencoin". A coin the service does not know is left out.
func (c *CoinGecko) InBTC(ctx context.Context, ids []string) (map[string]float64, error) {
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	key := strings.Join(ids, ",")
	if key == "" {
		return map[string]float64{}, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if key == c.key && time.Since(c.at) < coinPriceAge {
		return c.prices, nil
	}
	prices, err := c.fetch(ctx, key)
	if err != nil {
		if key == c.key && time.Since(c.at) < coinPriceMaxAge {
			return c.prices, nil
		}
		return nil, err
	}
	c.key, c.prices, c.at = key, prices, time.Now()
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
