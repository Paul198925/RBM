package device

import "testing"

func TestParseTelemetry(t *testing.T) {
	out := "0, 61, 73, 24.22, 100.00, 17, 1105, 210, 405, 170.00\r\n" +
		"1, 88, [N/A], 187.80, 250.00, 100, 6890, 1260, 7500, 250.00\r\n" +
		"garbage line\r\n" +
		"x, 1, 2, 3, 4, 5, 6, 7, 8, 9\r\n"
	got := parseTelemetry(out)
	if len(got) != 2 {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
	want0 := Telemetry{TemperatureC: 61, FanPercent: 73, PowerWatts: 24.22, PowerLimitWatts: 100, PowerDefaultWatts: 170,
		UtilizationPercent: 17, MemoryUsedMiB: 1105, ClockMHz: 210, MemoryClockMHz: 405}
	if got[0] != want0 {
		t.Errorf("gpu 0 = %+v", got[0])
	}
	// a card that does not report its fan still gives the rest
	if got[1].FanPercent != 0 || got[1].TemperatureC != 88 || got[1].PowerWatts != 187.8 {
		t.Errorf("gpu 1 = %+v", got[1])
	}
}
