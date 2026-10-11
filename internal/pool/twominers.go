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
	"sync"
	"time"

	"rbm/internal/algo"
)

const twoMinersAPI = "https://%s.2miners.com/api"

// twoMinersFee is what the pool keeps of a block, in percent (PPLNS).
const twoMinersFee = 1.0

// how long the block reward of a coin is taken as known
const rewardMaxAge = time.Hour

// TwoMinersRegions lists where 2miners.com runs stratum servers.
var TwoMinersRegions = []string{"eu", "us", "asia"}

// TwoMinersCoin is a coin that can be mined at 2miners.com.
type TwoMinersCoin struct {
	Symbol    string `json:"symbol"`
	Name      string `json:"name"`
	Algorithm string `json:"algorithm"`

	// rpc is the coin's name in the pool's host names
	rpc  string
	port int
	// divisor turns the pool's amounts into coins
	divisor float64
	// priceID names the coin at the price service
	priceID string
}

// The coins of 2miners.com that a GPU can mine. The stratum ports are the
// pool's ports for small rigs; port + 10000 is the same with TLS.
var twoMinersCoins = []TwoMinersCoin{
	{Symbol: "ERG", Name: "Ergo", Algorithm: "autolykos2", rpc: "erg", port: 8888, divisor: 1e9, priceID: "ergo"},
	{Symbol: "ETC", Name: "Ethereum Classic", Algorithm: "etchash", rpc: "etc", port: 1010, divisor: 1e18, priceID: "ethereum-classic"},
	{Symbol: "ETHW", Name: "EthereumPoW", Algorithm: "ethash", rpc: "ethw", port: 2020, divisor: 1e18, priceID: "ethereum-pow-iou"},
	{Symbol: "NEXA", Name: "Nexa", Algorithm: "nexapow", rpc: "nexa", port: 5050, divisor: 100, priceID: "nexacoin"},
	{Symbol: "RVN", Name: "Ravencoin", Algorithm: "kawpow", rpc: "rvn", port: 6060, divisor: 1e8, priceID: "ravencoin"},
	{Symbol: "XNA", Name: "Neurai", Algorithm: "kawpow", rpc: "xna", port: 6060, divisor: 1e8, priceID: "neurai"},
}

// TwoMinersCoins lists the coins of 2miners.com that can be mined here.
func TwoMinersCoins() []TwoMinersCoin { return slices.Clone(twoMinersCoins) }

// TwoMiners reads 2miners.com, a pool with one stratum server per coin
// that pays in the coin that is mined. So it takes a wallet per coin, and
// what a hash earns there has to be worked out from the coin's price, its
// block reward and the hashrate of its network.
type TwoMiners struct {
	// Wallets holds the payout address per coin symbol. A coin without one
	// is not offered.
	Wallets map[string]string
	Worker  string
	Region  string
	// ExcludeCoins are not offered.
	ExcludeCoins []string

	// Prices returns what coins cost in BTC, by the price service's id.
	Prices func(ctx context.Context, ids []string) (map[string]float64, error)

	// API is a format string with one %s for the coin and Client the HTTP
	// client; both are replaceable for tests.
	API    string
	Client *http.Client

	mu      sync.Mutex
	rewards map[string]cachedReward
}

type cachedReward struct {
	coins float64
	at    time.Time
}

func (t *TwoMiners) Name() string { return "2miners" }

// coins returns the coins there is a wallet for, without the excluded ones
// if mining is asked about.
func (t *TwoMiners) coins(mining bool) []TwoMinersCoin {
	var coins []TwoMinersCoin
	for _, c := range twoMinersCoins {
		if t.Wallets[c.Symbol] == "" || (mining && slices.Contains(t.ExcludeCoins, c.Symbol)) {
			continue
		}
		coins = append(coins, c)
	}
	return coins
}

var errNotFound = errors.New("not found")

// get reads one JSON document of the pool's API for a coin into v.
func (t *TwoMiners) get(ctx context.Context, coin TwoMinersCoin, path string, v any) error {
	base := t.API
	if base == "" {
		base = twoMinersAPI
	}
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(base, coin.rpc)+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "rbm")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("2miners %s: %w", coin.Symbol, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("2miners %s: status %s", coin.Symbol, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v); err != nil {
		return fmt.Errorf("2miners %s: %w", coin.Symbol, err)
	}
	return nil
}

