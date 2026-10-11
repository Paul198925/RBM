package miner

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuiltinDefinitions(t *testing.T) {
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	rigel, ok := defs["rigel"]
	if !ok {
		t.Fatalf("no rigel in %v", Names(defs))
	}
	for _, goos := range []string{"windows", "linux"} {
		if _, ok := rigel.Downloads[goos]; !ok {
			t.Errorf("rigel has no download for %s", goos)
		}
	}
	kawpow, ok := rigel.Algorithm("kawpow")
	if !ok {
		t.Fatal("rigel does not list kawpow")
	}
	if rigel.FeePercent(kawpow) != 1.0 || rigel.Fee != 0.7 {
		t.Errorf("fees = %v / %v", rigel.FeePercent(kawpow), rigel.Fee)
	}
}

func TestBuildArgs(t *testing.T) {
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	rigel := defs["rigel"]
	kawpow, _ := rigel.Algorithm("kawpow")
	job := Job{
		Algorithm:     kawpow,
		DeviceIndexes: []int{0, 1},
		APIPort:       4067,
		Host:          "kawpow.eu.mine.zpool.ca",
		Port:          1325,
		User:          "wallet1",
		Pass:          "rig1,c=BTC",
	}
	got, err := rigel.BuildArgs(job)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--api-bind", "127.0.0.1:4067", "-d", "0,1", "-a", "kawpow",
		"-o", "stratum+tcp://kawpow.eu.mine.zpool.ca:1325",
		"-u", "wallet1", "-p", "rig1,c=BTC", "--no-tui", "--no-watchdog",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args = %q\nwant   %q", got, want)
	}

	job.TLS, job.Port = true, 51325
	got, err = rigel.BuildArgs(job)
	if err != nil {
		t.Fatal(err)
	}
	if got[7] != "stratum+ssl://kawpow.eu.mine.zpool.ca:51325" {
		t.Errorf("tls url = %q", got[7])
	}
}

