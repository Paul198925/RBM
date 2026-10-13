// Package wallet asks public block explorers what a wallet address holds:
// what has arrived there, as opposed to what a pool still keeps for it.
package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Balance is what an address holds on its blockchain.
type Balance struct {
	Currency string `json:"currency"`
	Address  string `json:"address"`
	// Balance is confirmed, Pending is on its way in or out and not in a
	// block yet. Not every explorer tells the latter.
	Balance float64 `json:"balance"`
	Pending float64 `json:"pending"`
	// ValueBTC is what Balance is worth in bitcoin, 0 if the price of the
	// coin is not known.
	ValueBTC float64 `json:"value_btc"`
	// Explorer is the host that was asked.
	Explorer string    `json:"explorer"`
	Updated  time.Time `json:"updated"`
}

// explorer is a public service that knows the balances of one coin.
type explorer struct {
	// kind is the dialect it speaks
	kind string
	// base is its address, without a slash at the end
	base string
	// unit is how many of its smallest units make one coin
	unit float64
	// priceID is the name of the coin at CoinGecko, for what it is worth.
	// Empty for Bitcoin, which is what the worth is told in.
	priceID string
}

// The explorers, by coin symbol. All of them answer without an account or
// a key. A coin that is not listed has no balance shown.
var explorers = map[string]explorer{
	"BTC":  {"esplora", "https://mempool.space/api", 1e8, ""},
	"LTC":  {"esplora", "https://litecoinspace.org/api", 1e8, "litecoin"},
	"MEWC": {"esplora", "https://explorer.mewccrypto.com/api", 1e8, "meowcoin"},
	"DOGE": {"blockcypher", "https://api.blockcypher.com/v1/doge/main", 1e8, "dogecoin"},
	"RVN":  {"blockbook", "https://blockbook.ravencoin.org/api/v2", 1e8, "ravencoin"},
	"XNA":  {"blockbook", "https://blockbook.neurai.org/api/v2", 1e8, "neurai"},
	"ETC":  {"blockscout", "https://etc.blockscout.com/api/v2", 1e18, "ethereum-classic"},
	"ERG":  {"ergo", "https://api.ergoplatform.com/api/v1", 1e9, "ergo"},
	"FIRO": {"insight", "https://explorer.firo.org/insight-api-zcoin", 1e8, "zcoin"},
}

// Supported lists the coins whose balances can be asked for.
func Supported() []string {
	coins := make([]string, 0, len(explorers))
	for coin := range explorers {
		coins = append(coins, coin)
	}
	sort.Strings(coins)
	return coins
}

// PriceID returns the name of a coin at CoinGecko, where prices are asked
// for. It is empty for Bitcoin and for coins without a known explorer.
func PriceID(currency string) string { return explorers[strings.ToUpper(currency)].priceID }

// Known reports whether the balance of a coin can be asked for.
func Known(currency string) bool {
	_, ok := explorers[strings.ToUpper(currency)]
	return ok
}

// Reader asks the explorers.
type Reader struct {
	// Client is used for the requests, http.DefaultClient with a timeout
	// if nil.
	Client *http.Client
	// Base replaces the address of every explorer. For tests.
	Base string
}

// amount is a number that explorers send as a number or as text, in the
// smallest unit of the coin. It may be larger than fits an integer.
type amount struct{ value float64 }

func (a *amount) UnmarshalJSON(data []byte) error {
	text := strings.Trim(string(data), `"`)
	if text == "" || text == "null" {
		a.value = 0
		return nil
	}
	f, ok := new(big.Float).SetString(text)
	if !ok {
		return fmt.Errorf("%q is not an amount", text)
	}
	a.value, _ = f.Float64()
	return nil
}

