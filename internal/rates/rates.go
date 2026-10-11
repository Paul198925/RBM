// Package rates looks up what a bitcoin costs, to compare mining revenue
// with the price of electricity.
package rates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const coinbaseURL = "https://api.coinbase.com/v2/prices/BTC-%s/spot"

// Coinbase reads the public spot price of Coinbase. It needs no account.
type Coinbase struct {
	// URL is a format string with one %s for the currency, and Client the
	// HTTP client; both are replaceable for tests.
	URL    string
	Client *http.Client
}

// BTCPrice returns the price of one bitcoin in currency, e.g. "EUR".
func (c *Coinbase) BTCPrice(ctx context.Context, currency string) (float64, error) {
	url := c.URL
	if url == "" {
		url = coinbaseURL
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(url, currency), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "rbm")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("exchange rate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("exchange rate BTC-%s: status %s", currency, resp.Status)
	}

	var body struct {
		Data struct {
			Amount string `json:"amount"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return 0, fmt.Errorf("exchange rate: %w", err)
	}
	price, err := strconv.ParseFloat(body.Data.Amount, 64)
	if err != nil || price <= 0 {
		return 0, fmt.Errorf("exchange rate BTC-%s: unusable amount %q", currency, body.Data.Amount)
	}
	return price, nil
}
