package pool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func walletServer(t *testing.T, body string, asked *string) *Zpool {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if asked != nil {
			*asked = r.URL.Query().Get("address")
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Zpool{Wallet: "RWallet1", Currency: "RVN", Worker: "rig1", Region: "eu", WalletURL: srv.URL}
}

func TestZpoolBalance(t *testing.T) {
	var asked string
	z := walletServer(t, `{"currency": "RVN", "unsold": 12.5, "balance": "152.09", "unpaid": 164.59, "paid24h": 217.2, "paidtotal": 727.8, "no_fees": 0, "minpay": 0.05,
		"miners": [{"version": "rigel", "algo": "kawpow"}],
		"payouts": [
			{"time": "1791693259", "amount": "56.68336412", "tx": "7c66"},
			{"time": 1791707659, "amount": 55.66918293, "tx": "23b3"}
		]}`, &asked)
	b, err := z.Balance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if asked != "RWallet1" {
		t.Errorf("pool was asked for %q", asked)
	}
	if b.Pool != "zpool" || b.Currency != "RVN" || b.Balance != 152.09 || b.Unsold != 12.5 || b.Unpaid != 164.59 || b.Paid24h != 217.2 || b.Total != 727.8 {
		t.Errorf("balance = %+v", b)
	}
	// newest payout first, whatever the order and the types in the answer
	if len(b.Payouts) != 2 || b.Payouts[0].Tx != "23b3" || b.Payouts[0].Amount != 55.66918293 || b.Payouts[1].Time.Unix() != 1791693259 {
		t.Errorf("payouts = %+v", b.Payouts)
	}
	if b.Updated.IsZero() {
		t.Error("no update time")
	}
}

func TestZpoolBalanceErrors(t *testing.T) {
	// a wallet the pool has not seen yet
	if _, err := walletServer(t, `{"currency": "", "unsold": 0, "balance": 0}`, nil).Balance(context.Background()); err == nil {
		t.Error("no error for an unknown wallet")
	}
	if _, err := walletServer(t, `<html>busy</html>`, nil).Balance(context.Background()); err == nil {
		t.Error("no error for an answer that is not json")
	}
	z := walletServer(t, `{}`, nil)
	z.Wallet = ""
	if _, err := z.Balance(context.Background()); err == nil {
		t.Error("no error without a wallet")
	}
	// a wallet without payouts gives an empty list, not none
	b, err := walletServer(t, `{"currency": "BTC", "balance": 0.0001}`, nil).Balance(context.Background())
	if err != nil || b.Payouts == nil || len(b.Payouts) != 0 {
		t.Errorf("balance = %+v, %v", b, err)
	}
}
