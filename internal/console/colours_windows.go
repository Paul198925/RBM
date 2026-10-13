package console

import (
	"os"

	"golang.org/x/sys/windows"
)

// unicodeTerminal reports whether the terminal shows characters outside
// ASCII as they are meant. A Windows console is written to in Unicode,
// whatever its code page.
func unicodeTerminal() bool { return true }

// enableColours makes the console understand the codes for colours and for
// moving the cursor. Windows consoles do not until they are asked to.
func enableColours(out *os.File) bool {
	handle := windows.Handle(out.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
