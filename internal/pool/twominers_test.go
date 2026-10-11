package pool

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// twoMinersServer answers like the API of 2miners.com, one path per coin.
// The figures are taken from the real answers of 2026-10-11, shortened.
func twoMinersServer(t *testing.T, calls map[string]int) *TwoMiners {
	t.Helper()
	answers := map[string]string{
		// the reward is not in the stats and has to come from the blocks
		"/rvn/stats": `{"hashrate":308652513516.99,"workersTotal":3626,"now":1791711054624,
			"nodes":[{"avgBlockTime":"61.83","difficulty":"9005.1763","networkhashps":"611249811743.4303"}],
			"stats":{"lastBlockFound":1791710978}}`,
		"/rvn/blocks": `{"candidates":[{"reward":125000000000,"orphan":false}],
			"immature":[{"reward":125000000000},{"reward":999000000000,"orphan":true}],
			"matured":[{"reward":125200000000},{"reward":0}]}`,
		"/xna/stats": `{"hashrate":30208334598.32,"workersTotal":410,"now":1791711063000,
			"nodes":[{"avgBlockTime":"53.41","blockReward":"300000000000","networkhashps":"30558597122.4743"}],
			"stats":{"lastBlockFound":1791710000}}`,
		// a pool nobody mines at
		"/nexa/stats": `{"hashrate":0,"nodes":[{"avgBlockTime":"106.48","networkhashps":"2551385192046.1"}]}`,
		"/rvn/accounts/RAddr1": `{"stats":{"balance":7653318750,"immature":1784475000,"pending":100000000,"paid":22901753509484},
			"payments":[{"amount":51019031211,"timestamp":1000,"tx":"old"},{"amount":51129168735,"timestamp":` + recent + `,"tx":"new"}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			calls[r.URL.Path]++
		}
		body, ok := answers[r.URL.Path]
		switch {
		case strings.HasPrefix(r.URL.Path, "/erg/"):
			w.WriteHeader(http.StatusBadGateway)
		case !ok:
			w.WriteHeader(http.StatusNotFound)
		default:
			w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return &TwoMiners{
		Wallets: map[string]string{"RVN": "RAddr1", "XNA": "NAddr1", "NEXA": "nexa:addr", "DOGE": "DAddr"},
		Worker:  "rig1", Region: "eu", API: srv.URL + "/%s",
		Prices: func(ctx context.Context, ids []string) (map[string]float64, error) {
			return map[string]float64{"ravencoin": 2.4622e-08, "neurai": 4.24428e-10, "nexacoin": 1.2e-11, "ergo": 3.96e-06}, nil
		},
	}
}

// a payment of an hour ago, as the pool writes it
var recent = strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)

func TestTwoMinersFetch(t *testing.T) {
	calls := map[string]int{}
	pool := twoMinersServer(t, calls)
	offers, err := pool.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// NEXA has no hashrate at the pool, DOGE is no coin of the pool
	if len(offers) != 2 || offers[0].Coin != "RVN" || offers[1].Coin != "XNA" {
		t.Fatalf("offers = %+v", offers)
	}

	rvn := offers[0]
	if rvn.Pool != "2miners" || rvn.Algorithm != "kawpow" || rvn.CoinName != "Ravencoin" {
		t.Errorf("offer = %+v", rvn)
	}
	if rvn.Host != "rvn.2miners.com" || rvn.Port != 6060 || rvn.TLSPort != 16060 || rvn.User != "RAddr1.rig1" || rvn.Pass != "x" {
		t.Errorf("endpoint = %s:%d tls %d, login %q / %q", rvn.Host, rvn.Port, rvn.TLSPort, rvn.User, rvn.Pass)
	}
	// 1250.67 RVN per block (the orphan and the empty one do not count),
	// 86400/61.83 blocks a day, shared by the network's hashrate, less 1 %
	reward := (1250.0 + 1250 + 1252) / 3
	want := 2.4622e-08 * reward * (86400 / 61.83) / 611249811743.4303 * 0.99
	if !near(rvn.Price, want) || rvn.FeePercent != 1 {
		t.Errorf("price = %g, want %g, fee %v", rvn.Price, want, rvn.FeePercent)
	}
	if rvn.Hashrate != 308652513516.99 || rvn.Workers != 3626 || !near(rvn.LastBlockSeconds, 76.624) {
		t.Errorf("offer = %+v", rvn)
	}
	// the pool has about half the network, so it finds about half the blocks
	if !near(rvn.Blocks24h, 86400/61.83*308652513516.99/611249811743.4303) {
		t.Errorf("blocks a day = %v", rvn.Blocks24h)
	}

	// XNA says its reward itself
	xna := offers[1]
	if want := 4.24428e-10 * 3000 * (86400 / 53.41) / 30558597122.4743 * 0.99; !near(xna.Price, want) {
		t.Errorf("xna price = %g, want %g", xna.Price, want)
	}
	if calls["/xna/blocks"] != 0 || calls["/rvn/blocks"] != 1 {
		t.Errorf("calls = %v", calls)
	}

	// the reward is remembered, the stats are read anew
	if _, err := pool.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls["/rvn/blocks"] != 1 || calls["/rvn/stats"] != 2 {
		t.Errorf("calls = %v", calls)
	}
}

func TestTwoMinersRegionAndFilters(t *testing.T) {
	pool := twoMinersServer(t, nil)
	pool.Region = "asia"
	pool.ExcludeCoins = []string{"RVN"}
	offers, err := pool.Fetch(context.Background())
	if err != nil || len(offers) != 1 || offers[0].Coin != "XNA" || offers[0].Host != "asia-xna.2miners.com" {
		t.Fatalf("offers = %+v, %v", offers, err)
	}

	// a coin without a price cannot be valued
	pool.Prices = func(context.Context, []string) (map[string]float64, error) { return map[string]float64{}, nil }
	if offers, err := pool.Fetch(context.Background()); err != nil || len(offers) != 0 {
		t.Errorf("without prices: %+v, %v", offers, err)
	}
	pool.Prices = func(context.Context, []string) (map[string]float64, error) { return nil, errors.New("status 429") }
	if _, err := pool.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("price outage: %v", err)
	}

	// no wallet, nothing to mine
	pool.Wallets = map[string]string{"DOGE": "DAddr"}
	if _, err := pool.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "no wallet") {
		t.Errorf("without wallets: %v", err)
	}
}

func TestTwoMinersOutage(t *testing.T) {
	pool := twoMinersServer(t, nil)
	// one coin failing leaves the others
	pool.Wallets["ERG"] = "9addr"
	offers, err := pool.Fetch(context.Background())
	if err != nil || len(offers) != 2 {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	// all failing is an error
	pool.Wallets = map[string]string{"ERG": "9addr"}
	if _, err := pool.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "ERG") {
		t.Errorf("outage: %v", err)
	}
}

func TestTwoMinersBalances(t *testing.T) {
	pool := twoMinersServer(t, nil)
	// an excluded coin may still have a balance
	pool.ExcludeCoins = []string{"RVN"}
	balances, err := pool.Balances(context.Background())
	// the pool does not know the XNA and NEXA wallets
	if err != nil || len(balances) != 1 {
		t.Fatalf("balances = %+v, %v", balances, err)
	}
	b := balances[0]
	if b.Pool != "2miners" || b.Currency != "RVN" || !near(b.Balance, 77.5331875) || !near(b.Unsold, 17.84475) || !near(b.Unpaid, 95.3779375) {
		t.Errorf("balance = %+v", b)
	}
	if !near(b.Total, 229017.53509484+95.3779375) || !near(b.Paid24h, 511.29168735) {
		t.Errorf("total %v, paid in a day %v", b.Total, b.Paid24h)
	}
	if len(b.Payouts) != 2 || b.Payouts[0].Tx != "new" || !near(b.Payouts[1].Amount, 510.19031211) {
		t.Errorf("payouts = %+v", b.Payouts)
	}

	// what could be read comes back with the error of what could not
	pool.Wallets["ERG"] = "9addr"
	balances, err = pool.Balances(context.Background())
	if err == nil || len(balances) != 1 {
		t.Errorf("balances = %+v, %v", balances, err)
	}
}
