package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"hashsteer/internal/algo"
)

const (
	woolyPoolyAPI  = "https://api.woolypooly.com/api"
	woolyPoolyHost = "pool.woolypooly.com"
	// what the pool keeps of a block, in percent (PPLNS), where its API
	// does not say
	woolyPoolyFee = 0.9
)

// WoolyPoolyRegions lists where woolypooly.com runs stratum servers: it has
// one address that leads to the nearest.
var WoolyPoolyRegions = []string{"eu"}

// The coins of woolypooly.com that a GPU or a CPU can mine with the miner programs
// known here. One port serves both plain and TLS connections.
var woolyPoolyCoins = []poolCoin{
	{CoinInfo: CoinInfo{"ERG", "Ergo", "autolykos2"}, rpc: "ergo-1", port: 3100, priceID: "ergo"},
	{CoinInfo: CoinInfo{"ETC", "Ethereum Classic", "etchash"}, rpc: "etc-1", port: 35000, priceID: "ethereum-classic"},
	{CoinInfo: CoinInfo{"ETHW", "EthereumPoW", "ethash"}, rpc: "ethw-1", port: 3096, priceID: "ethereum-pow-iou"},
	{CoinInfo: CoinInfo{"FIRO", "Firo", "firopow"}, rpc: "firo-1", port: 3104, priceID: "zcoin"},
	{CoinInfo: CoinInfo{"MEWC", "Meowcoin", "meowpow"}, rpc: "mewc-1", port: 3116, priceID: "meowcoin"},
	{CoinInfo: CoinInfo{"NEXA", "Nexa", "nexapow"}, rpc: "nexa-1", port: 3124, priceID: "nexacoin"},
	{CoinInfo: CoinInfo{"OCTA", "OctaSpace", "ethash"}, rpc: "octa-1", port: 3130, priceID: "octaspace"},
	// mined with the CPU, or with a GPU miner that knows the algorithm
	{CoinInfo: CoinInfo{"RTM", "Raptoreum", "ghostrider"}, rpc: "rtm-1", port: 3110, priceID: "raptoreum"},
	{CoinInfo: CoinInfo{"RVN", "Ravencoin", "kawpow"}, rpc: "raven-1", port: 55555, priceID: "ravencoin"},
	{CoinInfo: CoinInfo{"RXD", "Radiant", "sha512256d"}, rpc: "rxd-1", port: 3122, priceID: "radiant"},
	{CoinInfo: CoinInfo{"VTC", "Vertcoin", "verthash"}, rpc: "vtc-1", port: 3102, priceID: "vertcoin"},
	{CoinInfo: CoinInfo{"XNA", "Neurai", "kawpow"}, rpc: "xna-1", port: 3128, priceID: "neurai"},
}

// WoolyPooly reads woolypooly.com, a pool that pays in the coin that is
// mined. One document of its API describes all its coins.
type WoolyPooly struct {
	// Wallets holds the payout address per coin symbol. A coin without one
	// is not offered.
	Wallets map[string]string
	Worker  string
	// ExcludeCoins are not offered.
	ExcludeCoins []string
	Prices       PriceFunc

	// API and Client are replaceable for tests.
	API    string
	Client *http.Client
}

func (w *WoolyPooly) Name() string { return "woolypooly" }

func (w *WoolyPooly) coins(mining bool) []poolCoin {
	var coins []poolCoin
	for _, c := range woolyPoolyCoins {
		if w.Wallets[c.Symbol] == "" || (mining && slices.Contains(w.ExcludeCoins, c.Symbol)) {
			continue
		}
		coins = append(coins, c)
	}
	return coins
}

func (w *WoolyPooly) get(ctx context.Context, path string, v any) error {
	base := w.API
	if base == "" {
		base = woolyPoolyAPI
	}
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "hashsteer")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("woolypooly: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("woolypooly: status %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v); err != nil {
		return fmt.Errorf("woolypooly: %w", err)
	}
	return nil
}

type woolyPoolyCoin struct {
	Fee         *float64  `json:"fee"`
	NetHashrate flexFloat `json:"netHashrate"`
	BlockTime   flexFloat `json:"blockTime"`
	BlockReward flexFloat `json:"blockReward"`
	Modes       []struct {
		PayoutScheme string `json:"payoutScheme"`
		AlgoStats    map[string]struct {
			Hashrate    flexFloat `json:"hashrate"`
			MinersTotal flexFloat `json:"minersTotal"`
		} `json:"algo_stats"`
	} `json:"modes"`
}

