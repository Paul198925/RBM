//go:build !windows

package console

import "os"

// unicodeTerminal reports whether the terminal shows characters outside
// ASCII as they are meant: whether the locale says UTF-8. The first of the
// three variables that is set decides, as it does for every program.
func unicodeTerminal() bool {
	return utf8Locale(os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG"))
}

// enableColours reports whether the terminal understands the codes for
// colours and for moving the cursor.
func enableColours(*os.File) bool {
	t := os.Getenv("TERM")
	return t != "" && t != "dumb"
}
