package sysinfo

import "testing"

func TestReadMemory(t *testing.T) {
	m, err := ReadMemory()
	if err != nil {
		t.Fatal(err)
	}
	// any machine that runs this test has these
	if m.TotalMiB < 256 || m.FreeMiB <= 0 || m.FreeMiB > m.TotalMiB {
		t.Errorf("ram: %+v", m)
	}
	if m.CommitLimitMiB < m.TotalMiB*0.5 || m.CommitFreeMiB <= 0 || m.CommitFreeMiB > m.CommitLimitMiB {
		t.Errorf("commit: %+v", m)
	}
}
