package miner

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

// serveTCP plays a miner with a line based API: it answers one command per
// connection and returns its port.
func serveTCP(t *testing.T, answer func(command string) string) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			command, _ := bufio.NewReader(conn).ReadString('\n')
			conn.Write([]byte(answer(strings.TrimSpace(command))))
			conn.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func TestCcminerPoll(t *testing.T) {
	api, err := NewAPI("ccminer")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		answer string
		want   AlgoSample
	}{
		// the classic answer: kH/s only, closed without a line end
		"khs": {"NAME=ccminer;VER=2026.09.2;API=1.9;ALGO=rinhash;GPUS=1;KHS=1234.56;SOLV=0;ACC=41;REJ=2;ACCMN=1.5;DIFF=0.5;UPTIME=600;TS=1791700000|",
			AlgoSample{Hashrate: 1234560, Accepted: 41, Rejected: 2}},
		// slow algorithms need the H/s field, kH/s rounds to nothing
		"hs": {"NAME=ccminer;ALGO=yespower;KHS=0.00;HS=1290.5;ACC=3;REJ=0|\n",
			AlgoSample{Hashrate: 1290.5, Accepted: 3}},
		"starting": {"NAME=ccminer;ALGO=rinhash;KHS=0.00;ACC=0;REJ=0|", AlgoSample{}},
	}
	for name, c := range cases {
		var got string
		port := serveTCP(t, func(command string) string { got = command; return c.answer })
		sample, err := api.Poll(context.Background(), port)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != "summary" {
			t.Errorf("%s: miner was asked %q", name, got)
		}
		if sample.For("rinhash") != c.want {
			t.Errorf("%s: sample = %+v, want %+v", name, sample.For("rinhash"), c.want)
		}
	}

	// garbage and silence are errors, not a hashrate of zero
	if _, err := api.Poll(context.Background(), serveTCP(t, func(string) string { return "HTTP/1.1 400 Bad Request\r\n" })); err == nil {
		t.Error("no error for an answer that is not ccminer's")
	}
	if _, err := api.Poll(context.Background(), serveTCP(t, func(string) string { return "" })); err == nil {
		t.Error("no error for an empty answer")
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if _, err := api.Poll(context.Background(), port); err == nil {
		t.Error("no error from a miner that is not listening")
	}
}

func TestSrbminerPoll(t *testing.T) {
	api, err := NewAPI("srbminer")
	if err != nil {
		t.Fatal(err)
	}
	status := func(miningTime string) string {
		return `{"rig_name": "rig1", "mining_time": ` + miningTime + `,
			"algorithms": [{"id": 0, "name": "firopow",
				"hashrate": {"gpu": {"gpu0": 16400000, "gpu1": 16430000, "total": 32830000}, "cpu": {"total": 0}},
				"shares": {"total": 22, "accepted": 21, "rejected": 1}}],
			"gpu_devices": [{"device": "gpu0", "asic_power": 158}, {"device": "gpu1", "asic_power": 162}]}`
	}
	sample, err := api.Poll(context.Background(), serveMiner(t, "/", status("600")))
	if err != nil {
		t.Fatal(err)
	}
	want := AlgoSample{Hashrate: 32830000, Accepted: 21, Rejected: 1}
	if sample.For("firopow") != want || sample.PowerWatts != 320 {
		t.Errorf("sample = %+v, power %v", sample.For("firopow"), sample.PowerWatts)
	}

	// what it reports while starting is not a mining speed
	sample, err = api.Poll(context.Background(), serveMiner(t, "/", status("5")))
	if err != nil || sample.For("firopow").Hashrate != 0 || sample.For("firopow").Accepted != 21 {
		t.Errorf("starting miner: %+v, %v", sample, err)
	}
	// nor is a miner that has no algorithm yet a failure
	if sample, err := api.Poll(context.Background(), serveMiner(t, "/", `{"mining_time": 0}`)); err != nil || sample.For("x").Hashrate != 0 {
		t.Errorf("empty status: %+v, %v", sample, err)
	}
}

