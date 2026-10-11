package stats

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// HashrateKey names the hashrate stat of a miner on a set of devices.
func HashrateKey(miner string, deviceIDs []string, algorithm string) string {
	return "hashrate/" + miner + "/" + strings.Join(deviceIDs, "+") + "/" + algorithm
}

// PriceKey names the price stat of a pool for an algorithm.
func PriceKey(pool, algorithm string) string {
	return "price/" + pool + "/" + algorithm
}

// Store persists stats and raw samples in a SQLite database.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS stats (
	key     TEXT PRIMARY KEY,
	data    TEXT NOT NULL,
	updated INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS samples (
	key   TEXT NOT NULL,
	ts    INTEGER NOT NULL,
	value REAL NOT NULL,
	power REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS samples_key_ts ON samples (key, ts);
`

// Open opens the database at path and creates it if needed.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// one writer process, one connection: keeps the pragmas below in effect
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode = WAL", "PRAGMA busy_timeout = 5000", schema} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, fmt.Errorf("open stats database %s: %w", path, err)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Load returns the stat stored under key. ok is false if there is none.
func (s *Store) Load(key string) (st Stat, ok bool, err error) {
	var data string
	err = s.db.QueryRow("SELECT data FROM stats WHERE key = ?", key).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return Stat{}, false, nil
	}
	if err != nil {
		return Stat{}, false, err
	}
	if err := json.Unmarshal([]byte(data), &st); err != nil {
		return Stat{}, false, fmt.Errorf("stat %s: %w", key, err)
	}
	return st, true, nil
}

// Save stores st under key, replacing an earlier version.
func (s *Store) Save(key string, st Stat) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		"INSERT INTO stats (key, data, updated) VALUES (?, ?, ?) "+
			"ON CONFLICT (key) DO UPDATE SET data = excluded.data, updated = excluded.updated",
		key, string(data), st.Updated.Unix())
	return err
}

// Delete removes the stat stored under key, if there is one.
func (s *Store) Delete(key string) error {
	_, err := s.db.Exec("DELETE FROM stats WHERE key = ?", key)
	return err
}

// AddSample records one raw measurement for charts and troubleshooting.
func (s *Store) AddSample(key string, t time.Time, value, power float64) error {
	_, err := s.db.Exec("INSERT INTO samples (key, ts, value, power) VALUES (?, ?, ?, ?)",
		key, t.Unix(), value, power)
	return err
}

// PruneSamples deletes raw samples older than before and returns how many.
func (s *Store) PruneSamples(before time.Time) (int64, error) {
	res, err := s.db.Exec("DELETE FROM samples WHERE ts < ?", before.Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
