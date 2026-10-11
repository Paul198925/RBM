package miner

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func builtin1(t *testing.T, name string) *Definition {
	t.Helper()
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	def, ok := defs[name]
	if !ok {
		t.Fatalf("no %s in %v", name, Names(defs))
	}
	return def
}

func TestAllBuiltinDefinitionsAreComplete(t *testing.T) {
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if got := Names(defs); !reflect.DeepEqual(got, []string{"bzminer", "ccminer-tpfuemp", "cpuminer-opt", "gminer", "lolminer", "rigel", "srbminer", "srbminer-cpu", "wildrig", "xmrig"}) {
		t.Errorf("definitions = %v", got)
	}
	// miners that do not exist for every system, on purpose
	windowsOnly := map[string]bool{"gminer": true, "cpuminer-opt": true}
	for name, def := range defs {
		for _, goos := range []string{"windows", "linux"} {
			if _, ok := def.Downloads[goos]; !ok && !(goos == "linux" && windowsOnly[name]) {
				t.Errorf("%s has no download for %s", name, goos)
			}
		}
		if !def.SupportsTLS() {
			t.Errorf("%s cannot connect with TLS", name)
		}
		// every algorithm must give a full command line, with and without TLS
		for _, a := range def.Algorithms {
			for _, tls := range []bool{false, true} {
				args, err := def.BuildArgs(Job{Algorithm: a, DeviceIndexes: []int{0}, DevicePCI: []string{"01:00"}, APIPort: 4000,
					Host: "pool.example", Port: 3333, TLS: tls, User: "wallet", Pass: "x", Threads: 4})
				if err != nil {
					t.Errorf("%s/%s: %v", name, a.Name, err)
					continue
				}
				for _, arg := range args {
					if arg == "" || strings.ContainsAny(arg, "{}") {
						t.Errorf("%s/%s: argument %q in %q", name, a.Name, arg, args)
					}
				}
			}
		}
	}
}

func TestXmrigArgs(t *testing.T) {
	xmrig := builtin1(t, "xmrig")
	if xmrig.Vendor != VendorCPU {
		t.Errorf("vendor = %q", xmrig.Vendor)
	}
	ghostrider, ok := xmrig.Algorithm("ghostrider")
	if !ok {
		t.Fatal("xmrig does not list ghostrider")
	}
	job := Job{Algorithm: ghostrider, APIPort: 4100, Host: "ghostrider.eu.mine.zpool.ca", Port: 5354, User: "wallet1", Pass: "rig1,c=BTC"}

	got, err := xmrig.BuildArgs(job)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-a", "gr", "-o", "stratum+tcp://ghostrider.eu.mine.zpool.ca:5354", "-u", "wallet1", "-p", "rig1,c=BTC",
		"--http-host", "127.0.0.1", "--http-port", "4100", "--donate-level", "1", "--no-color"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args = %q\nwant   %q", got, want)
	}

	// a thread limit adds its arguments, TLS changes the scheme
	job.Threads, job.TLS, job.Port = 12, true, 55354
	got, err = xmrig.BuildArgs(job)
	if err != nil {
		t.Fatal(err)
	}
	if got[3] != "stratum+ssl://ghostrider.eu.mine.zpool.ca:55354" || !reflect.DeepEqual(got[len(got)-2:], []string{"-t", "12"}) {
		t.Errorf("args = %q", got)
	}
}

func TestWildrigArgs(t *testing.T) {
	wildrig := builtin1(t, "wildrig")
	meraki, ok := wildrig.Algorithm("meraki")
	if !ok {
		t.Fatal("wildrig does not list meraki")
	}
	if wildrig.FeePercent(meraki) != 0.75 {
		t.Errorf("fee = %v", wildrig.FeePercent(meraki))
	}
	got, err := wildrig.BuildArgs(Job{Algorithm: meraki, DeviceIndexes: []int{0, 2}, APIPort: 4200,
		Host: "meraki.eu.mine.zpool.ca", Port: 1234, User: "wallet1", Pass: "rig1,c=BTC"})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(got, " ")
	for _, want := range []string{
		"--api-port 4200", "-a progpow-telestai", "-o stratum+tcp://meraki.eu.mine.zpool.ca:1234",
		"--opencl-platforms nvidia", "--opencl-devices 0,2", "--multiple-instance",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("%q is missing in %q", want, line)
		}
	}
	// an algorithm named the same by pool and miner
	anime, _ := wildrig.Algorithm("anime")
	if anime.NameForMiner() != "anime" || wildrig.FeePercent(anime) != 1.0 {
		t.Errorf("anime = %+v", anime)
	}
}

