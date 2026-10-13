package pool

import (
	"encoding/json"
	"testing"
)

// The pools write numbers as numbers, as text, and now and then as
// something else where there is nothing to say.
func TestFlexFloat(t *testing.T) {
	var got struct {
		A, B, C, D, E, F, G flexFloat
	}
	doc := `{"A": 1.5, "B": "2.5", "C": null, "D": "", "E": false, "F": true, "G": "1e3"}`
	if err := json.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatal(err)
	}
	if got.A != 1.5 || got.B != 2.5 || got.C != 0 || got.D != 0 || got.E != 0 || got.F != 1 || got.G != 1000 {
		t.Errorf("got %+v", got)
	}
	if json.Unmarshal([]byte(`{"A": "many"}`), &got) == nil {
		t.Error("text that is no number was taken for one")
	}

	// one coin with such a field does not spoil the list
	var coins map[string]zpoolCoin
	list := `{"RVN": {"name": "Ravencoin", "algo": "kawpow", "port": 1325, "minimum_payout": false, "conversion_disabled": "0"},
		"LTC": {"name": "Litecoin", "algo": "scrypt", "port": "3433", "minimum_payout": 0.01, "conversion_disabled": false}}`
	if err := json.Unmarshal([]byte(list), &coins); err != nil {
		t.Fatal(err)
	}
	if len(coins) != 2 || coins["RVN"].MinimumPayout != 0 || coins["LTC"].MinimumPayout != 0.01 || coins["LTC"].ConversionDisabled != 0 {
		t.Errorf("coins = %+v", coins)
	}
}
