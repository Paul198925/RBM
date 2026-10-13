package pool

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// woolyPoolyServer answers like the API of woolypooly.com. The figures are
// taken from the real answer of 2026-10-11, shortened.
func woolyPoolyServer(t *testing.T) *WoolyPooly {
	t.Helper()
	answers := map[string]string{
		"/stats": `{
			"raven-1": {"minPay": 5, "fee": 0.9, "netHashrate": 669342432690.7031, "blockTime": 60, "blockReward": 1250, "modes": [
				{"payoutScheme": "PPLNS", "algo_stats": {"default": {"hashrate": 6007017984, "minersTotal": 94}}},
				{"payoutScheme": "SOLO", "algo_stats": {"default": {"hashrate": 158036640, "minersTotal": 2}}}]},
			"etc-1": {"fee": 0.5, "netHashrate": 126754838882092.11, "blockTime": 15.8, "blockReward": 1.6385609772947838, "modes": [
				{"payoutScheme": "PPLNS", "algo_stats": {"default": {"hashrate": 11766666240, "minersTotal": 27}}}]},
			"firo-1": {"netHashrate": 11241242572.3, "blockTime": 150, "blockReward": 0.3125, "modes": [
				{"payoutScheme": "SOLO", "algo_stats": {"default": {"hashrate": 9692309504, "minersTotal": 100}}}]},
			"zano-1": {"fee": 0.9, "netHashrate": 314815170184, "blockTime": 60, "blockReward": 1, "modes": []}}`,
		"/raven-1/accounts/RAddr1": `{"stats": {"balance": 12.5, "immature_balance": 2.5, "paid": 1000},
			"payments": [{"amount": 50, "timestamp": 1000, "tx": "old"}, {"value": 60.5, "created": "` + time.Now().Add(-time.Hour).UTC().Format(time.RFC3339) + `", "txHash": "new"}, {"note": "unreadable"}]}`,
		"/etc-1/accounts/0xabc": `{}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := answers[r.URL.Path]
		switch {
		case strings.HasPrefix(r.URL.Path, "/firo-1/"):
			w.WriteHeader(http.StatusBadGateway)
		case !ok:
			w.WriteHeader(http.StatusNotFound)
		default:
			w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return &WoolyPooly{
		Wallets: map[string]string{"RVN": "RAddr1", "ETC": "0xabc", "FIRO": "aFiro", "XNA": "NAddr"},
		Worker:  "rig1", API: srv.URL,
		Prices: func(ctx context.Context, ids []string) (map[string]float64, error) {
			return map[string]float64{"ravencoin": 2.4622e-08, "ethereum-classic": 0.0001003, "zcoin": 1.173e-05, "neurai": 4.2e-10}, nil
		},
	}
}

func TestWoolyPoolyFetch(t *testing.T) {
	p := woolyPoolyServer(t)
	offers, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// FIRO has nobody mining for a share, XNA is not in the answer
	if len(offers) != 2 || offers[0].Coin != "ETC" || offers[1].Coin != "RVN" {
		t.Fatalf("offers = %+v", offers)
	}
	rvn := offers[1]
	if rvn.Pool != "woolypooly" || rvn.Algorithm != "kawpow" || rvn.Host != "pool.woolypooly.com" || rvn.Port != 55555 || rvn.TLSPort != 55555 ||
		rvn.User != "RAddr1.rig1" || rvn.Pass != "x" || rvn.EthMode != "" {
		t.Errorf("offer = %+v", rvn)
	}
	if want := 2.4622e-08 * 1250 * 1440 / 669342432690.7031 * 0.991; !near(rvn.Price, want) || rvn.FeePercent != 0.9 {
		t.Errorf("price = %g, want %g, fee %v", rvn.Price, want, rvn.FeePercent)
	}
	// only what is mined for a share counts, not the solo miners
	if rvn.Hashrate != 6007017984 || rvn.Workers != 94 || !near(rvn.Blocks24h, 1440*6007017984/669342432690.7031) {
		t.Errorf("offer = %+v", rvn)
	}
	// Ethash pools have dialects, and the fee is the coin's
	etc := offers[0]
	if etc.EthMode != "qtminer" || etc.FeePercent != 0.5 || !near(etc.Price, 0.0001003*1.6385609772947838*(86400/15.8)/126754838882092.11*0.995) {
		t.Errorf("etc = %+v", etc)
	}

	p.ExcludeCoins = []string{"ETC", "RVN"}
	if offers, err := p.Fetch(context.Background()); err != nil || len(offers) != 0 {
		t.Errorf("all excluded: %+v, %v", offers, err)
	}
	p.Wallets = map[string]string{"DOGE": "D"}
	if _, err := p.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "no wallet") {
		t.Errorf("without wallets: %v", err)
	}
	p = woolyPoolyServer(t)
	p.Prices = func(context.Context, []string) (map[string]float64, error) { return nil, errors.New("status 429") }
	if _, err := p.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("price outage: %v", err)
	}
	p = woolyPoolyServer(t)
	p.API += "/nothing"
	if _, err := p.Fetch(context.Background()); err == nil {
		t.Error("an outage of the pool went unnoticed")
	}
}

func TestWoolyPoolyBalances(t *testing.T) {
	p := woolyPoolyServer(t)
	balances, err := p.Balances(context.Background())
	// ETC answers without figures, XNA is unknown, FIRO fails
	if err == nil || !strings.Contains(err.Error(), "FIRO") || len(balances) != 1 {
		t.Fatalf("balances = %+v, %v", balances, err)
	}
	b := balances[0]
	if b.Pool != "woolypooly" || b.Currency != "RVN" || b.Balance != 12.5 || b.Unsold != 2.5 || b.Unpaid != 15 || b.Total != 1015 || b.Paid24h != 60.5 {
		t.Errorf("balance = %+v", b)
	}
	// payments are read whatever their fields are called; one without
	// amount and time is left out
	if len(b.Payouts) != 2 || b.Payouts[0].Tx != "new" || b.Payouts[1].Amount != 50 || b.Payouts[1].Time.Unix() != 1000 {
		t.Errorf("payouts = %+v", b.Payouts)
	}
}

func TestCoinPoolRegistry(t *testing.T) {
	for _, info := range CoinPools() {
		p := NewCoinPool(info.Name, CoinPoolSettings{Wallets: map[string]string{"RVN": "R"}, Worker: "w", Region: info.Regions[0]})
		if p == nil || p.Name() != info.Name || info.Key == "" || info.Title == "" || len(info.Coins) == 0 || info.FeePercent < 0 {
			t.Errorf("pool %+v is not complete", info)
		}
		for _, c := range info.Coins {
			if c.Symbol == "" || c.Name == "" || c.Algorithm == "" {
				t.Errorf("%s: coin %+v is not complete", info.Name, c)
			}
		}
	}
	if NewCoinPool("nopool", CoinPoolSettings{}) != nil {
		t.Error("a pool nobody knows")
	}
}