func TestAlgorithmArgsAndTLSSwitch(t *testing.T) {
	def := &Definition{
		Name: "x",
		Args: []string{"--pool", "{host}:{port}", "--tls", "{tls}"},
		Algorithms: []Algorithm{
			{Name: "equihash144", MinerName: "EQUI144_5", Args: []string{"--pers", "auto"}},
		},
	}
	// no {scheme} in use: TLS needs no scheme
	if !def.SupportsTLS() {
		t.Error("TLS not supported")
	}
	got, err := def.BuildArgs(Job{Algorithm: def.Algorithms[0], Host: "h", Port: 1, TLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--pool", "h:1", "--tls", "on", "--pers", "auto"}; !reflect.DeepEqual(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}

	// a scheme in use must be defined for the kind of connection
	def.Args = []string{"-o", "{scheme}://{host}"}
	def.Scheme = "stratum+tcp"
	if def.SupportsTLS() {
		t.Error("TLS supported without a TLS scheme")
	}
	if _, err := def.BuildArgs(Job{Host: "h"}); err != nil {
		t.Error(err)
	}
}

func TestParseChecksPlaceholdersAndVendor(t *testing.T) {
	base := `
name: demo
version: "1.0"
api: xmrig
vendor: CPU
downloads:
  linux: {url: "https://example.com/demo.tar.gz", sha256: "` + strings.Repeat("a", 64) + `", executable: demo}
args: ["-a", "{algo}", "-o", "{scheme}://{host}"]
scheme: stratum+tcp
algorithms:
  - name: ghostrider
`
	if _, err := Parse([]byte(base)); err != nil {
		t.Fatalf("valid definition: %v", err)
	}
	cases := map[string][2]string{
		"unknown vendor":         {"vendor: CPU", "vendor: AMD"},
		"unknown placeholder":    {"{algo}", "{alog}"},
		"scheme used, none set":  {"scheme: stratum+tcp", "scheme: \"\""},
		"bad algorithm argument": {"  - name: ghostrider", "  - name: ghostrider\n    args: [\"{nope}\"]"},
		"bad threads argument":   {"scheme: stratum+tcp", "scheme: stratum+tcp\nthreads_args: [\"-t\", \"{thread}\"]"},
	}
	for name, edit := range cases {
		if !strings.Contains(base, edit[0]) {
			t.Fatalf("%s: test is broken", name)
		}
		if _, err := Parse([]byte(strings.Replace(base, edit[0], edit[1], 1))); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// serveMiner plays a miner API and returns its port.
func serveMiner(t *testing.T, path, body string) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	return port
}

func TestXmrigStylePoll(t *testing.T) {
	summary := `{"algo": "gr", "hashrate": {"total": [null, 2150.5, null]},
		"results": {"diff_current": 4000, "shares_good": 18, "shares_total": 20}}`

	for kind, path := range map[string]string{"xmrig": "/2/summary", "wildrig": "/"} {
		api, err := NewAPI(kind)
		if err != nil {
			t.Fatal(err)
		}
		sample, err := api.Poll(context.Background(), serveMiner(t, path, summary))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		// the 10 second average is not there yet, the minute average is
		want := AlgoSample{Hashrate: 2150.5, Accepted: 18, Rejected: 2}
		if got := sample.For("gr"); got != want {
			t.Errorf("%s: sample = %+v, want %+v", kind, got, want)
		}
	}

	// a miner that has not hashed yet reports nothing but nulls
	api, _ := NewAPI("xmrig")
	sample, err := api.Poll(context.Background(), serveMiner(t, "/2/summary", `{"hashrate": {"total": [null, null, null]}}`))
	if err != nil || sample.For("gr").Hashrate != 0 {
		t.Errorf("idle miner: %+v, %v", sample, err)
	}
}

func TestSampleFor(t *testing.T) {
	named := Sample{Algorithms: map[string]AlgoSample{"kawpow": {Hashrate: 1}, "": {Hashrate: 2}}}
	if named.For("kawpow").Hashrate != 1 || named.For("other").Hashrate != 2 {
		t.Errorf("named = %+v", named)
	}
	if (Sample{}).For("kawpow") != (AlgoSample{}) {
		t.Error("empty sample is not zero")
	}
}