func TestNewMinerDefinitions(t *testing.T) {
	ccminer := builtin1(t, "ccminer-tpfuemp")
	rinhash, ok := ccminer.Algorithm("rinhash")
	if !ok || ccminer.FeePercent(rinhash) != 0 {
		t.Fatalf("rinhash = %+v, %v", rinhash, ok)
	}
	args, err := ccminer.BuildArgs(Job{Algorithm: rinhash, DeviceIndexes: []int{1}, APIPort: 4300,
		Host: "rinhash.eu.mine.zpool.ca", Port: 7148, User: "wallet1", Pass: "rig1,c=RVN,zap=RIN"})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	for _, want := range []string{"-a rinhash", "-o stratum+tcp://rinhash.eu.mine.zpool.ca:7148", "-u wallet1", "-p rig1,c=RVN,zap=RIN", "-d 1", "-b 127.0.0.1:4300"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is missing in %q", want, line)
		}
	}
	if verus, _ := ccminer.Algorithm("verushash"); verus.NameForMiner() != "verus" {
		t.Errorf("verushash is called %q", verus.NameForMiner())
	}

	srbminer := builtin1(t, "srbminer")
	sccpow, ok := srbminer.Algorithm("sccpow")
	if !ok || srbminer.FeePercent(sccpow) != 0.85 {
		t.Fatalf("sccpow = %+v, %v", sccpow, ok)
	}
	for tls, want := range map[bool]string{false: "--tls false", true: "--tls true"} {
		args, err := srbminer.BuildArgs(Job{Algorithm: sccpow, DeviceIndexes: []int{0, 1}, APIPort: 4400,
			Host: "sccpow.eu.mine.zpool.ca", Port: 1327, TLS: tls, User: "wallet1", Pass: "rig1,c=RVN"})
		if err != nil {
			t.Fatal(err)
		}
		line := strings.Join(args, " ")
		for _, want := range []string{"--algorithm firopow", "--pool sccpow.eu.mine.zpool.ca:1327", "--wallet wallet1", "--password rig1,c=RVN",
			"--gpu-id 0,1", "--disable-cpu", "--api-enable --api-port 4400", want} {
			if !strings.Contains(line, want) {
				t.Errorf("%q is missing in %q", want, line)
			}
		}
	}
}

func TestGminerPoll(t *testing.T) {
	api, err := NewAPI("gminer")
	if err != nil {
		t.Fatal(err)
	}
	stat := `{"miner": "GMiner 3.44", "algorithm": "Equihash 144,5 \"auto\"", "uptime": 600, "devices": [
		{"gpu_id": 0, "name": "RTX 3060", "speed": 58, "accepted_shares": 20, "rejected_shares": 1, "stale_shares": 0, "power_usage": 110},
		{"gpu_id": 1, "name": "RTX 2080 SUPER", "speed": 71.5, "accepted_shares": 25, "rejected_shares": 0, "stale_shares": 2, "power_usage": 180}]}`
	sample, err := api.Poll(context.Background(), serveMiner(t, "/stat", stat))
	if err != nil {
		t.Fatal(err)
	}
	want := AlgoSample{Hashrate: 129.5, Accepted: 45, Rejected: 1, Invalid: 2}
	if sample.For("144_5") != want || sample.PowerWatts != 290 {
		t.Errorf("sample = %+v, power %v", sample.For("144_5"), sample.PowerWatts)
	}
	// before the cards are initialised the list is empty
	if sample, err := api.Poll(context.Background(), serveMiner(t, "/stat", `{"devices": []}`)); err != nil || sample.For("x").Hashrate != 0 {
		t.Errorf("starting miner: %+v, %v", sample, err)
	}
}

func TestGminerAndCPUDefinitions(t *testing.T) {
	gminer := builtin1(t, "gminer")
	equihash, ok := gminer.Algorithm("equihash144")
	if !ok || gminer.FeePercent(equihash) != 2 {
		t.Fatalf("equihash144 = %+v, %v", equihash, ok)
	}
	args, err := gminer.BuildArgs(Job{Algorithm: equihash, DeviceIndexes: []int{0, 1}, APIPort: 4500,
		Host: "equihash144.eu.mine.zpool.ca", Port: 2144, TLS: true, User: "wallet1", Pass: "rig1,c=RVN"})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	// every card is an argument of its own, and the algorithm's extras follow
	for _, want := range []string{"--algo 144_5", "--server equihash144.eu.mine.zpool.ca:2144", "--ssl 1", "--devices 0 1 --cuda 1",
		"--api 4500", "--pers auto"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is missing in %q", want, line)
		}
	}
	for i, arg := range args {
		if arg == "--devices" && (args[i+1] != "0" || args[i+2] != "1") {
			t.Errorf("devices are not separate arguments: %q", args)
		}
	}
	if sccpow, _ := gminer.Algorithm("sccpow"); sccpow.NameForMiner() != "firo" {
		t.Errorf("sccpow is called %q", sccpow.NameForMiner())
	}

	cpu := builtin1(t, "srbminer-cpu")
	if cpu.Vendor != VendorCPU {
		t.Fatalf("vendor = %q", cpu.Vendor)
	}
	power2b, _ := cpu.Algorithm("power2b")
	args, err = cpu.BuildArgs(Job{Algorithm: power2b, APIPort: 4600, Host: "power2b.eu.mine.zpool.ca", Port: 6242,
		User: "wallet1", Pass: "rig1,c=RVN", Threads: 12})
	if err != nil {
		t.Fatal(err)
	}
	line = strings.Join(args, " ")
	for _, want := range []string{"--algorithm yespower2b", "--disable-gpu", "--tls false", "--api-port 4600", "--cpu-threads 12"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is missing in %q", want, line)
		}
	}
	if strings.Contains(line, "--gpu-id") {
		t.Errorf("a cpu miner got a gpu list: %q", line)
	}
}

