package runner

import "testing"

func TestFormatHashrate(t *testing.T) {
	cases := map[float64]string{
		0:        "0.00 H/s",
		999:      "999.00 H/s",
		1000:     "1.00 kH/s",
		52.3e6:   "52.30 MH/s",
		4.228e9:  "4.23 GH/s",
		8.786e11: "878.60 GH/s",
		3e18:     "3000.00 PH/s",
	}
	for h, want := range cases {
		if got := FormatHashrate(h); got != want {
			t.Errorf("FormatHashrate(%g) = %q, want %q", h, got, want)
		}
	}
}
