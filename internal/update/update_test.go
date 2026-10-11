package update

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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		current, candidate string
		want               bool
	}{
		{"0.2.1", "0.3.0", true},
		{"0.2", "0.2.1", true},
		{"0.2.0", "0.2", false},
		{"0.3.0", "0.3.0", false},
		{"0.3.0", "0.2.9", false},
		{"0.9.0", "0.10.0", true},
		{"v0.2.1", "v0.3.0", true},
		// a build that is no release is not replaced
		{"dev", "9.9.9", false},
		{"0.3.0", "nightly", false},
		{"0.3.0", "0.4.0-rc1", false},
	} {
		if got := Newer(c.current, c.candidate); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.current, c.candidate, got)
		}
	}
}

func zipWith(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(content))
	}
	w.Close()
	return buf.Bytes()
}

func tarGzWith(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	w := tar.NewWriter(gz)
	for name, content := range files {
		w.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		w.Write([]byte(content))
	}
	w.Close()
	gz.Close()
	return buf.Bytes()
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// releaseServer serves a release list and the files in it.
func releaseServer(t *testing.T, files map[string][]byte, list func(base string) []map[string]any) *Checker {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			json.NewEncoder(w).Encode(list(srv.URL))
			return
		}
		data, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return &Checker{API: srv.URL + "/releases", GOOS: "windows", GOARCH: "amd64", anyHost: true}
}

func asset(base, name string, data []byte) map[string]any {
	return map[string]any{"name": name, "browser_download_url": base + "/" + name, "digest": digest(data)}
}

func TestLatestAndDownload(t *testing.T) {
	win := zipWith(t, map[string]string{"rbm-v0.3.0-windows-amd64/rbm.exe": "new windows program", "rbm-v0.3.0-windows-amd64/start.bat": "@echo off"})
	lin := tarGzWith(t, map[string]string{"rbm-v0.3.0-linux-amd64/rbm": "new linux program", "rbm-v0.3.0-linux-amd64/start.sh": "#!/bin/sh"})
	old := zipWith(t, map[string]string{"rbm.exe": "old program"})
	files := map[string][]byte{"rbm-v0.3.0-windows-amd64.zip": win, "rbm-v0.3.0-linux-amd64.tar.gz": lin, "rbm-v0.2.1-windows-amd64.zip": old}
	c := releaseServer(t, files, func(base string) []map[string]any {
		return []map[string]any{
			{"tag_name": "v0.2.1", "html_url": "https://example/v0.2.1", "assets": []any{asset(base, "rbm-v0.2.1-windows-amd64.zip", old)}},
			{"tag_name": "v0.3.0", "html_url": "https://example/v0.3.0", "assets": []any{
				asset(base, "rbm-v0.3.0-linux-amd64.tar.gz", lin), asset(base, "rbm-v0.3.0-windows-amd64.zip", win)}},
			// not for anyone yet, no number, nothing for this system, no checksum
			{"tag_name": "v9.0.0", "draft": true, "assets": []any{asset(base, "rbm-v9.0.0-windows-amd64.zip", win)}},
			{"tag_name": "nightly", "assets": []any{asset(base, "rbm-nightly-windows-amd64.zip", win)}},
			{"tag_name": "v0.4.0", "assets": []any{asset(base, "rbm-v0.4.0-darwin-arm64.zip", win)}},
			{"tag_name": "v0.5.0", "assets": []any{map[string]any{"name": "rbm-v0.5.0-windows-amd64.zip", "browser_download_url": base + "/x"}}},
		}
	})
	ctx := context.Background()

	release, ok, err := c.Latest(ctx)
	if err != nil || !ok || release.Version != "0.3.0" || release.Page != "https://example/v0.3.0" || release.name != "rbm-v0.3.0-windows-amd64.zip" {
		t.Fatalf("latest = %+v, %v, %v", release, ok, err)
	}
	target := filepath.Join(t.TempDir(), "rbm.new.exe")
	os.WriteFile(target, []byte("left over"), 0o644)
	if err := c.Download(ctx, release, target); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new windows program" {
		t.Errorf("installed %q", got)
	}

	// the same for Linux, from a tar.gz
	c.GOOS = "linux"
	release, ok, err = c.Latest(ctx)
	if err != nil || !ok || release.name != "rbm-v0.3.0-linux-amd64.tar.gz" {
		t.Fatalf("latest for linux = %+v, %v, %v", release, ok, err)
	}
	target = filepath.Join(t.TempDir(), "rbm.new")
	if err := c.Download(ctx, release, target); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new linux program" {
		t.Errorf("installed %q", got)
	}

	// a system no release has a file for
	c.GOOS = "plan9"
	if _, ok, err := c.Latest(ctx); ok || err != nil {
		t.Errorf("plan9: %v, %v", ok, err)
	}
}

func TestDownloadRefuses(t *testing.T) {
	good := zipWith(t, map[string]string{"rbm.exe": "program"})
	empty := zipWith(t, map[string]string{"readme.txt": "no program here"})
	files := map[string][]byte{"good.zip": good, "empty.zip": empty, "swapped.zip": []byte("something else")}
	c := releaseServer(t, files, func(string) []map[string]any { return nil })
	base := strings.TrimSuffix(c.API, "/releases")
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "rbm.new.exe")
	sum := strings.TrimPrefix(digest(good), "sha256:")

	for name, r := range map[string]Release{
		"swapped file":   {asset: base + "/swapped.zip", name: "swapped.zip", sha256: sum},
		"no program":     {asset: base + "/empty.zip", name: "empty.zip", sha256: strings.TrimPrefix(digest(empty), "sha256:")},
		"missing file":   {asset: base + "/gone.zip", name: "gone.zip", sha256: sum},
		"no checksum":    {asset: base + "/good.zip", name: "good.zip"},
		"nothing at all": {},
	} {
		if err := c.Download(ctx, r, target); err == nil {
			t.Errorf("%s: installed", name)
		}
	}
	// nothing was left behind by the failed tries
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}

	// outside of tests only github.com is fetched from
	c.anyHost = false
	if err := c.Download(ctx, Release{asset: base + "/good.zip", name: "good.zip", sha256: sum}, target); err == nil || !strings.Contains(err.Error(), "github.com") {
		t.Errorf("another host: %v", err)
	}
}

func TestLatestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	if _, _, err := (&Checker{API: srv.URL}).Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("rate limited: %v", err)
	}
}
