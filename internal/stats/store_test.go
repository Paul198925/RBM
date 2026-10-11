package stats

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "stats.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	key := HashrateKey("rigel", []string{"GPU#00", "GPU#01"}, "kawpow")
	if key != "hashrate/rigel/GPU#00+GPU#01/kawpow" {
		t.Fatalf("key = %q", key)
	}
	if _, ok, err := store.Load(key); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}

	var st Stat
	st.Update(100, time.Minute, t0, 0.1)
	st.Update(104, time.Minute, t0.Add(time.Minute), 0.1)
	if err := store.Save(key, st); err != nil {
		t.Fatal(err)
	}
	st.Update(102, time.Minute, t0.Add(2*time.Minute), 0.1)
	if err := store.Save(key, st); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// survives a restart
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, ok, err := store.Load(key)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.Avg != st.Avg || got.Fluct != st.Fluct || got.Duration != st.Duration || !got.Updated.Equal(st.Updated) {
		t.Errorf("got %+v, want %+v", got, st)
	}
}

func TestStoreSamples(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for i := 0; i < 5; i++ {
		if err := store.AddSample("k", t0.Add(time.Duration(i)*time.Hour), float64(i), 120); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.PruneSamples(t0.Add(2 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("pruned %d samples, want 2", n)
	}
}
