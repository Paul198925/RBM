package runner

import (
	"strings"
	"testing"
)

func TestConsoleIsTheWindowsPicture(t *testing.T) {
	app, _ := testApp(t)
	app.logf("a line of the log")
	lines := app.Console()
	text := strings.Join(lines, "\n")
	for _, want := range []string{"hashsteer " + Version, "worker ", "RUNNING MINERS","\x1b["} {
		if !strings.Contains(text, want) {
			t.Errorf("the console lacks %q:\n%s", want, text)
		}
	}
	// the page shows the whole log below: its end is not repeated here
	if strings.Contains(text, "a line of the log") || strings.Contains(text, "LOG") {
		t.Errorf("the log is part of the picture:\n%s", text)
	}
	// a page has buttons for what the keys do
	if strings.Contains(text, "[Q] quit") {
		t.Error("the keys of the window are offered on a page")
	}
	if len(lines) > consoleRows {
		t.Errorf("%d lines for a window of %d", len(lines), consoleRows)
	}
}
