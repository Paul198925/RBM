package engine

import (
	"io"
	"os"
	"regexp"
	"strings"
)

// The exit code of a Windows program that could not load a DLL
// (STATUS_DLL_NOT_FOUND), as Go reports it.
const exitDLLNotFound = 0xC0000135

var missingLibraryRE = regexp.MustCompile(`error while loading shared libraries: ([^\s:]+)`)

// failureHint says what a miner that ended on its own is probably missing,
// from its exit code and the end of its log. It is empty if nothing
// familiar shows.
func failureHint(exitCode int, logTail string) string {
	lower := strings.ToLower(logTail)
	switch {
	case uint32(exitCode) == exitDLLNotFound,
		strings.Contains(lower, "vcruntime140"), strings.Contains(lower, "msvcp140"):
		return "a DLL the program needs is missing, usually the Microsoft Visual C++ Redistributable. " +
			"Install it with: winget install Microsoft.VCRedist.2015+.x64"
	case strings.Contains(lower, "driver version is insufficient"),
		strings.Contains(lower, "cuda driver version"):
		return "the NVIDIA driver is too old for this program. Update the driver."
	}
	if m := missingLibraryRE.FindStringSubmatch(logTail); m != nil {
		return "the library " + m[1] + " is missing. Install it with the package manager of your system."
	}
	return ""
}

// logTail returns the end of a log file, empty if it cannot be read.
func logTail(path string) string {
	const size = 4 << 10
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > size {
		f.Seek(-size, io.SeekEnd)
	}
	data, _ := io.ReadAll(io.LimitReader(f, size))
	return string(data)
}
