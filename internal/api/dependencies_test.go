package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestDependenciesAreListedAndInstalledOnRequest(t *testing.T) {
	b := newBackend()
	body := do(b, request{path: "/api/dependencies"}).Body.String()
	for _, want := range []string{`"miner":"ccminer-tpfuemp"`, `"library":"libcurl.so.4","present":false`, `"command":"sudo apt-get install -y libcurl4"`,
		`"library":"libcuda.so.1","present":false,"from_driver":true`, `"miner":"bzminer","version":"100.45","vendor":"NVIDIA","needs":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("the list lacks %s", want)
		}
	}

	install := request{method: http.MethodPost, path: "/api/dependencies/install", body: `{"library":"libcurl.so.4"}`}
	// installing changes the system: never without the header of this interface
	blocked := install
	blocked.noHeader = true
	if w := do(b, blocked); w.Code == http.StatusOK || b.fixed["libcurl.so.4"] {
		t.Fatalf("installed without the header: %d", w.Code)
	}
	if w := do(b, request{method: http.MethodPost, path: "/api/dependencies/install", body: `{}`}); w.Code != http.StatusBadRequest {
		t.Errorf("no library named: %d", w.Code)
	}
	w := do(b, request{method: http.MethodPost, path: "/api/dependencies/install", body: `{"library":"libcuda.so.1"}`})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "knows no package") {
		t.Errorf("a library without a package: %d %s", w.Code, w.Body.String())
	}
	// the answer is the list as it stands afterwards
	w = do(b, install)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"library":"libcurl.so.4","present":true`) {
		t.Errorf("install: %d %s", w.Code, w.Body.String())
	}
}