type twoMinersStats struct {
	Hashrate     flexFloat `json:"hashrate"`
	WorkersTotal flexFloat `json:"workersTotal"`
	// Now is the pool's clock in milliseconds.
	Now   flexFloat `json:"now"`
	Nodes []struct {
		AvgBlockTime  flexFloat `json:"avgBlockTime"`
		BlockReward   flexFloat `json:"blockReward"`
		NetworkHashps flexFloat `json:"networkhashps"`
	} `json:"nodes"`
	Stats struct {
		LastBlockFound flexFloat `json:"lastBlockFound"`
	} `json:"stats"`
}

type twoMinersBlock struct {
	Reward flexFloat `json:"reward"`
	Orphan bool      `json:"orphan"`
}

// Fetch returns one offer per coin there is a wallet for. A coin whose API
// does not answer is left out as long as another one does.
func (t *TwoMiners) Fetch(ctx context.Context) ([]Offer, error) {
	coins := t.coins(true)
	if len(coins) == 0 {
		return nil, errors.New("2miners: there is no wallet for any coin that may be mined there")
	}
	if t.Prices == nil {
		return nil, errors.New("2miners: no source of coin prices")
	}
	ids := make([]string, len(coins))
	for i, c := range coins {
		ids[i] = c.priceID
	}
	prices, err := t.Prices(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("2miners: %w", err)
	}

	var offers []Offer
	var failed error
	for _, coin := range coins {
		// without a price nothing can be said about what the coin earns
		price := prices[coin.priceID]
		if price <= 0 {
			continue
		}
		offer, ok, err := t.offer(ctx, coin, price)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			failed = err
			continue
		}
		if ok {
			offers = append(offers, offer)
		}
	}
	if len(offers) == 0 && failed != nil {
		return nil, failed
	}
	sort.Slice(offers, func(i, j int) bool {
		if offers[i].Algorithm != offers[j].Algorithm {
			return offers[i].Algorithm < offers[j].Algorithm
		}
		return offers[i].Coin < offers[j].Coin
	})
	return offers, nil
}

// offer works out what the pool pays for a coin that costs btc. It reports
// false for a coin that cannot be mined there at the moment.
func (t *TwoMiners) offer(ctx context.Context, coin TwoMinersCoin, btc float64) (Offer, bool, error) {
	var stats twoMinersStats
	if err := t.get(ctx, coin, "/stats", &stats); err != nil {
		return Offer{}, false, err
	}
	if len(stats.Nodes) == 0 {
		return Offer{}, false, nil
	}
	node := stats.Nodes[0]
	network, blockTime, hashrate := float64(node.NetworkHashps), float64(node.AvgBlockTime), float64(stats.Hashrate)
	// a pool nobody mines at finds no blocks, whatever the network pays
	if network <= 0 || blockTime <= 0 || hashrate <= 0 {
		return Offer{}, false, nil
	}
	reward := float64(node.BlockReward) / coin.divisor
	if reward <= 0 {
		var err error
		if reward, err = t.reward(ctx, coin); err != nil {
			return Offer{}, false, err
		}
		if reward <= 0 {
			return Offer{}, false, nil
		}
	}

	blocksPerDay := 86400 / blockTime
	// what one hash per second finds of the network's blocks in a day, on
	// average: with PPLNS the luck of the pool evens out over time
	perHash := btc * reward * blocksPerDay / network
	offer := Offer{
		Pool:          t.Name(),
		Algorithm:     algo.Normalize(coin.Algorithm),
		PoolAlgorithm: coin.Algorithm,
		Coin:          coin.Symbol,
		CoinName:      coin.Name,
		Host:          t.host(coin),
		Port:          coin.port,
		TLSPort:       coin.port + 10000,
		User:          t.Wallets[coin.Symbol] + "." + t.Worker,
		Pass:          "x",
		// the pool's fee is taken off here: this is what arrives
		Price:      perHash * (1 - twoMinersFee/100),
		FeePercent: twoMinersFee,
		Hashrate:   hashrate,
		Workers:    int(stats.WorkersTotal),
		Blocks24h:  blocksPerDay * min(1, hashrate/network),
		Updated:    time.Now(),
	}
	if last, now := float64(stats.Stats.LastBlockFound), float64(stats.Now)/1000; last > 0 && now > last {
		offer.LastBlockSeconds = now - last
	}
	return offer, true, nil
}

