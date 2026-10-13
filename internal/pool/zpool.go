package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"hashsteer/internal/algo"
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

	// AlgoSwitching offers the pool's algorithm ports, where the pool
	// picks the coin and may change it every few minutes.
	AlgoSwitching bool
	// CoinMining offers single coins.
	CoinMining bool
	// Coins limits the single coins to these symbols, empty allows all;
	// ExcludeCoins are left out.
	Coins        []string
	ExcludeCoins []string

	// URL, CurrenciesURL, WalletURL and Client are replaceable for tests.
	URL           string
	CurrenciesURL string
	WalletURL     string
	Client        *http.Client
}

func (z *Zpool) Name() string { return "zpool" }

// flexFloat reads a JSON number that may also arrive as a string, or as
// true or false.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "null":
		return nil
	case "false":
		// how the pool's PHP says "nothing" where a number belongs
		*f = 0
		return nil
	case "true":
		*f = 1
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

type zpoolCoin struct {
	Symbol             string    `json:"symbol"`
	Name               string    `json:"name"`
	Algo               string    `json:"algo"`
	Port               flexFloat `json:"port"`
	SSLPort            flexFloat `json:"ssl_port"`
	Hashrate           flexFloat `json:"hashrate"`
	Workers            flexFloat `json:"workers"`
	Estimate           flexFloat `json:"estimate"`
	TimeSinceLast      flexFloat `json:"timesincelast"`
	Blocks24h          flexFloat `json:"24h_blocks"`
	ConversionDisabled flexFloat `json:"conversion_disabled"`
	MinimumPayout      flexFloat `json:"minimum_payout"`
	Error              string    `json:"error"`
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
	req.Header.Set("User-Agent", "hashsteer")
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

func (z *Zpool) coins(ctx context.Context) (map[string]zpoolCoin, error) {
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
	return coins, nil
}

// Fetch returns what the pool pays right now: one offer per algorithm port
// and, with coin mining, one per coin somebody is mining there.
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
	var offers []Offer
	// the prices are BTC per day for mbtc_mh_factor MH/s; without the
	// factor the numbers cannot be read
	perHash := func(a zpoolAlgo) float64 { return 1 / (float64(a.MBTCMHFactor) * 1e6) }

	if z.AlgoSwitching {
		for key, a := range status {
			name := a.Name
			if name == "" {
				name = key
			}
			if a.MBTCMHFactor <= 0 || a.Port <= 0 {
				continue
			}
			offers = append(offers, Offer{
				Pool:          z.Name(),
				Algorithm:     algo.Normalize(name),
				PoolAlgorithm: name,
				Host:          z.host(name),
				Port:          int(a.Port),
				TLSPort:       int(a.SSLPort),
				User:          z.Wallet,
				Pass:          z.pass(""),
				Price:         float64(a.EstimateCurrent) * perHash(a),
				Price24h:      float64(a.EstimateLast24h) * perHash(a),
				// the paid value comes in mBTC where the estimates are in BTC
				Actual24h:  float64(a.ActualLast24h) / 1000 * perHash(a),
				FeePercent: float64(a.Fees),
				Hashrate:   float64(a.Hashrate),
				Workers:    int(a.Workers),
				Updated:    now,
			})
		}
	}

	if z.CoinMining {
		coins, err := z.coins(ctx)
		if err != nil {
			// the algorithm ports can still be mined without the coin list
			if !z.AlgoSwitching {
				return nil, err
			}
		}
		for key, coin := range coins {
			a, ok := status[coin.Algo]
			if !ok || a.MBTCMHFactor <= 0 || coin.Port <= 0 {
				continue
			}
			symbol := coinSymbol(key, coin)
			if !z.coinAllowed(symbol) {
				continue
			}
			// a coin without an estimate pays nothing, and one nobody is
			// mining would have to find its blocks with this rig alone
			if coin.Estimate <= 0 || coin.Hashrate <= 0 {
				continue
			}
			offers = append(offers, Offer{
				Pool:          z.Name(),
				Algorithm:     algo.Normalize(coin.Algo),
				PoolAlgorithm: coin.Algo,
				Coin:          symbol,
				CoinName:      coin.Name,
				Host:          z.host(coin.Algo),
				Port:          int(coin.Port),
				TLSPort:       int(coin.SSLPort),
				User:          z.Wallet,
				// the pool wants its own key here, which differs from
				// the symbol for coins with several algorithms
				Pass:       z.pass(key),
				Price:      float64(coin.Estimate) * perHash(a),
				FeePercent: float64(a.Fees),
				Hashrate:   float64(coin.Hashrate),
				Workers:    int(coin.Workers),

				LastBlockSeconds: float64(coin.TimeSinceLast),
				Blocks24h:        float64(coin.Blocks24h),
				Updated:          now,
			})
		}
	}

	sort.Slice(offers, func(i, j int) bool {
		if offers[i].Algorithm != offers[j].Algorithm {
			return offers[i].Algorithm < offers[j].Algorithm
		}
		return offers[i].Coin < offers[j].Coin
	})
	return offers, nil
}

func (z *Zpool) host(poolAlgorithm string) string {
	return fmt.Sprintf("%s.%s.mine.zpool.ca", poolAlgorithm, z.Region)
}

// pass builds the stratum password, which carries the options at zpool:
// c names the payout coin, zap the one coin to mine.
func (z *Zpool) pass(zap string) string {
	pass := fmt.Sprintf("%s,c=%s", z.Worker, z.Currency)
	if zap != "" {
		pass += ",zap=" + zap
	}
	return pass
}

func coinSymbol(key string, coin zpoolCoin) string {
	symbol := coin.Symbol
	if symbol == "" {
		symbol = key
	}
	return strings.ToUpper(symbol)
}

func (z *Zpool) coinAllowed(symbol string) bool {
	if len(z.Coins) > 0 && !slices.Contains(z.Coins, symbol) {
		return false
	}
	return !slices.Contains(z.ExcludeCoins, symbol)
}

// PayoutCurrency is a coin zpool can pay the earnings out in.
type PayoutCurrency struct {
	Symbol        string  `json:"symbol"`
	Name          string  `json:"name"`
	MinimumPayout float64 `json:"minimum_payout"`
	// Notice is a warning of the pool about this currency, e.g. that it is
	// short of it at the moment. Empty if there is none.
	Notice string `json:"notice"`
}

// PayoutCurrencies lists the coins the earnings can be converted to, sorted
// by symbol. Whatever is mined, the pool pays in the one chosen here; the
// wallet has to be an address of that coin.
func (z *Zpool) PayoutCurrencies(ctx context.Context) ([]PayoutCurrency, error) {
	coins, err := z.coins(ctx)
	if err != nil {
		return nil, err
	}

	// a coin with several algorithms is listed once per algorithm
	bySymbol := map[string]PayoutCurrency{}
	for key, coin := range coins {
		if coin.ConversionDisabled != 0 {
			continue
		}
		symbol := coinSymbol(key, coin)
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
