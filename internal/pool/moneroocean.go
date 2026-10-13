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
)

const (
	moneroOceanAPI  = "https://api.moneroocean.stream"
	moneroOceanHost = "gulf.moneroocean.stream"
	// the port that sets the difficulty by itself; + 10000 is the same with TLS
	moneroOceanPort = 10128
	// the pool keeps nothing; what it exchanges costs what the exchange takes
	moneroOceanFee = 0
	// the pool counts in piconero
	moneroAtomic = 1e12
)

// MoneroOceanSwitching is the name of what is no algorithm: the pool
// switching between its coins by itself.
const MoneroOceanSwitching = "moneroocean"

// MoneroOceanRegions lists where the pool runs stratum servers: one
// address that leads to the nearest.
var MoneroOceanRegions = []string{"global"}

// The pool mines many coins and pays all of them in Monero. A miner that
// does not tell it how fast it is on each algorithm gets Monero's own;
// one that does is handed what pays best. Both are offered, for the CPU.
var moneroOceanCoins = []poolCoin{
	{CoinInfo: CoinInfo{"XMR", "Monero", "randomx"}, priceID: "monero"},
}

// MoneroOcean reads moneroocean.stream, a pool for CPUs that pays in
// Monero. It says itself what a hash earns there.
type MoneroOcean struct {
	// Wallets holds the payout address per coin symbol; the one for XMR
	// is the account at the pool.
	Wallets map[string]string
	Worker  string
	// ExcludeCoins are not offered.
	ExcludeCoins []string
	// API replaces the address of the pool's API. For tests.
	API    string
	Client *http.Client
}

func (m *MoneroOcean) Name() string { return "moneroocean" }

func (m *MoneroOcean) wallet() string { return strings.TrimSpace(m.Wallets["XMR"]) }

func (m *MoneroOcean) get(ctx context.Context, path string, v any) error {
	base := m.API
	if base == "" {
		base = moneroOceanAPI
	}
	client := m.Client
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
		// the address of the wallet may be part of the request: keep it
		// out of what is logged
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("moneroocean: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("moneroocean: status %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v); err != nil {
		return fmt.Errorf("moneroocean: %w", err)
	}
	return nil
}

type moneroOceanStats struct {
	Pool struct {
		HashRate flexFloat `json:"hashRate"`
		Miners   flexFloat `json:"miners"`
		Price    struct {
			BTC flexFloat `json:"btc"`
		} `json:"price"`
		// by the port of the coin's own network, as text
		Coins map[string]struct {
			Symbol string `json:"symbol"`
			Algo   string `json:"algo"`
			Active bool   `json:"active"`
			// Profit is what one hash per second earns in a day, in XMR.
			Profit    flexFloat `json:"profit"`
			Hashrate  flexFloat `json:"hashrate"`
			Miners    flexFloat `json:"miners"`
			BlockTime flexFloat `json:"blockTime"`
		} `json:"coins"`
		LastBlockFoundTime flexFloat `json:"lastBlockFoundTime"`
	} `json:"pool_statistics"`
}

