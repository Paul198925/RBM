package stats

import "time"

// How a run of a miner ended.
const (
	EndSwitched   = "switched"    // something else became more profitable
	EndCrashed    = "crashed"     // the miner program exited on its own
	EndNoHashrate = "no hashrate" // the miner hung and was stopped
	EndPaused     = "paused"      // the user paused mining
	EndStopped    = "stopped"     // the program stopped or the configuration changed
)

// Run is one run of a miner program, from start to stop: the activity log.
type Run struct {
	ID        int64     `json:"id"`
	Group     string    `json:"group"`
	Miner     string    `json:"miner"`
	Algorithm string    `json:"algorithm"`
	Pool      string    `json:"pool"`
	Coin      string    `json:"coin"`
	Started   time.Time `json:"started"`
	Ended     time.Time `json:"ended"`
	Benchmark bool      `json:"benchmark"`
	End       string    `json:"end"`
	// Hashrate is the average over the measured time, pauses included.
	Hashrate float64 `json:"hashrate"`
	// EarnedBTC is an estimate from hashrate and pool price, not what the
	// pool has credited.
	EarnedBTC float64 `json:"earned_btc"`
	EnergyKWh float64 `json:"energy_kwh"`
	Accepted  int64   `json:"accepted"`
	Rejected  int64   `json:"rejected"`
}

// AddRun appends a run to the activity log.
func (s *Store) AddRun(r Run) error {
	_, err := s.db.Exec(
		"INSERT INTO runs (grp, miner, algorithm, pool, coin, started, ended, benchmark, end, hashrate, earned, energy, accepted, rejected) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.Group, r.Miner, r.Algorithm, r.Pool, r.Coin, r.Started.Unix(), r.Ended.Unix(), r.Benchmark, r.End,
		r.Hashrate, r.EarnedBTC, r.EnergyKWh, r.Accepted, r.Rejected)
	return err
}

// Runs returns the runs that ended at or after since, newest first, at most
// limit of them.
func (s *Store) Runs(since time.Time, limit int) ([]Run, error) {
	rows, err := s.db.Query(
		"SELECT id, grp, miner, algorithm, pool, coin, started, ended, benchmark, end, hashrate, earned, energy, accepted, rejected "+
			"FROM runs WHERE ended >= ? ORDER BY ended DESC, id DESC LIMIT ?", since.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		var r Run
		var started, ended int64
		if err := rows.Scan(&r.ID, &r.Group, &r.Miner, &r.Algorithm, &r.Pool, &r.Coin, &started, &ended, &r.Benchmark, &r.End,
			&r.Hashrate, &r.EarnedBTC, &r.EnergyKWh, &r.Accepted, &r.Rejected); err != nil {
			return nil, err
		}
		r.Started, r.Ended = time.Unix(started, 0), time.Unix(ended, 0)
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// PruneRuns deletes the runs that ended before the given time.
func (s *Store) PruneRuns(before time.Time) error {
	_, err := s.db.Exec("DELETE FROM runs WHERE ended < ?", before.Unix())
	return err
}

// Total sums up the runs at one pool.
type Total struct {
	Pool string `json:"pool"`
	Runs int    `json:"runs"`
	// EarnedBTC is the estimate of the runs, as in Run.
	EarnedBTC float64   `json:"earned_btc"`
	EnergyKWh float64   `json:"energy_kwh"`
	Minutes   float64   `json:"minutes"`
	Started   time.Time `json:"started"`
	Updated   time.Time `json:"updated"`
}

// Totals sums up all recorded runs per pool.
func (s *Store) Totals() ([]Total, error) {
	rows, err := s.db.Query(
		"SELECT pool, COUNT(*), SUM(earned), SUM(energy), SUM(ended - started) / 60.0, MIN(started), MAX(ended) " +
			"FROM runs GROUP BY pool ORDER BY pool")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	totals := []Total{}
	for rows.Next() {
		var t Total
		var started, updated int64
		if err := rows.Scan(&t.Pool, &t.Runs, &t.EarnedBTC, &t.EnergyKWh, &t.Minutes, &started, &updated); err != nil {
			return nil, err
		}
		t.Started, t.Updated = time.Unix(started, 0), time.Unix(updated, 0)
		totals = append(totals, t)
	}
	return totals, rows.Err()
}

// RunKey names a device group's runs of a miner on an algorithm.
func RunKey(group, miner, algorithm string) string {
	return group + "/" + miner + "/" + algorithm
}

// LastRuns returns the newest run of every device group, miner and
// algorithm, by RunKey.
func (s *Store) LastRuns() (map[string]Run, error) {
	rows, err := s.db.Query(
		"SELECT grp, miner, algorithm, started, ended, end, hashrate, accepted, rejected FROM runs " +
			"WHERE id IN (SELECT MAX(id) FROM runs GROUP BY grp, miner, algorithm)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := map[string]Run{}
	for rows.Next() {
		var r Run
		var started, ended int64
		if err := rows.Scan(&r.Group, &r.Miner, &r.Algorithm, &started, &ended, &r.End, &r.Hashrate, &r.Accepted, &r.Rejected); err != nil {
			return nil, err
		}
		r.Started, r.Ended = time.Unix(started, 0), time.Unix(ended, 0)
		runs[RunKey(r.Group, r.Miner, r.Algorithm)] = r
	}
	return runs, rows.Err()
}

// SetNote stores a short text under key; an empty text removes it.
func (s *Store) SetNote(key, value string) error {
	if value == "" {
		_, err := s.db.Exec("DELETE FROM notes WHERE key = ?", key)
		return err
	}
	_, err := s.db.Exec("INSERT INTO notes (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// Notes returns the texts whose keys start with prefix, by the rest of the
// key.
func (s *Store) Notes(prefix string) (map[string]string, error) {
	rows, err := s.db.Query("SELECT key, value FROM notes WHERE substr(key, 1, ?) = ?", len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		notes[key[len(prefix):]] = value
	}
	return notes, rows.Err()
}
