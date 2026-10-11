package miner

import (
	"context"
	"strings"
	"testing"
)

func TestBzminerPoll(t *testing.T) {
	api, err := NewAPI("bzminer")
	if err != nil {
		t.Fatal(err)
	}
	// the CPU and a card that is left out are listed too
	status := `{"devices": [
		{"name": "CPU", "status": [0], "hashrate": [0], "valid_solutions": [0], "rejected_solutions": [0], "stale_solutions": [0], "power": 65},
		{"name": "RTX 3060", "status": [3], "hashrate": [120500000], "valid_solutions": [20], "rejected_solutions": [1], "stale_solutions": [2], "power": 110},
		{"name": "RTX 2080 SUPER", "status": [3], "hashrate": [131000000], "valid_solutions": [25], "rejected_solutions": [0], "stale_solutions": [0], "power": 150.5},
		{"name": "GTX 1050", "status": [0], "hashrate": [], "power": 30}]}`
	sample, err := api.Poll(context.Background(), serveMiner(t, "/status", status))
	if err != nil {
		t.Fatal(err)
	}
	want := AlgoSample{Hashrate: 251.5e6, Accepted: 45, Rejected: 1, Invalid: 2}
	if sample.For("ergo") != want || sample.PowerWatts != 260.5 {
		t.Errorf("sample = %+v, power %v", sample.For("ergo"), sample.PowerWatts)
	}
	if sample, err := api.Poll(context.Background(), serveMiner(t, "/status", `{"devices": []}`)); err != nil || sample.For("x").Hashrate != 0 {
		t.Errorf("starting miner: %+v, %v", sample, err)
	}
}

func TestBzminerAndCpuminerDefinitions(t *testing.T) {
	bz := builtin1(t, "bzminer")
	ergo, ok := bz.Algorithm("autolykos2")
	if !ok || ergo.NameForMiner() != "ergo" || bz.FeePercent(ergo) != 1 {
		t.Fatalf("autolykos2 = %+v, %v", ergo, ok)
	}
	job := Job{Algorithm: ergo, DeviceIndexes: []int{0, 2}, DevicePCI: []string{"01:00", "0a:00"}, APIPort: 4500,
		Host: "erg.2miners.com", Port: 8888, User: "9addr.rig1", Pass: "x"}
	args, err := bz.BuildArgs(job)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	// the cards are named by where they sit, not by number
	for _, want := range []string{"-a ergo", "--nvidia 1", "--cpu 0", "--devices 01:00,0a:00", "-p stratum+tcp://erg.2miners.com:8888",
		"-u 9addr.rig1", "--pass x", "--config config_4500.txt", "--http_port 4500", "--no-watchdog", "--disable_udp"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is missing in %q", want, line)
		}
	}
	// without the address of every card it cannot be started
	for _, pci := range [][]string{nil, {"01:00"}, {"01:00", ""}} {
		job.DevicePCI = pci
		if _, err := bz.BuildArgs(job); err == nil || !strings.Contains(err.Error(), "PCI address") {
			t.Errorf("addresses %q: %v", pci, err)
		}
	}
	// a miner that goes by numbers does not need them
	rigel := builtin1(t, "rigel")
	kawpow, _ := rigel.Algorithm("kawpow")
	if _, err := rigel.BuildArgs(Job{Algorithm: kawpow, DeviceIndexes: []int{0}, Host: "h", Port: 1}); err != nil {
		t.Errorf("rigel without addresses: %v", err)
	}

	etchash, _ := bz.Algorithm("etchash")
	args, _ = bz.BuildArgs(Job{Algorithm: etchash, DeviceIndexes: []int{0}, DevicePCI: []string{"01:00"}, Host: "etc.2miners.com", Port: 11010, TLS: true, EthMode: "ethproxy"})
	if line := strings.Join(args, " "); !strings.Contains(line, "-p ethproxy+ssl://etc.2miners.com:11010") || bz.FeePercent(etchash) != 0.5 {
		t.Errorf("etchash: %s", line)
	}

	cpu := builtin1(t, "cpuminer-opt")
	if cpu.Vendor != "CPU" || cpu.API != "ccminer" {
		t.Errorf("cpuminer-opt = %+v", cpu)
	}
	yespower, ok := cpu.Algorithm("yespowerr16")
	if !ok {
		t.Fatal("cpuminer-opt does not list yespowerr16")
	}
	args, err = cpu.BuildArgs(Job{Algorithm: yespower, APIPort: 4501, Host: "yespowerr16.eu.mine.zpool.ca", Port: 6534, User: "wallet1", Pass: "rig1,c=BTC", Threads: 8})
	if err != nil {
		t.Fatal(err)
	}
	line = strings.Join(args, " ")
	for _, want := range []string{"-a yespowerr16", "-o stratum+tcp://yespowerr16.eu.mine.zpool.ca:6534", "-u wallet1", "-p rig1,c=BTC", "-b 127.0.0.1:4501", "-t 8"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is missing in %q", want, line)
		}
	}
	// without a limit the miner picks the number of threads
	args, _ = cpu.BuildArgs(Job{Algorithm: yespower, APIPort: 4501, Host: "h", Port: 1})
	if strings.Contains(strings.Join(args, " "), "-t ") {
		t.Errorf("threads without a limit: %q", args)
	}
}
