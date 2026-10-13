package sysdeps

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// What every system of its kind has, and what therefore is no dependency
// worth naming: on Linux the C library and what belongs to it, on Windows
// the libraries of the system itself.
var (
	linuxBase = []string{"ld-linux", "libc.so.", "libm.so.", "libdl.so.", "libpthread.so.", "librt.so.", "libutil.so.", "libresolv.so."}
	windowsOS = map[string]bool{
		"advapi32.dll": true, "bcrypt.dll": true, "comdlg32.dll": true, "crypt32.dll": true, "dbghelp.dll": true,
		"gdi32.dll": true, "hal.dll": true, "iphlpapi.dll": true, "kernel32.dll": true, "msvcrt.dll": true,
		"mswsock.dll": true, "normaliz.dll": true, "ntdll.dll": true, "ntoskrnl.exe": true, "ole32.dll": true,
		"oleaut32.dll": true, "psapi.dll": true, "secur32.dll": true, "setupapi.dll": true, "shell32.dll": true,
		"shlwapi.dll": true, "user32.dll": true, "userenv.dll": true, "version.dll": true, "winmm.dll": true,
		"wldap32.dll": true, "ws2_32.dll": true, "powrprof.dll": true, "pdh.dll": true, "wtsapi32.dll": true,
	}
	// what comes with the driver of the graphics cards, not with a package
	driver = map[string]bool{
		"libcuda.so.1": true, "libnvidia-ml.so.1": true, "nvcuda.dll": true, "nvml.dll": true, "opencl.dll": true,
	}
)

// Notable reports whether a library a program imports is worth naming as
// something it needs from the system: everything but what every system of
// that kind has anyway.
func Notable(goos, library string) bool {
	if goos == "windows" {
		lower := strings.ToLower(library)
		// the names that start like this are no files but sets of
		// functions the system always has
		return !windowsOS[lower] && !strings.HasPrefix(lower, "api-ms-") && !strings.HasPrefix(lower, "ext-ms-")
	}
	for _, base := range linuxBase {
		if strings.HasPrefix(library, base) {
			return false
		}
	}
	return true
}

// FromDriver reports whether a library comes with the driver of the
// graphics cards and cannot be installed as a package of its own.
func FromDriver(library string) bool { return driver[strings.ToLower(library)] || driver[library] }

// Finder says whether the system has a library. The zero value looks at
// this machine.
type Finder struct {
	// GOOS replaces runtime.GOOS. For tests.
	GOOS string
	// Exists replaces looking for a file. For tests.
	Exists func(path string) bool
	// Cache replaces asking the loader of a Linux for the libraries it
	// knows. For tests.
	Cache func() string

	mu     sync.Mutex
	known  map[string]bool
	readAt time.Time
}

func (f *Finder) goos() string {
	if f.GOOS != "" {
		return f.GOOS
	}
	return runtime.GOOS
}

func (f *Finder) exists(path string) bool {
	if f.Exists != nil {
		return f.Exists(path)
	}
	_, err := os.Stat(path)
	return err == nil
}

// where a Linux keeps libraries, besides what its loader lists
var linuxDirs = []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64", "/usr/local/lib",
	"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu", "/usr/lib/wsl/lib"}

// Present reports whether the system has the library: on Linux by what
// its loader knows and where libraries are kept, on Windows in the system
// folder.
func (f *Finder) Present(library string) bool {
	if f.goos() == "windows" {
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = `C:\Windows`
		}
		return f.exists(filepath.Join(root, "System32", library))
	}
	if f.loaderKnows(library) {
		return true
	}
	dirs := append([]string{}, linuxDirs...)
	dirs = append(dirs, filepath.SplitList(os.Getenv("LD_LIBRARY_PATH"))...)
	for _, dir := range dirs {
		if dir != "" && f.exists(filepath.Join(dir, library)) {
			return true
		}
	}
	return false
}

// loaderKnows asks the loader's list, which is read anew now and then: a
// package may have been installed in between.
func (f *Finder) loaderKnows(library string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.known == nil || time.Since(f.readAt) > 5*time.Second {
		f.known = map[string]bool{}
		f.readAt = time.Now()
		text := ""
		if f.Cache != nil {
			text = f.Cache()
		} else {
			for _, tool := range []string{"ldconfig", "/sbin/ldconfig", "/usr/sbin/ldconfig"} {
				if out, err := exec.Command(tool, "-p").Output(); err == nil {
					text = string(out)
					break
				}
			}
		}
		// lines like "	libcurl.so.4 (libc6,x86-64) => /usr/lib/.../libcurl.so.4"
		for _, line := range strings.Split(text, "\n") {
			if name, _, ok := strings.Cut(strings.TrimSpace(line), " "); ok && strings.Contains(line, "=>") {
				f.known[name] = true
			}
		}
	}
	return f.known[library]
}
