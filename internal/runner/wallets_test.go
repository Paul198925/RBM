package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hashsteer/internal/wallet"
)

func TestWalletBalancesAreAskedFor(t *testing.T) {
	app, _ := testApp(t)
	ctx := context.Background()
	var asked []string
	var fail error
	app.walletFn = func(_ context.Context, currency, address string) (wallet.Balance, error) {
		asked = append(asked, currency+":"+address)
		if fail != nil {
			return wallet.Balance{}, fail
		}
		return wallet.Balance{Currency: currency, Address: address, Balance: float64(len(asked))}, nil
	}

	// no wallet, nobody is asked
	app.readWallets(ctx)
	if b := app.Balances(); len(asked) != 0 || b.Wallets == nil || len(b.Wallets) != 0 {
		t.Fatalf("asked %v, wallets %+v", asked, b.Wallets)
	}

	// a coin without a known explorer is left out; the others are asked
	// once, in the order of their names
	app.cfg.Pools.Wallets = map[string]string{"RVN": " RWallet1 ", "BTC": "bc1wallet", "NEXA": "nexa:wallet", "LTC": ""}
	app.readWallets(ctx)
	app.readWallets(ctx)
	if got := strings.Join(asked, " "); got != "BTC:bc1wallet RVN:RWallet1" {
		t.Fatalf("asked %q", got)
	}
	b := app.Balances()
	if len(b.Wallets) != 2 || b.Wallets[0].Currency != "BTC" || b.Wallets[1].Balance != 2 || b.WalletError != "" {
		t.Fatalf("wallets %+v, error %q", b.Wallets, b.WalletError)
	}

	// later they are asked again; a failure keeps the old figures
	app.walletAt = time.Now().Add(-balanceInterval - time.Second)
	fail = errors.New("RVN balance from blockbook.ravencoin.org: status 502")
	app.readWallets(ctx)
	b = app.Balances()
	if len(asked) != 4 || len(b.Wallets) != 2 || b.Wallets[1].Balance != 2 || !strings.Contains(b.WalletError, "502") {
		t.Errorf("asked %v, wallets %+v, error %q", asked, b.Wallets, b.WalletError)
	}

	// another address is asked for at once, and the old balance is not its
	fail = nil
	app.cfg.Pools.Wallets = map[string]string{"RVN": "RWallet2"}
	app.readWallets(ctx)
	b = app.Balances()
	if len(asked) != 5 || len(b.Wallets) != 1 || b.Wallets[0].Address != "RWallet2" {
		t.Errorf("asked %v, wallets %+v", asked, b.Wallets)
	}

	// switched off: nobody is asked and nothing is shown any more
	app.cfg.Pools.NoWalletBalances = true
	app.readWallets(ctx)
	if b = app.Balances(); len(asked) != 5 || len(b.Wallets) != 0 {
		t.Errorf("asked %v, wallets %+v", asked, b.Wallets)
	}
}
