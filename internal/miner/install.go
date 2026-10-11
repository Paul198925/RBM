package miner

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	installedMarker = ".rbm-installed"
	// upper bounds against a download or archive that never ends
	maxDownloadBytes = 2 << 30
	maxExtractBytes  = 8 << 30
)

// Dir returns the directory the miner is installed in below binDir.
func (d *Definition) Dir(binDir string) string {
	return filepath.Join(binDir, d.Name+"-"+d.Version)
}

// Installed returns the path of the miner's executable if this exact
// download has been installed below binDir.
func (d *Definition) Installed(binDir string) (string, bool) {
	dl, ok := d.Downloads[runtime.GOOS]
	if !ok {
		return "", false
	}
	dir := d.Dir(binDir)
	marker, err := os.ReadFile(filepath.Join(dir, installedMarker))
	if err != nil || strings.TrimSpace(string(marker)) != dl.SHA256 {
		return "", false
	}
	exe, err := findFile(dir, dl.Executable)
	return exe, err == nil
}

// Install downloads the miner into binDir unless it is there already, and
// returns the path of its executable. The archive must match the SHA-256 of
// the definition, otherwise nothing is unpacked.
func (d *Definition) Install(ctx context.Context, binDir string, client *http.Client) (string, error) {
	if exe, ok := d.Installed(binDir); ok {
		return exe, nil
	}
	dl, ok := d.Downloads[runtime.GOOS]
	if !ok {
		return "", fmt.Errorf("%s has no download for %s", d.Name, runtime.GOOS)
	}
	if client == nil {
		client = http.DefaultClient
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}

	archive, err := download(ctx, client, dl, binDir)
	if err != nil {
		return "", fmt.Errorf("%s: %w", d.Name, err)
	}
	defer os.Remove(archive)

	dir := d.Dir(binDir)
	staging := dir + ".tmp"
	if err := os.RemoveAll(staging); err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)

	switch {
	case strings.HasSuffix(dl.URL, ".zip"):
		err = extractZip(archive, staging)
	case strings.HasSuffix(dl.URL, ".tar.gz"), strings.HasSuffix(dl.URL, ".tgz"):
		err = extractTarGz(archive, staging)
	default:
		err = fmt.Errorf("unsupported archive type: %s", dl.URL)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", d.Name, err)
	}
	if _, err := findFile(staging, dl.Executable); err != nil {
		return "", fmt.Errorf("%s: archive does not contain %s", d.Name, dl.Executable)
	}
	if err := os.WriteFile(filepath.Join(staging, installedMarker), []byte(dl.SHA256+"\n"), 0o644); err != nil {
		return "", err
	}

	// the staging directory only takes the final name once it is complete
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.Rename(staging, dir); err != nil {
		return "", err
	}
	return findFile(dir, dl.Executable)
}

// download fetches dl into a temporary file in dir and checks its hash.
func download(ctx context.Context, client *http.Client, dl Download, dir string) (path string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dl.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: status %s", dl.URL, resp.Status)
	}

	f, err := os.CreateTemp(dir, "download-*")
	if err != nil {
		return "", err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(f.Name())
		}
	}()

	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return "", fmt.Errorf("download %s: %w", dl.URL, err)
	}
	if n > maxDownloadBytes {
		return "", fmt.Errorf("download %s: larger than %d bytes", dl.URL, maxDownloadBytes)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != dl.SHA256 {
		return "", fmt.Errorf("download %s: sha256 is %s, expected %s", dl.URL, got, dl.SHA256)
	}
	return f.Name(), f.Close()
}

// target returns where the archive entry name goes below dest, and refuses
// names that would end up outside of it.
func target(dest, name string) (string, error) {
	local := filepath.FromSlash(name)
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("archive entry %q leaves the target directory", name)
	}
	return filepath.Join(dest, local), nil
}

func writeFile(path string, r io.Reader, mode fs.FileMode, budget *int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode.Perm()|0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, *budget+1))
	*budget -= n
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && *budget < 0 {
		err = errors.New("archive unpacks to more than the allowed size")
	}
	return err
}

func extractZip(archive, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()

	budget := int64(maxExtractBytes)
	for _, entry := range zr.File {
		path, err := target(dest, entry.Name)
		if err != nil {
			return err
		}
		mode := entry.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case mode.IsRegular():
			r, err := entry.Open()
			if err != nil {
				return err
			}
			err = writeFile(path, r, mode, &budget)
			r.Close()
			if err != nil {
				return err
			}
		}
		// links and other special entries are not needed and skipped
	}
	return nil
}

func extractTarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	budget := int64(maxExtractBytes)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		path, err := target(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(path, tr, fs.FileMode(hdr.Mode), &budget); err != nil {
				return err
			}
		}
	}
}

// findFile returns the path of the file called name somewhere below dir.
func findFile(dir, name string) (string, error) {
	var found string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == name {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fs.ErrNotExist
	}
	return found, nil
}
