package proc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The log of a program that keeps writing can be cut while it runs, and the
// program carries on behind what is left.
func TestTrimLogOfARunningProgram(t *testing.T) {
	spec, logPath := helper(t, "chatter")
	p, err := Start(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop(context.Background(), time.Second)

	size := func() int64 {
		info, err := os.Stat(logPath)
		if err != nil {
			return 0
		}
		return info.Size()
	}
	deadline := time.Now().Add(20 * time.Second)
	for size() < 200_000 {
		if time.Now().After(deadline) {
			t.Fatalf("the helper wrote only %d bytes", size())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cut, err := trimLog(logPath, 100_000, 10_000); !cut || err != nil {
		t.Fatalf("cut %v, %v", cut, err)
	}
	// the helper writes on at once, so the size says little; that it
	// wrote at the new end and not at its old place shows below
	after := size()
	// it keeps growing from there, without a hole where the old end was
	for size() < after+50_000 {
		if time.Now().After(deadline) {
			t.Fatalf("the helper stopped writing at %d bytes", size())
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Stop(context.Background(), time.Second)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "[hashsteer: the log grew to") {
		t.Errorf("the log starts with %q", data[:60])
	}
	if strings.Contains(string(data), "\x00") {
		t.Error("the program left a hole in the file")
	}
}

func TestTrimLogKeepsTheEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "miner.log")
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString("the same line over and over\n")
	}
	b.WriteString("the last line\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// small enough: left alone
	if cut, err := trimLog(path, 1<<20, 100); cut || err != nil {
		t.Fatalf("a small log: cut %v, %v", cut, err)
	}

	// the writer appends, as a miner does
	w, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if cut, err := trimLog(path, 1000, 100); !cut || err != nil {
		t.Fatalf("a large log: cut %v, %v", cut, err)
	}
	if _, err := w.WriteString("written after the cut\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if len(data) > 400 {
		t.Errorf("%d bytes are left", len(data))
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "[hashsteer: the log grew to") {
		t.Errorf("first line: %q", lines[0])
	}
	// what is kept starts at a whole line
	if lines[1] != "the same line over and over" {
		t.Errorf("second line: %q", lines[1])
	}
	if n := len(lines); lines[n-2] != "the last line" || lines[n-1] != "written after the cut" {
		t.Errorf("the end: %q", lines[n-2:])
	}
	if strings.Contains(text, "\x00") {
		t.Error("the writer left a hole in the file")
	}
}