// reward returns the average reward of the blocks the pool found lately,
// in coins. It is 0 if the pool lists none.
func (t *TwoMiners) reward(ctx context.Context, coin TwoMinersCoin) (float64, error) {
	t.mu.Lock()
	cached, ok := t.rewards[coin.Symbol]
	t.mu.Unlock()
	if ok && time.Since(cached.at) < rewardMaxAge {
		return cached.coins, nil
	}

	var blocks struct {
		Candidates []twoMinersBlock `json:"candidates"`
		Immature   []twoMinersBlock `json:"immature"`
		Matured    []twoMinersBlock `json:"matured"`
	}
	if err := t.get(ctx, coin, "/blocks", &blocks); err != nil {
		// an old figure is better than none: rewards change rarely
		if ok {
			return cached.coins, nil
		}
		return 0, err
	}
	sum, n := 0.0, 0
	for _, list := range [][]twoMinersBlock{blocks.Candidates, blocks.Immature, blocks.Matured} {
		for _, b := range list {
			if b.Reward > 0 && !b.Orphan {
				sum += float64(b.Reward)
				n++
			}
		}
	}
	if n == 0 {
		return 0, nil
	}
	reward := sum / float64(n) / coin.divisor
	t.mu.Lock()
	if t.rewards == nil {
		t.rewards = map[string]cachedReward{}
	}
	t.rewards[coin.Symbol] = cachedReward{coins: reward, at: time.Now()}
	t.mu.Unlock()
	return reward, nil
}

func (t *TwoMiners) host(coin TwoMinersCoin) string {
	prefix := ""
	if t.Region != "" && t.Region != "eu" {
		prefix = t.Region + "-"
	}
	return prefix + coin.rpc + ".2miners.com"
}

type twoMinersAccount struct {
	Stats struct {
		Balance  flexFloat `json:"balance"`
		Immature flexFloat `json:"immature"`
		Pending  flexFloat `json:"pending"`
		Paid     flexFloat `json:"paid"`
	} `json:"stats"`
	Payments []struct {
		Amount    flexFloat `json:"amount"`
		Timestamp flexFloat `json:"timestamp"`
		Tx        string    `json:"tx"`
	} `json:"payments"`
}

// Balances asks the pool what it holds for each wallet, one balance per
// coin. A wallet the pool has never seen has none. The pool wants the
// address in the request; it is its own account name there. Balances that
// could be read come back together with the error of one that could not.
func (t *TwoMiners) Balances(ctx context.Context) ([]Balance, error) {
	var balances []Balance
	var failed error
	for _, coin := range t.coins(false) {
		var account twoMinersAccount
		err := t.get(ctx, coin, "/accounts/"+url.PathEscape(strings.TrimSpace(t.Wallets[coin.Symbol])), &account)
		if errors.Is(err, errNotFound) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			failed = err
			continue
		}
		s := account.Stats
		confirmed := float64(s.Balance+s.Pending) / coin.divisor
		immature := float64(s.Immature) / coin.divisor
		balance := Balance{
			Pool:     t.Name(),
			Currency: coin.Symbol,
			Balance:  confirmed,
			// here this is what was mined in blocks that are not final yet
			Unsold:  immature,
			Unpaid:  confirmed + immature,
			Total:   float64(s.Paid)/coin.divisor + confirmed + immature,
			Payouts: []Payout{},
			Updated: time.Now(),
		}
		dayAgo := time.Now().Add(-24 * time.Hour)
		for _, p := range account.Payments {
			payout := Payout{Time: time.Unix(int64(p.Timestamp), 0), Amount: float64(p.Amount) / coin.divisor, Tx: p.Tx}
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