func TestBuildArgsDoesNotExpandValues(t *testing.T) {
	def := &Definition{Name: "x", Scheme: "stratum+tcp", Args: []string{"-u", "{user}", "-p", "{pass}"}}
	got, err := def.BuildArgs(Job{User: "{pass}", Pass: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if got[1] != "{pass}" {
		t.Errorf("user = %q", got[1])
	}

	def.Args = []string{"{nonsense}"}
	if _, err := def.BuildArgs(Job{}); err == nil {
		t.Error("unknown placeholder was accepted")
	}
}

func TestParseRejects(t *testing.T) {
	valid := `
name: demo
version: "1.0"
api: rigel
vendor: NVIDIA
downloads:
  linux: {url: "https://example.com/demo.tar.gz", sha256: "` + strings.Repeat("a", 64) + `", executable: demo}
args: ["-a", "{algo}"]
scheme: stratum+tcp
algorithms:
  - name: kawpow
`
	if _, err := Parse([]byte(valid)); err != nil {
		t.Fatalf("valid definition: %v", err)
	}
	cases := map[string][2]string{
		"unknown api":      {"api: rigel", "api: nope"},
		"http download":    {"https://example.com", "http://example.com"},
		"short hash":       {strings.Repeat("a", 64), "abc"},
		"path as exe":      {"executable: demo", "executable: ../demo"},
		"bad algorithm":    {"name: kawpow", "name: KawPow"},
		"unknown field":    {"vendor: NVIDIA", "vendro: NVIDIA"},
		"version with sep": {`version: "1.0"`, `version: "1/0"`},
	}
	for name, edit := range cases {
		if !strings.Contains(valid, edit[0]) {
			t.Fatalf("%s: test is broken", name)
		}
		if _, err := Parse([]byte(strings.Replace(valid, edit[0], edit[1], 1))); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestRigelPoll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stat" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{
			"algorithm": "kawpow",
			"hashrate": {"kawpow": 52300000.5},
			"power_usage": 285,
			"solution_stat": {"kawpow": {"accepted": 41, "rejected": 2, "invalid": 1}}
		}`))
	}))
	defer srv.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portText)

	api, err := NewAPI("rigel")
	if err != nil {
		t.Fatal(err)
	}
	sample, err := api.Poll(context.Background(), port)
	if err != nil {
		t.Fatal(err)
	}
	want := AlgoSample{Hashrate: 52300000.5, Accepted: 41, Rejected: 2, Invalid: 1}
	if sample.Algorithms["kawpow"] != want || sample.PowerWatts != 285 {
		t.Errorf("sample = %+v", sample)
	}

	srv.Close()
	if _, err := api.Poll(context.Background(), port); err == nil {
		t.Error("no error from a miner that is not listening")
	}
}

func TestWindowAverage(t *testing.T) {
	base := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	var w Window
	for i, h := range []float64{0, 0, 100, 110, 120} {
		w.Add(Sample{
			Time:       base.Add(time.Duration(i) * 10 * time.Second),
			Algorithms: map[string]AlgoSample{"kawpow": {Hashrate: h}},
			PowerWatts: 200,
		})
	}
	// only the samples after the warm-up count
	hashrate, power, n := w.Average("kawpow", base.Add(15*time.Second))
	if n != 3 || hashrate != 110 || power != 200 {
		t.Errorf("average = %v, %v, %d", hashrate, power, n)
	}
	if _, _, n := w.Average("kawpow", base.Add(time.Hour)); n != 0 {
		t.Errorf("n = %d", n)
	}
	w.Prune(base.Add(35 * time.Second))
	if len(w.samples) != 1 {
		t.Errorf("%d samples after prune", len(w.samples))
	}
	if last, ok := w.Last(); !ok || last.Algorithms["kawpow"].Hashrate != 120 {
		t.Errorf("last = %+v", last)
	}
}

// archives for the install tests

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarGzOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(content))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// definitionFor serves archive and returns a definition that downloads it.
func definitionFor(t *testing.T, archive []byte, suffix string) (*Definition, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(archive)
	return &Definition{
		Name:    "demo",
		Version: "1.0",
		Downloads: map[string]Download{runtime.GOOS: {
			URL:        srv.URL + "/demo" + suffix,
			SHA256:     hex.EncodeToString(sum[:]),
			Executable: "tool.bin",
		}},
	}, srv
}

func TestInstallZip(t *testing.T) {
	def, srv := definitionFor(t, zipOf(t, map[string]string{
		"demo-1.0/tool.bin":   "program",
		"demo-1.0/readme.txt": "docs",
	}), ".zip")
	binDir := filepath.Join(t.TempDir(), "bin")

	if _, ok := def.Installed(binDir); ok {
		t.Fatal("installed before Install")
	}
	exe, err := def.Install(context.Background(), binDir, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "program" {
		t.Errorf("executable holds %q", data)
	}
	if !strings.HasPrefix(exe, def.Dir(binDir)) {
		t.Errorf("executable at %s", exe)
	}

	// the second call needs no network
	srv.Close()
	again, err := def.Install(context.Background(), binDir, srv.Client())
	if err != nil || again != exe {
		t.Errorf("second install: %q, %v", again, err)
	}
	// nothing but the install directory is left behind
	entries, _ := os.ReadDir(binDir)
	if len(entries) != 1 {
		t.Errorf("bin dir holds %d entries", len(entries))
	}
}

func TestInstallTarGz(t *testing.T) {
	def, srv := definitionFor(t, tarGzOf(t, map[string]string{"demo/tool.bin": "program"}), ".tar.gz")
	exe, err := def.Install(context.Background(), t.TempDir(), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "program" {
		t.Errorf("executable holds %q", data)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(exe); info.Mode().Perm()&0o100 == 0 {
			t.Errorf("executable bit lost: %v", info.Mode())
		}
	}
}

func TestInstallRefusesWrongHash(t *testing.T) {
	def, srv := definitionFor(t, zipOf(t, map[string]string{"tool.bin": "program"}), ".zip")
	dl := def.Downloads[runtime.GOOS]
	dl.SHA256 = strings.Repeat("0", 64)
	def.Downloads[runtime.GOOS] = dl

	binDir := t.TempDir()
	_, err := def.Install(context.Background(), binDir, srv.Client())
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(binDir); len(entries) != 0 {
		t.Errorf("%d entries left after a refused download", len(entries))
	}
}

func TestInstallRefusesEscapingEntries(t *testing.T) {
	for _, name := range []string{"../evil.bin", "a/../../evil.bin", "/abs/evil.bin"} {
		def, srv := definitionFor(t, zipOf(t, map[string]string{name: "x", "tool.bin": "program"}), ".zip")
		parent := t.TempDir()
		binDir := filepath.Join(parent, "bin")
		if _, err := def.Install(context.Background(), binDir, srv.Client()); err == nil {
			t.Errorf("%s: no error", name)
		}
		if _, ok := def.Installed(binDir); ok {
			t.Errorf("%s: counts as installed", name)
		}
		if _, err := os.Stat(filepath.Join(parent, "evil.bin")); err == nil {
			t.Errorf("%s: file was written outside the target", name)
		}
	}
}

func TestInstallNeedsTheExecutable(t *testing.T) {
	def, srv := definitionFor(t, zipOf(t, map[string]string{"other.bin": "x"}), ".zip")
	if _, err := def.Install(context.Background(), t.TempDir(), srv.Client()); err == nil {
		t.Error("no error for an archive without the executable")
	}
}
