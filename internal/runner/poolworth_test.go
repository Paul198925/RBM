package runner

import (
	"context"
	"testing"

	"hashsteer/internal/pool"
)

func TestPoolBalancesGetAWorth(t *testing.T) {
	app, _ := testApp(t)
	balances := []pool.Balance{
		{Pool: "zpool", Currency: "RVN", Unpaid: 300},
		{Pool: "2miners", Currency: "ERG", Unpaid: 2},
		{Pool: "zpool", Currency: "BTC", Unpaid: 0.001},
		{Pool: "zpool", Currency: "NOPE", Unpaid: 5},
	}

	// without a price source nothing is worth anything but bitcoin
	app.valueBalances(context.Background(), balances)
	if balances[0].ValueBTC != 0 || balances[2].ValueBTC != 0.001 {
		t.Errorf("without prices: %+v", balances)
	}

	var asked []string
	app.walletPriceFn = func(_ context.Context, ids []string) (map[string]float64, error) {
		asked = ids
		// the price of ergo is not known right now
		return map[string]float64{"ravencoin": 0.00000002}, nil
	}
	app.valueBalances(context.Background(), balances)
	if len(asked) != 2 || asked[0] != "ravencoin" || asked[1] != "ergo" {
		t.Errorf("asked for %v", asked)
	}
	if balances[0].ValueBTC != 300*0.00000002 || balances[1].ValueBTC != 0 || balances[2].ValueBTC != 0.001 || balances[3].ValueBTC != 0 {
		t.Errorf("worth: %+v", balances)
	}
}
