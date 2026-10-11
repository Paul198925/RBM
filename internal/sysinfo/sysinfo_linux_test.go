package sysinfo

import "testing"

func TestParseMeminfo(t *testing.T) {
	m, err := parseMeminfo("MemTotal:       32000000 kB\nMemFree:         1000000 kB\nMemAvailable:    8192000 kB\n" +
		"SwapTotal:       4096000 kB\nSwapFree:        2048000 kB\nHugePages_Total:       0\n")
	if err != nil {
		t.Fatal(err)
	}
	if m.TotalMiB != 31250 || m.FreeMiB != 8000 || m.CommitLimitMiB != 35250 || m.CommitFreeMiB != 10000 {
		t.Errorf("memory = %+v", m)
	}
	if _, err := parseMeminfo("nothing useful"); err == nil {
		t.Error("no error for a file without MemTotal")
	}
}
