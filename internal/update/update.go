// Package update looks for newer releases of rbm on GitHub and fetches them.
// What it fetches is checked against the SHA-256 GitHub publishes for the
// file. That guards against a broken or swapped download, not against a
// release that should never have been published.
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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const releasesAPI = "https://api.github.com/repos/Paul198925/RBM/releases?per_page=30"

// maxDownload is far above what a release weighs.
const maxDownload = 200 << 20

// Release is a published version of rbm for this system.
type Release struct {
	// Version is the release's number without the leading "v".
	Version string `json:"version"`
	// Page is where the release is described.
	Page string `json:"page"`

	asset  string
	name   string
	sha256 string
}

// Checker asks GitHub for releases. The zero value is ready to use.
type Checker struct {
	// API, Client, GOOS and GOARCH are replaceable for tests.
	API    string
	Client *http.Client
	GOOS   string
	GOARCH string

	// anyHost lets a test serve the files itself
	anyHost bool
}

func (c *Checker) system() (string, string) {
	goos, goarch := c.GOOS, c.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

func (c *Checker) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

// Latest returns the newest release that has a file for this system. ok is
// false if there is none.
func (c *Checker) Latest(ctx context.Context) (release Release, ok bool, err error) {
	api := c.API
	if api == "" {
		api = releasesAPI
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return Release{}, false, err
	}
	req.Header.Set("User-Agent", "rbm")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client().Do(req)
	if err != nil {
		return Release{}, false, fmt.Errorf("update check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, false, fmt.Errorf("update check: status %s", resp.Status)
	}
	var list []githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return Release{}, false, fmt.Errorf("update check: %w", err)
	}

	goos, goarch := c.system()
	for _, r := range list {
		version := strings.TrimPrefix(r.TagName, "v")
		if r.Draft || !valid(version) || (ok && !Newer(release.Version, version)) {
			continue
		}
		// the file for this system: rbm-v1.2.3-linux-amd64.tar.gz
		prefix := "rbm-" + r.TagName + "-" + goos + "-" + goarch + "."
		for _, a := range r.Assets {
			digest, hasDigest := strings.CutPrefix(a.Digest, "sha256:")
			if !strings.HasPrefix(a.Name, prefix) || !hasDigest || len(digest) != 64 {
				continue
			}
			if !strings.HasSuffix(a.Name, ".zip") && !strings.HasSuffix(a.Name, ".tar.gz") {
				continue
			}
			release, ok = Release{Version: version, Page: r.HTMLURL, asset: a.URL, name: a.Name, sha256: strings.ToLower(digest)}, true
		}
	}
	return release, ok, nil
}

// parse splits "1.2.3" into its numbers.
func parse(version string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) < 2 || len(parts) > 4 {
		return nil, false
	}
	numbers := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		numbers[i] = n
	}
	return numbers, true
}

func valid(version string) bool {
	_, ok := parse(version)
	return ok
}

// Newer reports whether candidate is a later version than current. A
// version that is no number, like that of a build from a branch, is never
// older than anything: such a program is not replaced.
func Newer(current, candidate string) bool {
	a, okA := parse(current)
	b, okB := parse(candidate)
	if !okA || !okB {
		return false
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return y > x
		}
	}
	return false
}

// Download fetches the release, checks it and writes the program in it to
// target. The file there is replaced in one step.
func (c *Checker) Download(ctx context.Context, release Release, target string) error {
	if release.asset == "" || release.sha256 == "" {
		return errors.New("update: this release has no file for this system")
	}
	if !c.anyHost && !strings.HasPrefix(release.asset, "https://github.com/") {
		return fmt.Errorf("update: the file is not at github.com: %s", release.asset)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.asset, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "rbm")
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update: status %s", resp.Status)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if len(archive) > maxDownload {
		return errors.New("update: the file is larger than a release can be")
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != release.sha256 {
		return fmt.Errorf("update: %s does not have the checksum GitHub lists for it; it was not installed", release.name)
	}

	goos, _ := c.system()
	program := "rbm"
	if goos == "windows" {
		program = "rbm.exe"
	}
	var data []byte
	if strings.HasSuffix(release.name, ".zip") {
		data, err = fromZip(archive, program)
	} else {
		data, err = fromTarGz(archive, program)
	}
	if err != nil {
		return fmt.Errorf("update: %s: %w", release.name, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

var errNoProgram = errors.New("the program is not in the archive")

func fromZip(archive []byte, program string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if path.Base(f.Name) != program || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, maxDownload))
	}
	return nil, errNoProgram
}

func fromTarGz(archive []byte, program string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errNoProgram
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeReg && path.Base(header.Name) == program {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}
