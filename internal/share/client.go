package share

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rbm/internal/estimate"
)

// Client talks to a share server.
type Client struct {
	// URL is the address of the server, without a path.
	URL  string
	HTTP *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) endpoint(path string) string { return strings.TrimRight(c.URL, "/") + path }

// Estimates fetches what the rigs reported, as estimates for this one.
func (c *Client) Estimates(ctx context.Context) ([]estimate.Entry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/v1/estimates"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "rbm")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("shared hashrates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("shared hashrates: %w %s", errStatus, resp.Status)
	}
	var list listing
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&list); err != nil {
		return nil, fmt.Errorf("shared hashrates: %w", err)
	}
	entries := make([]estimate.Entry, 0, len(list.Estimates))
	for _, e := range list.Estimates {
		// what the server would not take from a rig is not taken from it
		if !e.valid() {
			continue
		}
		entries = append(entries, estimate.Entry{Device: e.Device, Miner: e.Miner, Version: e.Version, Algorithm: e.Algorithm,
			Hashrate: e.Hashrate, PowerWatts: e.PowerWatts})
	}
	return entries, nil
}

// Report sends what this rig measured. client is the id it made up for
// itself, so that a later report replaces this one.
func (c *Client) Report(ctx context.Context, client string, entries []estimate.Entry) error {
	sub := submission{Client: client}
	for _, e := range entries {
		r := Report{Device: e.Device, Miner: e.Miner, Version: e.Version, Algorithm: e.Algorithm, Hashrate: e.Hashrate, PowerWatts: e.PowerWatts}
		// a CPU's name can hold what the server does not take; such a
		// measurement stays here
		if r.valid() {
			sub.Reports = append(sub.Reports, r)
		}
	}
	if len(sub.Reports) == 0 {
		return nil
	}
	if len(sub.Reports) > maxEntries {
		sub.Reports = sub.Reports[:maxEntries]
	}
	body, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/v1/reports"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "rbm")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("sharing hashrates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("sharing hashrates: %w %s: %s", errStatus, resp.Status, strings.TrimSpace(string(text)))
	}
	return nil
}

// ClientID returns the id of this rig for the share server, made up once
// and kept in dir. It says nothing about the rig: it is a random number.
func ClientID(dir string) (string, error) {
	path := filepath.Join(dir, "share-id")
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); clientRE.MatchString(id) {
			return id, nil
		}
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return id, os.WriteFile(path, []byte(id+"\n"), 0o600)
}

// cached is the list of the server as it was last fetched, kept on disk so
// that a start without a connection still has it.
type cached struct {
	Fetched time.Time        `json:"fetched"`
	Entries []estimate.Entry `json:"entries"`
}

func cachePath(dir string) string { return filepath.Join(dir, "shared-estimates.json") }

// SaveCache keeps the fetched list in dir.
func SaveCache(dir string, entries []estimate.Entry, now time.Time) error {
	data, err := json.Marshal(cached{Fetched: now, Entries: entries})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := cachePath(dir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, cachePath(dir))
}

// LoadCache returns the list kept in dir and when it was fetched. There is
// none before the first fetch.
func LoadCache(dir string) ([]estimate.Entry, time.Time) {
	data, err := os.ReadFile(cachePath(dir))
	if err != nil {
		return nil, time.Time{}
	}
	var c cached
	if json.Unmarshal(data, &c) != nil {
		return nil, time.Time{}
	}
	return c.Entries, c.Fetched
}
