package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"rbm/internal/algo"
)

const (
	zpoolStatusURL     = "https://www.zpool.ca/api/status"
	zpoolCurrenciesURL = "https://www.zpool.ca/api/currencies"
)

// ZpoolMinInterval is how often the status may be fetched without annoying
// the pool.
const ZpoolMinInterval = 2 * time.Minute

// Zpool reads zpool.ca, a pool that pays in one chosen currency whatever
// coin is actually mined.
type Zpool struct {
	Wallet   string
	Currency string
	Worker   string
	Region   string

	// URL, CurrenciesURL and Client are replaceable for tests.
	URL           string
	CurrenciesURL string
	Client        *http.Client
}

func (z *Zpool) Name() string { return "zpool" }

// flexFloat reads a JSON number that may also arrive as a string.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	if len(data) > 1 && data[0] == '"' {
		data = data[1 : len(data)-1]
	}
	if len(data) == 0 {
		return nil
	}
	v, err := strconv.ParseFloat(string(data), 64)
	if err != nil {
		return err
	}
	*f = flexFloat(v)
	return nil
}

type zpoolAlgo struct {
	Name            string    `json:"name"`
	Port            flexFloat `json:"port"`
	SSLPort         flexFloat `json:"ssl_port"`
	Fees            flexFloat `json:"fees"`
	Hashrate        flexFloat `json:"hashrate"`
	Workers         flexFloat `json:"workers"`
	EstimateCurrent flexFloat `json:"estimate_current"`
	EstimateLast24h flexFloat `json:"estimate_last24h"`
	ActualLast24h   flexFloat `json:"actual_last24h"`
	MBTCMHFactor    flexFloat `json:"mbtc_mh_factor"`
}

// get reads one JSON document of the pool's API into v.
func (z *Zpool) get(ctx context.Context, url string, v any) error {
	client := z.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "rbm")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("zpool: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("zpool: status %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v); err != nil {
		return fmt.Errorf("zpool: %w", err)
	}
	return nil
}

func (z *Zpool) Fetch(ctx context.Context) ([]Offer, error) {
	url := z.URL
	if url == "" {
		url = zpoolStatusURL
	}
	var status map[string]zpoolAlgo
	if err := z.get(ctx, url, &status); err != nil {
		return nil, err
	}
	if len(status) == 0 {
		return nil, fmt.Errorf("zpool: empty status")
	}

	now := time.Now()
	offers := make([]Offer, 0, len(status))
	for key, a := range status {
		name := a.Name
		if name == "" {
			name = key
		}
		// the estimates are BTC per day for mbtc_mh_factor MH/s; without
		// the factor the numbers cannot be read
		if a.MBTCMHFactor <= 0 || a.Port <= 0 {
			continue
		}
		perHash := 1 / (float64(a.MBTCMHFactor) * 1e6)
		offers = append(offers, Offer{
			Pool:          z.Name(),
			Algorithm:     algo.Normalize(name),
			PoolAlgorithm: name,
			Host:          fmt.Sprintf("%s.%s.mine.zpool.ca", name, z.Region),
			Port:          int(a.Port),
			TLSPort:       int(a.SSLPort),
			User:          z.Wallet,
			Pass:          fmt.Sprintf("%s,c=%s", z.Worker, z.Currency),
			Price:         float64(a.EstimateCurrent) * perHash,
			Price24h:      float64(a.EstimateLast24h) * perHash,
			// the paid value comes in mBTC where the estimates are in BTC
			Actual24h:  float64(a.ActualLast24h) / 1000 * perHash,
			FeePercent: float64(a.Fees),
			Hashrate:   float64(a.Hashrate),
			Workers:    int(a.Workers),
			Updated:    now,
		})
	}
	sort.Slice(offers, func(i, j int) bool { return offers[i].Algorithm < offers[j].Algorithm })
	return offers, nil
}

// PayoutCurrency is a coin zpool can pay the earnings out in.
type PayoutCurrency struct {
	Symbol        string
	Name          string
	MinimumPayout float64
	// Notice is a warning of the pool about this currency, e.g. that it is
	// short of it at the moment. Empty if there is none.
	Notice string
}

type zpoolCoin struct {
	Symbol             string    `json:"symbol"`
	Name               string    `json:"name"`
	ConversionDisabled flexFloat `json:"conversion_disabled"`
	MinimumPayout      flexFloat `json:"minimum_payout"`
	Error              string    `json:"error"`
}

// PayoutCurrencies lists the coins the earnings can be converted to, sorted
// by symbol. Whatever is mined, the pool pays in the one chosen here; the
// wallet has to be an address of that coin.
func (z *Zpool) PayoutCurrencies(ctx context.Context) ([]PayoutCurrency, error) {
	url := z.CurrenciesURL
	if url == "" {
		url = zpoolCurrenciesURL
	}
	var coins map[string]zpoolCoin
	if err := z.get(ctx, url, &coins); err != nil {
		return nil, err
	}
	if len(coins) == 0 {
		return nil, fmt.Errorf("zpool: empty currency list")
	}

	// a coin with several algorithms is listed once per algorithm
	bySymbol := map[string]PayoutCurrency{}
	for key, coin := range coins {
		if coin.ConversionDisabled != 0 {
			continue
		}
		symbol := coin.Symbol
		if symbol == "" {
			symbol = key
		}
		symbol = strings.ToUpper(symbol)
		c, seen := bySymbol[symbol]
		if !seen {
			c = PayoutCurrency{Symbol: symbol, Name: coin.Name, MinimumPayout: float64(coin.MinimumPayout)}
		}
		if c.Notice == "" {
			c.Notice = strings.TrimSpace(coin.Error)
		}
		bySymbol[symbol] = c
	}
	// the pool's base currency is always paid, listed or not
	if _, ok := bySymbol["BTC"]; !ok {
		bySymbol["BTC"] = PayoutCurrency{Symbol: "BTC", Name: "Bitcoin"}
	}

	currencies := make([]PayoutCurrency, 0, len(bySymbol))
	for _, c := range bySymbol {
		currencies = append(currencies, c)
	}
	sort.Slice(currencies, func(i, j int) bool { return currencies[i].Symbol < currencies[j].Symbol })
	return currencies, nil
}

// FindPayoutCurrency returns the entry for symbol.
func FindPayoutCurrency(currencies []PayoutCurrency, symbol string) (PayoutCurrency, bool) {
	for _, c := range currencies {
		if c.Symbol == symbol {
			return c, true
		}
	}
	return PayoutCurrency{}, false
}
