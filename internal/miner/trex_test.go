package miner

import (
	"context"
	"strings"
	"testing"
)

func TestTrexPoll(t *testing.T) {
	api, err := NewAPI("trex")
	if err != nil {
		t.Fatal(err)
	}
	// shortened from the example in the program's API documentation
	summary := `{"accepted_count": 6, "rejected_count": 1, "invalid_count": 2, "solved_count": 0, "algorithm": "kawpow",
		"active_pool": {"url": "stratum+tcp://rvn.2miners.com:6060", "user": "wallet.rig"},
		"gpus": [{"device_id": 0, "hashrate": 22000000, "power": 120, "shares": {"accepted_count": 3}},
		         {"device_id": 1, "hashrate": 23290540, "power": 151, "shares": {"accepted_count": 3}}],
		"hashrate": 45290540, "hashrate_minute": 45000000, "uptime": 108}`
	sample, err := api.Poll(context.Background(), serveMiner(t, "/summary", summary))
	if err != nil {
		t.Fatal(err)
	}
	want := AlgoSample{Hashrate: 45290540, Accepted: 6, Rejected: 1, Invalid: 2}
	if sample.For("kawpow") != want || sample.PowerWatts != 271 {
		t.Errorf("sample = %+v, power %v", sample.For("kawpow"), sample.PowerWatts)
	}
	if sample, err := api.Poll(context.Background(), serveMiner(t, "/summary", `{"gpus": []}`)); err != nil || sample.For("x").Hashrate != 0 {
		t.Errorf("starting miner: %+v, %v", sample, err)
	}
}

func TestTrexDefinition(t *testing.T) {
	trex := builtin1(t, "trex")
	ergo, ok := trex.Algorithm("autolykos2")
	if !ok || trex.FeePercent(ergo) != 2 {
		t.Fatalf("autolykos2 = %+v, %v", ergo, ok)
	}
	etchash, _ := trex.Algorithm("etchash")
	if trex.FeePercent(etchash) != 1 {
		t.Errorf("etchash fee = %v", trex.FeePercent(etchash))
	}
	// the pools' dialects need no word: the program's own scripts connect
	// to both kinds of pool the same way
	for _, mode := range []string{"ethproxy", "qtminer"} {
		args, err := trex.BuildArgs(Job{Algorithm: etchash, DeviceIndexes: []int{0, 1}, APIPort: 4500, Host: "etc.2miners.com", Port: 1010,
			User: "0xabc.rig1", Pass: "x", EthMode: mode})
		if err != nil {
			t.Fatal(err)
		}
		line := strings.Join(args, " ")
		for _, want := range []string{"-a etchash", "-o stratum+tcp://etc.2miners.com:1010", "-u 0xabc.rig1", "-p x", "-d 0,1",
			"--api-bind-http 127.0.0.1:4500", "--api-read-only", "--no-watchdog"} {
			if !strings.Contains(line, want) {
				t.Errorf("%q is missing in %q", want, line)
			}
		}
	}
	args, _ := trex.BuildArgs(Job{Algorithm: etchash, DeviceIndexes: []int{0}, Host: "etc.2miners.com", Port: 11010, TLS: true})
	if line := strings.Join(args, " "); !strings.Contains(line, "-o stratum+ssl://etc.2miners.com:11010") {
		t.Errorf("with TLS: %s", line)
	}
}