// Fetch returns the offer for Monero, if there is a wallet for it.
func (m *MoneroOcean) Fetch(ctx context.Context) ([]Offer, error) {
	if m.wallet() == "" || slices.Contains(m.ExcludeCoins, "XMR") {
		return nil, errors.New("moneroocean: there is no XMR wallet, or XMR may not be mined")
	}
	var stats moneroOceanStats
	if err := m.get(ctx, "/pool/stats", &stats); err != nil {
		return nil, err
	}
	btc := float64(stats.Pool.Price.BTC)
	if btc <= 0 || len(stats.Pool.Coins) == 0 {
		return nil, errors.New("moneroocean: the pool names no price")
	}
	coin := moneroOceanCoins[0]
	now := time.Now()
	var offers []Offer
	for _, c := range stats.Pool.Coins {
		// Monero itself, on its own algorithm
		if c.Symbol != "XMR" || c.Algo != "rx/0" || !c.Active || c.Profit <= 0 || c.Hashrate <= 0 {
			continue
		}
		offer := Offer{
			Pool:          m.Name(),
			Algorithm:     "randomx",
			PoolAlgorithm: c.Algo,
			Coin:          coin.Symbol,
			CoinName:      coin.Name,
			Host:          moneroOceanHost,
			Port:          moneroOceanPort,
			TLSPort:       moneroOceanPort + 10000,
			// the account is the address alone; the pool takes the
			// password for the name of the rig
			User:       m.wallet(),
			Pass:       m.Worker,
			Price:      float64(c.Profit) * btc * (1 - moneroOceanFee/100.0),
			FeePercent: moneroOceanFee,
			Hashrate:   float64(c.Hashrate),
			Workers:    int(c.Miners),
			Updated:    now,
		}
		if last := float64(stats.Pool.LastBlockFoundTime); last > 0 && float64(now.Unix()) > last {
			offer.LastBlockSeconds = float64(now.Unix()) - last
		}
		offers = append(offers, offer)

		// the pool's own switching between all its coins, for the miner
		// program that tells it how fast the CPU is on each algorithm.
		// That program reports its hashrate in hashes of Monero's
		// algorithm that earn the same, so one of them pays what one
		// of Monero's does.
		switching := offer
		switching.Algorithm, switching.PoolAlgorithm = MoneroOceanSwitching, ""
		switching.Coin, switching.CoinName = "", ""
		if total := float64(stats.Pool.HashRate); total > 0 {
			switching.Hashrate, switching.Workers = total, int(stats.Pool.Miners)
		}
		offers = append(offers, switching)
	}
	sort.Slice(offers, func(i, j int) bool { return offers[i].Algorithm < offers[j].Algorithm })
	return offers, nil
}

type moneroOceanAccount struct {
	// in piconero
	Paid        flexFloat `json:"amtPaid"`
	Due         flexFloat `json:"amtDue"`
	TotalHashes flexFloat `json:"totalHashes"`
}

type moneroOceanPayment struct {
	Time   flexFloat `json:"ts"`
	Amount flexFloat `json:"amount"`
	Tx     string    `json:"txnHash"`
}

// Balances asks the pool what it holds for the XMR wallet. A wallet the
// pool has never seen has none. The pool wants the address in the request;
// it is its own account name there.
func (m *MoneroOcean) Balances(ctx context.Context) ([]Balance, error) {
	if m.wallet() == "" {
		return nil, nil
	}
	address := url.PathEscape(m.wallet())
	var account moneroOceanAccount
	if err := m.get(ctx, "/miner/"+address+"/stats", &account); err != nil {
		return nil, err
	}
	// the pool answers for every address, with nothing but zeros for one
	// it does not know
	if account.Paid == 0 && account.Due == 0 && account.TotalHashes == 0 {
		return nil, nil
	}
	due, paid := float64(account.Due)/moneroAtomic, float64(account.Paid)/moneroAtomic
	balance := Balance{
		Pool:     m.Name(),
		Currency: "XMR",
		// the pool does not tell what is still to mature apart
		Balance: due,
		Unpaid:  due,
		Total:   due + paid,
		Payouts: []Payout{},
		Updated: time.Now(),
	}
	// the payments are worth showing, but no reason to show no balance
	var payments []moneroOceanPayment
	err := m.get(ctx, "/miner/"+address+"/payments", &payments)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	dayAgo := time.Now().Add(-24 * time.Hour)
	for _, p := range payments {
		payout := Payout{Time: time.Unix(int64(p.Time), 0), Amount: float64(p.Amount) / moneroAtomic, Tx: p.Tx}
		if payout.Amount <= 0 {
			continue
		}
		if payout.Time.After(dayAgo) {
			balance.Paid24h += payout.Amount
		}
		balance.Payouts = append(balance.Payouts, payout)
	}
	sort.Slice(balance.Payouts, func(i, j int) bool { return balance.Payouts[i].Time.After(balance.Payouts[j].Time) })
	return []Balance{balance}, err
}
