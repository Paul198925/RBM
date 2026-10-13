package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestProblemsAreListedAndFixedOnRequest(t *testing.T) {
	b := newBackend()
	status := do(b, request{path: "/api/status"}).Body.String()
	for _, want := range []string{`"miner":"ccminer-tpfuemp"`, `"package":"libcurl4"`, `"command":"sudo apt-get install -y libcurl4"`, `"miner":"wildrig"`} {
		if !strings.Contains(status, want) {
			t.Errorf("the status lacks %s", want)
		}
	}

	// installing changes the system: never without the header a page of
	// this interface sends, and never from another site
	fix := request{method: http.MethodPost, path: "/api/problems/fix", body: `{"miner":"ccminer-tpfuemp"}`}
	blocked := fix
	blocked.noHeader = true
	if w := do(b, blocked); w.Code == http.StatusNoContent || b.fixed["ccminer-tpfuemp"] {
		t.Fatalf("fixed without the header: %d", w.Code)
	}
	foreign := fix
	foreign.origin = "https://evil.example"
	if w := do(b, foreign); w.Code == http.StatusNoContent || b.fixed["ccminer-tpfuemp"] {
		t.Fatalf("fixed for another site: %d", w.Code)
	}

	// a miner must be named, and what cannot be fixed says so
	if w := do(b, request{method: http.MethodPost, path: "/api/problems/fix", body: `{}`}); w.Code != http.StatusBadRequest {
		t.Errorf("no miner named: %d", w.Code)
	}
	w := do(b, request{method: http.MethodPost, path: "/api/problems/fix", body: `{"miner":"wildrig"}`})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "knows no way") {
		t.Errorf("a problem without a fix: %d %s", w.Code, w.Body.String())
	}

	if w := do(b, fix); w.Code != http.StatusNoContent || !b.fixed["ccminer-tpfuemp"] {
		t.Fatalf("fix: %d %s", w.Code, w.Body.String())
	}
	if status = do(b, request{path: "/api/status"}).Body.String(); strings.Contains(status, `"miner":"ccminer-tpfuemp"`) {
		t.Error("the fixed problem is still listed")
	}
}
