//go:build !windows

package console

import "os"

// enableColours reports whether the terminal understands the codes for
// colours and for moving the cursor.
func enableColours(*os.File) bool {
	t := os.Getenv("TERM")
	return t != "" && t != "dumb"
}