func TestLolminerPoll(t *testing.T) {
	api, err := NewAPI("lolminer")
	if err != nil {
		t.Fatal(err)
	}
	summary := `{"Software": "lolMiner 1.98a", "Num_Workers": 2, "Num_Algorithms": 1, "Algorithms": [
		{"Algorithm": "Autolykos V2", "Performance_Unit": "Mh/s", "Performance_Factor": 1000000, "Total_Performance": 251.5,
		 "Total_Accepted": 40, "Total_Rejected": 1, "Total_Stales": 2, "Worker_Performance": [120.5, 131]}],
		"Workers": [{"Index": 0, "Power": 110.5}, {"Index": 1, "Power": 150}]}`
	sample, err := api.Poll(context.Background(), serveMiner(t, "/summary", summary))
	if err != nil {
		t.Fatal(err)
	}
	want := AlgoSample{Hashrate: 251.5e6, Accepted: 40, Rejected: 1, Invalid: 2}
	if sample.For("AUTOLYKOS2") != want || sample.PowerWatts != 260.5 {
		t.Errorf("sample = %+v, power %v", sample.For("AUTOLYKOS2"), sample.PowerWatts)
	}
	// while the miner starts there is nothing to report
	if sample, err := api.Poll(context.Background(), serveMiner(t, "/summary", `{"Algorithms": []}`)); err != nil || sample.For("x").Hashrate != 0 {
		t.Errorf("starting miner: %+v, %v", sample, err)
	}
}

func TestStratumDialects(t *testing.T) {
	job := func(def *Definition, name, mode string, tls bool) string {
		a, ok := def.Algorithm(name)
		if !ok {
			t.Fatalf("%s does not list %s", def.Name, name)
		}
		args, err := def.BuildArgs(Job{Algorithm: a, DeviceIndexes: []int{0}, APIPort: 4500, Host: "etc.2miners.com", Port: 1010,
			TLS: tls, User: "0xabc.rig1", Pass: "x", EthMode: mode})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(args, " ")
	}

	// Rigel names the dialect in the address
	rigel := builtin1(t, "rigel")
	if line := job(rigel, "etchash", "ethproxy", false); !strings.Contains(line, "-o ethproxy+tcp://etc.2miners.com:1010") {
		t.Errorf("rigel, ethproxy: %s", line)
	}
	if line := job(rigel, "etchash", "ethproxy", true); !strings.Contains(line, "-o ethproxy+ssl://") {
		t.Errorf("rigel, ethproxy with TLS: %s", line)
	}
	for _, mode := range []string{"", "qtminer"} {
		if line := job(rigel, "etchash", mode, false); !strings.Contains(line, "-o stratum+tcp://") {
			t.Errorf("rigel, mode %q: %s", mode, line)
		}
	}

	// lolMiner takes it as an option
	lol := builtin1(t, "lolminer")
	line := job(lol, "etchash", "ethproxy", false)
	for _, want := range []string{"--algo ETCHASH", "--pool etc.2miners.com:1010", "--user 0xabc.rig1", "--tls off", "--devices 0",
		"--apiport 4500", "--disable-dag-verify 1", "--ethstratum ETHPROXY"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is missing in %q", want, line)
		}
	}
	if line := job(lol, "ethash", "qtminer", true); !strings.Contains(line, "--ethstratum ETHV1") || !strings.Contains(line, "--tls on") {
		t.Errorf("lolminer, qtminer: %s", line)
	}
	if line := job(lol, "autolykos2", "", false); strings.Contains(line, "--ethstratum") {
		t.Errorf("lolminer, no dialect: %s", line)
	}
	// a miner that knows no dialects is started as ever
	if line := job(builtin1(t, "gminer"), "etchash", "ethproxy", false); !strings.Contains(line, "--algo etchash") {
		t.Errorf("gminer: %s", line)
	}
}
