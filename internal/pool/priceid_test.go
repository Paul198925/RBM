package pool

import "testing"

func TestPriceID(t *testing.T) {
	for symbol, want := range map[string]string{
		"RVN": "ravencoin", "rvn": "ravencoin", "FIRO": "zcoin", "RXD": "radiant", "LTC": "litecoin", "DOGE": "dogecoin",
		"BTC": "", "NOPE": "",
	} {
		if got := PriceID(symbol); got != want {
			t.Errorf("PriceID(%q) = %q, want %q", symbol, got, want)
		}
	}
}
