package device

import "testing"

func TestParseNvidiaGPUs(t *testing.T) {
	out := "0, NVIDIA GeForce RTX 3060, 12288, GPU-11111111-2222-3333-4444-555555555555, 00000000:01:00.0\r\n" +
		"1, NVIDIA GeForce RTX 2080 SUPER, 8192, GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee, 00000000:0A:00.0\r\n\r\n"
	got, err := parseNvidiaGPUs(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []Device{
		{ID: "GPU#00", Kind: GPU, Vendor: "NVIDIA", Model: "GeForce RTX 3060", Index: 0, MemoryMiB: 12288, UUID: "GPU-11111111-2222-3333-4444-555555555555", PCI: "01:00"},
		{ID: "GPU#01", Kind: GPU, Vendor: "NVIDIA", Model: "GeForce RTX 2080 SUPER", Index: 1, MemoryMiB: 8192, UUID: "GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", PCI: "0a:00"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d devices", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("device %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseNvidiaGPUsRejectsGarbage(t *testing.T) {
	for _, out := range []string{"NVIDIA-SMI has failed", "x, name, 12288, uuid, 00000000:01:00.0", "0, name, [N/A], uuid, 00000000:01:00.0", "0, name, 12288, uuid"} {
		if _, err := parseNvidiaGPUs(out); err == nil {
			t.Errorf("%q: no error", out)
		}
	}
}

func TestPCIAddress(t *testing.T) {
	for in, want := range map[string]string{
		"00000000:01:00.0": "01:00",
		"0000:2B:00.1":     "2b:00",
		"[N/A]":            "",
		"":                 "",
		"0000:1:00.0":      "",
		"0000:zz:00.0":     "",
	} {
		if got := pciAddress(in); got != want {
			t.Errorf("pciAddress(%q) = %q, want %q", in, got, want)
		}
	}
	// a card without an address is still a card
	got, err := parseNvidiaGPUs("0, NVIDIA GeForce RTX 3060, 12288, GPU-1, [N/A]")
	if err != nil || len(got) != 1 || got[0].PCI != "" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestParseNoGPUs(t *testing.T) {
	got, err := parseNvidiaGPUs("\n")
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}