// Balance asks for what address holds of currency.
func (r *Reader) Balance(ctx context.Context, currency, address string) (Balance, error) {
	currency, address = strings.ToUpper(currency), strings.TrimSpace(address)
	e, ok := explorers[currency]
	if !ok {
		return Balance{}, fmt.Errorf("no block explorer is known for %s", currency)
	}
	if address == "" {
		return Balance{}, fmt.Errorf("no %s address", currency)
	}
	host := e.base
	if u, err := url.Parse(e.base); err == nil {
		host = u.Host
	}
	if r.Base != "" {
		e.base = strings.TrimRight(r.Base, "/")
	}
	b := Balance{Currency: currency, Address: address, Explorer: host}
	escaped := url.PathEscape(address)

	var confirmed, pending float64
	var err error
	switch e.kind {
	case "esplora":
		var reply struct {
			Chain, Mempool struct {
				Funded amount `json:"funded_txo_sum"`
				Spent  amount `json:"spent_txo_sum"`
			}
		}
		var raw struct {
			Chain   json.RawMessage `json:"chain_stats"`
			Mempool json.RawMessage `json:"mempool_stats"`
		}
		if err = r.get(ctx, e.base+"/address/"+escaped, &raw); err == nil {
			if raw.Chain == nil {
				err = fmt.Errorf("the answer holds no balance")
			} else if err = json.Unmarshal(raw.Chain, &reply.Chain); err == nil && raw.Mempool != nil {
				err = json.Unmarshal(raw.Mempool, &reply.Mempool)
			}
		}
		confirmed = reply.Chain.Funded.value - reply.Chain.Spent.value
		pending = reply.Mempool.Funded.value - reply.Mempool.Spent.value
	case "blockbook":
		var reply struct {
			Balance     *amount `json:"balance"`
			Unconfirmed amount  `json:"unconfirmedBalance"`
		}
		if err = r.get(ctx, e.base+"/address/"+escaped+"?details=basic", &reply); err == nil && reply.Balance == nil {
			err = fmt.Errorf("the answer holds no balance")
		}
		if reply.Balance != nil {
			confirmed, pending = reply.Balance.value, reply.Unconfirmed.value
		}
	case "blockcypher":
		var reply struct {
			Balance     *amount `json:"balance"`
			Unconfirmed amount  `json:"unconfirmed_balance"`
		}
		if err = r.get(ctx, e.base+"/addrs/"+escaped+"/balance", &reply); err == nil && reply.Balance == nil {
			err = fmt.Errorf("the answer holds no balance")
		}
		if reply.Balance != nil {
			confirmed, pending = reply.Balance.value, reply.Unconfirmed.value
		}
	case "blockscout":
		var reply struct {
			Balance *amount `json:"coin_balance"`
		}
		if err = r.get(ctx, e.base+"/addresses/"+escaped, &reply); err == nil && reply.Balance == nil {
			err = fmt.Errorf("the answer holds no balance")
		}
		if reply.Balance != nil {
			confirmed = reply.Balance.value
		}
	case "ergo":
		var reply struct {
			Confirmed *struct {
				NanoErgs amount `json:"nanoErgs"`
			} `json:"confirmed"`
			Unconfirmed struct {
				NanoErgs amount `json:"nanoErgs"`
			} `json:"unconfirmed"`
		}
		if err = r.get(ctx, e.base+"/addresses/"+escaped+"/balance/total", &reply); err == nil && reply.Confirmed == nil {
			err = fmt.Errorf("the answer holds no balance")
		}
		if reply.Confirmed != nil {
			confirmed, pending = reply.Confirmed.NanoErgs.value, reply.Unconfirmed.NanoErgs.value
		}
	case "insight":
		var reply struct {
			Balance     *amount `json:"balanceSat"`
			Unconfirmed amount  `json:"unconfirmedBalanceSat"`
		}
		if err = r.get(ctx, e.base+"/addr/"+escaped+"?noTxList=1", &reply); err == nil && reply.Balance == nil {
			err = fmt.Errorf("the answer holds no balance")
		}
		if reply.Balance != nil {
			confirmed, pending = reply.Balance.value, reply.Unconfirmed.value
		}
	default:
		err = fmt.Errorf("explorer of kind %q", e.kind)
	}
	if err != nil {
		return Balance{}, fmt.Errorf("%s balance from %s: %w", currency, host, err)
	}
	b.Balance, b.Pending, b.Updated = confirmed/e.unit, pending/e.unit, time.Now()
	return b, nil
}

func (r *Reader) get(ctx context.Context, address string, into any) error {
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hashsteer")
	resp, err := client.Do(req)
	if err != nil {
		// the address of the wallet is part of the request: keep it out
		// of what is logged
		if uerr, ok := err.(*url.Error); ok {
			return uerr.Err
		}
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		text := strings.TrimSpace(string(body))
		if len(text) > 120 || strings.HasPrefix(text, "<") {
			text = ""
		}
		return fmt.Errorf("status %s %s", resp.Status, text)
	}
	return json.Unmarshal(body, into)
}
