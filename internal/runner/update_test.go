package runner

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"hashsteer/internal/update"
)

// toServer sends every request to a test server, whatever host it names: the
// update code only fetches from github.com.
type toServer struct{ base *url.URL }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.URL.Scheme, clone.URL.Host = t.base.Scheme, t.base.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// releaseOf serves one release with a file for this system that holds
// program, and returns a checker that asks it.
func releaseOf(t *testing.T, version, program string) *update.Checker {
	t.Helper()
	var archive bytes.Buffer
	name := "hashsteer-v" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".zip"
		w := zip.NewWriter(&archive)
		f, _ := w.Create("hashsteer.exe")
		f.Write([]byte(program))
		w.Close()
	} else {
		name += ".tar.gz"
		gz := gzip.NewWriter(&archive)
		w := tar.NewWriter(gz)
		w.WriteHeader(&tar.Header{Name: "hashsteer", Mode: 0o755, Size: int64(len(program)), Typeflag: tar.TypeReg})
		w.Write([]byte(program))
		w.Close()
		gz.Close()
	}
	sum := sha256.Sum256(archive.Bytes())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases") {
			json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "v" + version, "html_url": "https://github.com/Paul198925/hashsteer/releases/tag/v" + version,
				"assets": []any{map[string]any{"name": name, "browser_download_url": "https://github.com/files/" + name, "digest": "sha256:" + hex.EncodeToString(sum[:])}}}})
			return
		}
		w.Write(archive.Bytes())
	}))
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL)
	return &update.Checker{API: "https://github.com/releases", Client: &http.Client{Transport: toServer{base}}}
}

func TestUpdateIsFoundFetchedAndWaits(t *testing.T) {
	app, out := testApp(t)
	ctx := context.Background()

	// without a checker nothing is looked for
	if info := app.UpdateInfo(); info.Current != Version || info.Available || info.Checked != nil {
		t.Errorf("info = %+v", info)
	}
	if app.CheckUpdate(ctx) == nil || app.InstallUpdate(ctx) == nil {
		t.Error("an update without a checker")
	}

	app.checker = releaseOf(t, "99.0.0", "the new program")
	app.restart = make(chan struct{}, 1)
	if err := app.InstallUpdate(ctx); err == nil {
		t.Error("installed before anything was found")
	}
	if err := app.CheckUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	info := app.UpdateInfo()
	if !info.Available || info.Latest != "99.0.0" || info.Checked == nil || !strings.Contains(info.Page, "v99.0.0") {
		t.Fatalf("info = %+v", info)
	}
	waitFor(t, "the news in the log", func() bool { return strings.Contains(out.String(), "hashsteer 99.0.0 is available") })

	// not started by a start script: the new program waits for the next start
	t.Setenv(RestartEnv, "")
	target, err := newProgramPath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(target) })
	if err := app.InstallUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "the new program" {
		t.Errorf("fetched %q", got)
	}
	info = app.UpdateInfo()
	if info.Downloaded != "99.0.0" || info.Restartable || info.Busy || info.Error != "" {
		t.Errorf("info = %+v", info)
	}
	select {
	case <-app.restart:
		t.Error("a restart nobody would follow up")
	default:
	}

	// started by a start script: it ends to let the new one take over
	t.Setenv(RestartEnv, "1")
	if err := app.InstallUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-app.restart:
	default:
		t.Error("no restart")
	}
}

func TestNewProgramIsNamedAfterTheRunningOne(t *testing.T) {
	for exe, want := range map[string]string{
		filepath.Join("opt", "hashsteer", "hashsteer"): filepath.Join("opt", "hashsteer", "hashsteer.new"),
		filepath.Join("mining", "hashsteer.exe"):       filepath.Join("mining", "hashsteer.new.exe"),
		// an installation from before the program was renamed
		filepath.Join("mining", "rbm.exe"): filepath.Join("mining", "rbm.new.exe"),
		filepath.Join("opt", "rbm", "rbm"): filepath.Join("opt", "rbm", "rbm.new"),
	} {
		if got := newProgramName(exe); got != want {
			t.Errorf("newProgramName(%q) = %q, want %q", exe, got, want)
		}
	}
	// the old start scripts say so under the old name
	t.Setenv(RestartEnv, "")
	t.Setenv(legacyRestartEnv, "1")
	if !restartable() {
		t.Error("not restartable under an old start script")
	}
	t.Setenv(legacyRestartEnv, "")
	if restartable() {
		t.Error("restartable without a start script")
	}
}

func TestNoUpdateToAnOlderOrEqualRelease(t *testing.T) {
	app, _ := testApp(t)
	app.checker = releaseOf(t, Version, "the same program")
	if err := app.CheckUpdate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if info := app.UpdateInfo(); info.Available || info.Latest != Version {
		t.Errorf("info = %+v", info)
	}
	if err := app.InstallUpdate(context.Background()); err == nil {
		t.Error("installed the version that runs")
	}
}

func TestRunEndsForARestart(t *testing.T) {
	app, _ := testApp(t)
	app.restart = make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() { done <- app.Run(context.Background()) }()
	waitFor(t, "the app to wait for a wallet", func() bool { return app.Waiting() != "starting" })
	app.restart <- struct{}{}
	if err := <-done; err != ErrRestart {
		t.Errorf("Run returned %v", err)
	}
}
