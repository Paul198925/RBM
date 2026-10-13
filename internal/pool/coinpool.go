package pool

import (
	"context"
	"strings"
)

// CoinInfo names a coin a pool offers, and the algorithm it is mined with.
type CoinInfo struct {
	Symbol    string `json:"symbol"`
	Name      string `json:"name"`
	Algorithm string `json:"algorithm"`
}

// PriceFunc returns what coins cost in BTC, by the price service's id.
type PriceFunc func(ctx context.Context, ids []string) (map[string]float64, error)

// CoinPool is a pool that pays in the coin that is mined, to a wallet of
// that coin. What a hash earns there is worked out from the coin's price,
// its block reward and the hashrate of its network.
type CoinPool interface {
	Source
	// Balances returns what the pool holds for each wallet. Balances that
	// could be read come back together with the error of one that could not.
	Balances(ctx context.Context) ([]Balance, error)
}

// CoinPoolSettings is what every coin pool is set up with.
type CoinPoolSettings struct {
	// Wallets holds the payout address per coin symbol. A coin without one
	// is not offered.
	Wallets map[string]string
	Worker  string
	Region  string
	// ExcludeCoins are not offered.
	ExcludeCoins []string
	Prices       PriceFunc
}

// CoinPoolInfo describes a coin pool the application knows.
type CoinPoolInfo struct {
	// Name is what the pool's offers carry, Key the name of its settings
	// in the configuration as the interface gets it.
	Name       string     `json:"name"`
	Key        string     `json:"key"`
	Title      string     `json:"title"`
	FeePercent float64    `json:"fee_percent"`
	Regions    []string   `json:"regions"`
	Coins      []CoinInfo `json:"coins"`
}

// CoinPools lists the coin pools.
func CoinPools() []CoinPoolInfo {
	return []CoinPoolInfo{
		{Name: "2miners", Key: "twominers", Title: "2Miners", FeePercent: twoMinersFee, Regions: TwoMinersRegions, Coins: coinInfos(twoMinersCoins)},
		{Name: "woolypooly", Key: "woolypooly", Title: "WoolyPooly", FeePercent: woolyPoolyFee, Regions: WoolyPoolyRegions, Coins: coinInfos(woolyPoolyCoins)},
		{Name: "moneroocean", Key: "moneroocean", Title: "MoneroOcean", FeePercent: moneroOceanFee, Regions: MoneroOceanRegions, Coins: coinInfos(moneroOceanCoins)},
	}
}

// NewCoinPool makes the coin pool of that name, nil if there is none.
func NewCoinPool(name string, s CoinPoolSettings) CoinPool {
	switch name {
	case "2miners":
		return &TwoMiners{Wallets: s.Wallets, Worker: s.Worker, Region: s.Region, ExcludeCoins: s.ExcludeCoins, Prices: s.Prices}
	case "woolypooly":
		return &WoolyPooly{Wallets: s.Wallets, Worker: s.Worker, ExcludeCoins: s.ExcludeCoins, Prices: s.Prices}
	case "moneroocean":
		return &MoneroOcean{Wallets: s.Wallets, Worker: s.Worker, ExcludeCoins: s.ExcludeCoins}
	}
	return nil
}

// poolCoin is a coin in the table of a pool.
type poolCoin struct {
	CoinInfo
	// rpc is the coin's name in the pool's addresses
	rpc  string
	port int
	// divisor turns the pool's amounts into coins, where they are not
	divisor float64
	// priceID names the coin at the price service
	priceID string
}

// The names at the price service of coins that pools pay out in and that
// are not among the coins of the coin pools.
var payoutPriceIDs = map[string]string{
	"BCH": "bitcoin-cash", "DASH": "dash", "DGB": "digibyte", "DOGE": "dogecoin", "KAS": "kaspa", "LTC": "litecoin",
	"VTC": "vertcoin", "XMR": "monero", "ZEC": "zcash",
}

// PriceID returns the name of a coin at the price service, by its symbol.
// It is empty for a coin that is not known here, and for Bitcoin, which is
// what prices are told in.
func PriceID(symbol string) string {
	symbol = strings.ToUpper(symbol)
	for _, coins := range [][]poolCoin{twoMinersCoins, woolyPoolyCoins, moneroOceanCoins} {
		for _, c := range coins {
			if c.Symbol == symbol {
				return c.priceID
			}
		}
	}
	return payoutPriceIDs[symbol]
}

func coinInfos(coins []poolCoin) []CoinInfo {
	infos := make([]CoinInfo, len(coins))
	for i, c := range coins {
		infos[i] = c.CoinInfo
	}
	return infos
}

// ethMode returns mode for the algorithms that have stratum dialects.
func ethMode(algorithm, mode string) string {
	switch algorithm {
	case "ethash", "etchash":
		return mode
	}
	return ""
}

// perHashPrice is what one hash per second earns in a day, in BTC, at a
// pool that keeps feePercent: its share of the blocks the network finds.
// With PPLNS the luck of the pool evens out over time.
func perHashPrice(btc, reward, blockTime, networkHashrate, feePercent float64) float64 {
	return btc * reward * (86400 / blockTime) / networkHashrate * (1 - feePercent/100)
}