// Fetch returns one offer per coin there is a wallet for.
func (w *WoolyPooly) Fetch(ctx context.Context) ([]Offer, error) {
	coins := w.coins(true)
	if len(coins) == 0 {
		return nil, errors.New("woolypooly: there is no wallet for any coin that may be mined there")
	}
	if w.Prices == nil {
		return nil, errors.New("woolypooly: no source of coin prices")
	}
	ids := make([]string, len(coins))
	for i, c := range coins {
		ids[i] = c.priceID
	}
	prices, err := w.Prices(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("woolypooly: %w", err)
	}
	var stats map[string]woolyPoolyCoin
	if err := w.get(ctx, "/stats", &stats); err != nil {
		return nil, err
	}
	if len(stats) == 0 {
		return nil, errors.New("woolypooly: empty status")
	}

	now := time.Now()
	var offers []Offer
	for _, coin := range coins {
		s, listed := stats[coin.rpc]
		btc := prices[coin.priceID]
		network, blockTime, reward := float64(s.NetHashrate), float64(s.BlockTime), float64(s.BlockReward)
		if !listed || btc <= 0 || network <= 0 || blockTime <= 0 || reward <= 0 {
			continue
		}
		// the pool also has a solo mode, which is a lottery and not used
		hashrate, miners := 0.0, 0
		for _, mode := range s.Modes {
			if mode.PayoutScheme == "PPLNS" {
				hashrate, miners = float64(mode.AlgoStats["default"].Hashrate), int(mode.AlgoStats["default"].MinersTotal)
			}
		}
		// a pool nobody mines at finds no blocks, whatever the network pays
		if hashrate <= 0 {
			continue
		}
		fee := woolyPoolyFee
		if s.Fee != nil {
			fee = *s.Fee
		}
		offers = append(offers, Offer{
			Pool:          w.Name(),
			Algorithm:     algo.Normalize(coin.Algorithm),
			PoolAlgorithm: coin.Algorithm,
			Coin:          coin.Symbol,
			CoinName:      coin.Name,
			Host:          woolyPoolyHost,
			Port:          coin.port,
			TLSPort:       coin.port,
			User:          w.Wallets[coin.Symbol] + "." + w.Worker,
			Pass:          "x",
			EthMode:       ethMode(coin.Algorithm, "qtminer"),
			// the pool's fee is taken off here: this is what arrives
			Price:      perHashPrice(btc, reward, blockTime, network, fee),
			FeePercent: fee,
			Hashrate:   hashrate,
			// the pool counts miners, not their rigs
			Workers:   miners,
			Blocks24h: 86400 / blockTime * min(1, hashrate/network),
			Updated:   now,
		})
	}
	sort.Slice(offers, func(i, j int) bool {
		if offers[i].Algorithm != offers[j].Algorithm {
			return offers[i].Algorithm < offers[j].Algorithm
		}
		return offers[i].Coin < offers[j].Coin
	})
	return offers, nil
}

type woolyPoolyAccount struct {
	Stats *struct {
		Balance  flexFloat `json:"balance"`
		Immature flexFloat `json:"immature_balance"`
		Paid     flexFloat `json:"paid"`
	} `json:"stats"`
	// the fields of a payment are read as loosely as they are documented
	Payments []map[string]any `json:"payments"`
}

// Balances asks the pool what it holds for each wallet, one balance per
// coin. A wallet the pool has never seen has none. The pool wants the
// address in the request; it is its own account name there. The amounts
// are in coins.
func (w *WoolyPooly) Balances(ctx context.Context) ([]Balance, error) {
	var balances []Balance
	var failed error
	for _, coin := range w.coins(false) {
		var account woolyPoolyAccount
		err := w.get(ctx, "/"+coin.rpc+"/accounts/"+url.PathEscape(strings.TrimSpace(w.Wallets[coin.Symbol])), &account)
		if errors.Is(err, errNotFound) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			failed = fmt.Errorf("%w (%s)", err, coin.Symbol)
			continue
		}
		if account.Stats == nil {
			continue
		}
		confirmed, immature := float64(account.Stats.Balance), float64(account.Stats.Immature)
		balance := Balance{
			Pool:     w.Name(),
			Currency: coin.Symbol,
			Balance:  confirmed,
			// here this is what was mined in blocks that are not final yet
			Unsold:  immature,
			Unpaid:  confirmed + immature,
			Total:   float64(account.Stats.Paid) + confirmed + immature,
			Payouts: []Payout{},
			Updated: time.Now(),
		}
		dayAgo := time.Now().Add(-24 * time.Hour)
		for _, p := range account.Payments {
			payout, ok := loosePayout(p)
			if !ok {
				continue
			}
			if payout.Time.After(dayAgo) {
				balance.Paid24h += payout.Amount
			}
			balance.Payouts = append(balance.Payouts, payout)
		}
		sort.Slice(balance.Payouts, func(i, j int) bool { return balance.Payouts[i].Time.After(balance.Payouts[j].Time) })
		balances = append(balances, balance)
	}
	return balances, failed
}

// loosePayout reads a payment whose field names are not known for
// certain: the usual ones are tried. It needs an amount and a time.
func loosePayout(p map[string]any) (Payout, bool) {
	number := func(names ...string) float64 {
		for _, name := range names {
			if v, ok := p[name].(float64); ok {
				return v
			}
		}
		return 0
	}
	text := func(names ...string) string {
		for _, name := range names {
			if v, ok := p[name].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	payout := Payout{Amount: number("amount", "value"), Tx: text("tx", "txid", "txHash", "hash")}
	if seconds := number("timestamp", "time", "date"); seconds > 0 {
		// some pools count in milliseconds
		if seconds > 1e11 {
			seconds /= 1000
		}
		payout.Time = time.Unix(int64(seconds), 0)
	} else if t, err := time.Parse(time.RFC3339, text("created", "date", "time", "timestamp")); err == nil {
		payout.Time = t
	}
	return payout, payout.Amount > 0 && !payout.Time.IsZero()
}
