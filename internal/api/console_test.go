package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestConsoleLines(t *testing.T) {
	w := do(newBackend(), request{path: "/api/console"})
	var lines []string
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &lines) != nil || len(lines) < 5 {
		t.Fatalf("console: %d %s", w.Code, w.Body.String())
	}
	// the colour codes of the terminal travel as they are
	if !strings.HasPrefix(lines[0], "\x1b[1m\x1b[36mhashsteer") {
		t.Errorf("first line: %q", lines[0])
	}
}
