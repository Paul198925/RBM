package pool

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// What the pool answered in October 2026, shortened to what is read and to
// one coin that is not Monero.
const moneroOceanStatsDoc = `{"pool_statistics": {"hashRate": 232560048.44, "miners": 4675, "lastBlockFoundTime": 1791895673,
	"price": {"btc": 0.006335832188590185, "usd": 527.42, "eur": 471.18},
	"coins": {
		"9998": {"port": 9998, "symbol": "RTM", "algo": "ghostrider", "active": true, "profit": 4.9e-08, "hashrate": 288077.89, "miners": 73},
		"18081": {"port": 18081, "symbol": "XMR", "displayName": "XMR", "algo": "rx/0", "active": true, "profit": 7.233568427305283e-08,
			"comment": "", "hashrate": 215752669.63, "miners": 4506, "blockTime": 120, "atomicUnits": 1000000000000}}}}`

func moneroOceanStub(t *testing.T, replies map[string]string) *MoneroOcean {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply, ok := replies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return &MoneroOcean{API: srv.URL, Worker: "rig1", Wallets: map[string]string{"XMR": " 44TestWallet ", "RVN": "RWallet"}}
}

func TestMoneroOceanOffer(t *testing.T) {
	m := moneroOceanStub(t, map[string]string{"/pool/stats": moneroOceanStatsDoc})
	offers, err := m.Fetch(context.Background())
	if err != nil || len(offers) != 1 {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	o := offers[0]
	// what a hash per second earns in a day in XMR, at the pool's price of it
	want := 7.233568427305283e-08 * 0.006335832188590185
	if math.Abs(o.Price-want) > want*1e-9 {
		t.Errorf("price = %v, want %v", o.Price, want)
	}
	if o.Pool != "moneroocean" || o.Algorithm != "randomx" || o.Coin != "XMR" || o.Host != "gulf.moneroocean.stream" ||
		o.Port != 10128 || o.TLSPort != 20128 || o.FeePercent != 0 || o.Workers != 4506 || o.Hashrate < 2e8 {
		t.Errorf("offer = %+v", o)
	}
	// the account is the address, the rig's name goes where the password is
	if o.User != "44TestWallet" || o.Pass != "rig1" {
		t.Errorf("user %q, pass %q", o.User, o.Pass)
	}

	// no wallet for Monero, or Monero switched off: nothing to mine there
	m.Wallets = map[string]string{"RVN": "RWallet"}
	if _, err := m.Fetch(context.Background()); err == nil {
		t.Error("an offer without an XMR wallet")
	}
	m.Wallets = map[string]string{"XMR": "44TestWallet"}
	m.ExcludeCoins = []string{"XMR"}
	if _, err := m.Fetch(context.Background()); err == nil {
		t.Error("an offer although XMR is not to be mined")
	}

	// a pool that is down, or that does not mine Monero right now
	m.ExcludeCoins = nil
	down := moneroOceanStub(t, nil)
	if _, err := down.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("a pool that is down: %v", err)
	}
	idle := moneroOceanStub(t, map[string]string{"/pool/stats": strings.Replace(moneroOceanStatsDoc, `"algo": "rx/0", "active": true`, `"algo": "rx/0", "active": false`, 1)})
	if offers, err := idle.Fetch(context.Background()); err != nil || len(offers) != 0 {
		t.Errorf("Monero not active: %+v, %v", offers, err)
	}
}

func TestMoneroOceanBalance(t *testing.T) {
	recent := time.Now().Add(-2 * time.Hour).Unix()
	m := moneroOceanStub(t, map[string]string{
		"/miner/44TestWallet/stats": `{"hash":0,"hash2":0,"identifier":"global","lastHash":1648589054,"lastShareAlgo":false,
			"totalHashes":957282516.79,"validShares":46370,"invalidShares":201,"amtPaid":3081048137,"amtDue":629028991,"txnCount":1}`,
		"/miner/44TestWallet/payments": `[{"pt":"pplns","ts":1621487822,"amount":3081048137,"txnHash":"3857f1","mixin":10},
			{"pt":"pplns","ts":` + itoa(recent) + `,"amount":5000000000,"txnHash":"abcd"}]`,
	})
	balances, err := m.Balances(context.Background())
	if err != nil || len(balances) != 1 {
		t.Fatalf("balances = %+v, %v", balances, err)
	}
	b := balances[0]
	if b.Pool != "moneroocean" || b.Currency != "XMR" || math.Abs(b.Unpaid-0.000629028991) > 1e-15 || math.Abs(b.Total-0.003710077128) > 1e-15 {
		t.Errorf("balance = %+v", b)
	}
	if len(b.Payouts) != 2 || b.Payouts[0].Tx != "abcd" || math.Abs(b.Paid24h-0.005) > 1e-15 {
		t.Errorf("payouts = %+v, paid in a day %v", b.Payouts, b.Paid24h)
	}

	// an address the pool has never seen: it answers with zeros
	unknown := moneroOceanStub(t, map[string]string{"/miner/44TestWallet/stats": `{"hash":false,"totalHashes":false,"amtPaid":0,"amtDue":0,"txnCount":0}`})
	if balances, err := unknown.Balances(context.Background()); err != nil || len(balances) != 0 {
		t.Errorf("an unknown wallet: %+v, %v", balances, err)
	}
	// the balance is shown even when the payments cannot be read
	half := moneroOceanStub(t, map[string]string{"/miner/44TestWallet/stats": `{"totalHashes":5,"amtPaid":0,"amtDue":1000000000000}`})
	balances, err = half.Balances(context.Background())
	if err == nil || len(balances) != 1 || balances[0].Unpaid != 1 {
		t.Errorf("without payments: %+v, %v", balances, err)
	}
	// no wallet, no question
	m.Wallets = nil
	if balances, err := m.Balances(context.Background()); err != nil || balances != nil {
		t.Errorf("without a wallet: %+v, %v", balances, err)
	}
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{digits[n%10]}, b...)
	}
	return string(b)
}
