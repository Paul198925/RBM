package runner

import (
	"slices"
	"testing"

	"hashsteer/internal/pool"
)

// A coin that is switched off for all pools is offered by none of them,
// next to what each pool's own list takes out.
func TestCoinsSwitchedOffForAllPools(t *testing.T) {
	app, _ := testApp(t)
	cfg := app.Config()
	cfg.Pools.Wallets = map[string]string{"RVN": "RWallet", "ERG": "9wallet", "XNA": "Nwallet"}
	cfg.Pools.TwoMiners.Enabled = true
	cfg.Pools.TwoMiners.ExcludeCoins = []string{"XNA"}
	cfg.Pools.ExcludeCoins = []string{"erg"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Pools.ExcludeCoins, []string{"ERG"}) {
		t.Errorf("switched off: %v", cfg.Pools.ExcludeCoins)
	}
	pools := app.coinPools(&cfg)
	if len(pools) != 1 {
		t.Fatalf("%d coin pools", len(pools))
	}
	two, ok := pools[0].(*pool.TwoMiners)
	if !ok {
		t.Fatalf("the pool is a %T", pools[0])
	}
	if !slices.Equal(two.ExcludeCoins, []string{"XNA", "ERG"}) {
		t.Errorf("2miners leaves out %v, want its own XNA and ERG from the switch for all pools", two.ExcludeCoins)
	}
}
