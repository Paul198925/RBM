package wallet

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// What the explorers answered when they were asked in October 2026,
// shortened.
var replies = map[string]string{
	"/address/bc1qtest":                     `{"address":"bc1qtest","chain_stats":{"funded_txo_count":3,"funded_txo_sum":5758459553,"spent_txo_count":1,"spent_txo_sum":758459553,"tx_count":4},"mempool_stats":{"funded_txo_count":2,"funded_txo_sum":4218,"spent_txo_count":0,"spent_txo_sum":0,"tx_count":2}}`,
	"/address/Rtest?details=basic":          `{"address":"Rtest","balance":"1230577094080288","totalReceived":"1230577094080288","totalSent":"0","unconfirmedBalance":"-150000000","unconfirmedTxs":1,"txs":24629}`,
	"/addrs/Dtest/balance":                  `{"address":"Dtest","total_received":4385334472418486462,"total_sent":4385328250307366598,"balance":6222111119864,"unconfirmed_balance":0,"final_balance":6222111119864,"n_tx":1484}`,
	"/addresses/0xtest":                     `{"block_number_balance_updated_at":25522449,"coin_balance":"1411352811853608741","creation_status":null,"exchange_rate":"8.06"}`,
	"/addresses/9test/balance/total":        `{"confirmed":{"nanoErgs":2500000000,"tokens":[]},"unconfirmed":{"nanoErgs":100000000,"tokens":[]}}`,
	"/addr/atest?noTxList=1":                `{"addrStr":"atest","balance":40,"balanceSat":4000000000,"totalReceived":40,"totalReceivedSat":4000000000,"unconfirmedBalance":null,"txApperances":2}`,
	"/address/unknown-to-blockbook?details": `{"error":"Invalid address, checksum mismatch"}`,
}

func explorerStub(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "hashsteer" {
			t.Errorf("asked as %q", r.Header.Get("User-Agent"))
		}
		reply, ok := replies[r.URL.RequestURI()]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"Invalid address, checksum mismatch"}`)
			return
		}
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestBalanceInEveryDialect(t *testing.T) {
	r := &Reader{Base: explorerStub(t)}
	for _, c := range []struct {
		coin, address    string
		balance, pending float64
		explorer         string
	}{
		{"BTC", "bc1qtest", 50, 0.00004218, "mempool.space"},
		{"rvn", " Rtest ", 12305770.94080288, -1.5, "blockbook.ravencoin.org"},
		{"DOGE", "Dtest", 62221.11119864, 0, "api.blockcypher.com"},
		{"ETC", "0xtest", 1.411352811853608741, 0, "etc.blockscout.com"},
		{"ERG", "9test", 2.5, 0.1, "api.ergoplatform.com"},
		{"FIRO", "atest", 40, 0, "explorer.firo.org"},
	} {
		b, err := r.Balance(context.Background(), c.coin, c.address)
		if err != nil {
			t.Errorf("%s: %v", c.coin, err)
			continue
		}
		if math.Abs(b.Balance-c.balance) > 1e-9 || math.Abs(b.Pending-c.pending) > 1e-9 {
			t.Errorf("%s: balance %v pending %v, want %v and %v", c.coin, b.Balance, b.Pending, c.balance, c.pending)
		}
		if b.Currency != strings.ToUpper(c.coin) || b.Address != strings.TrimSpace(c.address) || b.Explorer != c.explorer || time.Since(b.Updated) > time.Minute {
			t.Errorf("%s: %+v", c.coin, b)
		}
	}
}

func TestBalanceThatCannotBeRead(t *testing.T) {
	r := &Reader{Base: explorerStub(t)}
	ctx := context.Background()
	if _, err := r.Balance(ctx, "NOPE", "x"); err == nil || !strings.Contains(err.Error(), "no block explorer is known for NOPE") {
		t.Errorf("an unknown coin: %v", err)
	}
	if _, err := r.Balance(ctx, "BTC", " "); err == nil {
		t.Error("no address, and no error")
	}
	// the explorer says what is wrong with the address, and that is passed on
	_, err := r.Balance(ctx, "RVN", "notanaddress")
	if err == nil || !strings.Contains(err.Error(), "RVN balance from blockbook.ravencoin.org") || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("a wrong address: %v", err)
	}
	// an answer without a balance is no balance of zero
	replies["/address/Xempty?details=basic"] = `{"address":"Xempty"}`
	if _, err := r.Balance(ctx, "XNA", "Xempty"); err == nil {
		t.Error("an answer without a balance was taken for zero")
	}
	// an explorer that does not answer: the address stays out of the error
	dead := &Reader{Base: "http://127.0.0.1:1"}
	_, err = dead.Balance(ctx, "BTC", "bc1qsecretaddress")
	if err == nil || strings.Contains(err.Error(), "bc1qsecretaddress") {
		t.Errorf("an explorer that is down: %v", err)
	}
}

func TestSupported(t *testing.T) {
	coins := Supported()
	if strings.Join(coins, " ") != "BTC DOGE ERG ETC ETHW FIRO LTC MEWC NEXA RVN RXD XNA" {
		t.Errorf("supported: %v", coins)
	}
	if !Known("rvn") || !Known("NEXA") || Known("XMR") {
		t.Error("Known is off")
	}
}

// TestLive asks the real explorers for well-known public addresses. It
// only runs when asked for: HASHSTEER_WALLET_LIVE=1.
func TestLive(t *testing.T) {
	if os.Getenv("HASHSTEER_WALLET_LIVE") == "" {
		t.Skip("set HASHSTEER_WALLET_LIVE=1 to ask the real explorers")
	}
	r := &Reader{}
	addresses := map[string]string{}
	// no NEXA address is well known enough to stand here: name one
	if nexa := os.Getenv("HASHSTEER_WALLET_LIVE_NEXA"); nexa != "" {
		addresses["NEXA"] = nexa
	}
	for coin, address := range map[string]string{
		"BTC":  "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		"LTC":  "LQTpS3VaYTjCr4s9Y1t5zbeY26zevf7Fb3",
		"MEWC": "MPyNGZSSZ4rbjkVJRLn3v64pMcktpEYJnU",
		"DOGE": "DH5yaieqoZN36fDVciNyRueRGvGLR3mr7L",
		"RVN":  "RXissueAssetXXXXXXXXXXXXXXXXXhhZGt",
		"ETC":  "0x000000000000000000000000000000000000dEaD",
		"ERG":  "9hY16vzHmmfyVBwKeFGHvb2bMFsG94A1u7To1QWtUokACyFVENQ",
		"FIRO": "aEF2p3jepoWF2yRYZjb6EACCP4CaP41doV",
		"ETHW": "0x000000000000000000000000000000000000dEaD",
		"RXD":  "1BitcoinEaterAddressDontSendf59kuE",
	} {
		addresses[coin] = address
	}
	for coin, address := range addresses {
		b, err := r.Balance(context.Background(), coin, address)
		if err != nil {
			t.Errorf("%s: %v", coin, err)
			continue
		}
		t.Logf("%-4s %18.8f  pending %g  (%s)", coin, b.Balance, b.Pending, b.Explorer)
	}
}
