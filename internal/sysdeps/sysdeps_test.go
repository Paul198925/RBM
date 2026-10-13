package sysdeps

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNotable(t *testing.T) {
	for lib, want := range map[string]bool{
		"libc.so.6": false, "libm.so.6": false, "ld-linux-x86-64.so.2": false, "libpthread.so.0": false,
		"libcurl.so.4": true, "libOpenCL.so.1": true, "libstdc++.so.6": true, "libcuda.so.1": true,
	} {
		if Notable("linux", lib) != want {
			t.Errorf("linux %s: notable = %v", lib, !want)
		}
	}
	for lib, want := range map[string]bool{
		"kernel32.dll": false, "KERNEL32.dll": false, "ws2_32.dll": false, "ntoskrnl.exe": false,
		"api-ms-win-crt-runtime-l1-1-0.dll": false, "vcruntime140.dll": true, "VCRUNTIME140_1.dll": true,
		"msvcp140.dll": true, "nvcuda.dll": true, "opencl.dll": true, "somethingnew.dll": true,
	} {
		if Notable("windows", lib) != want {
			t.Errorf("windows %s: notable = %v", lib, !want)
		}
	}
	if !FromDriver("libcuda.so.1") || !FromDriver("NVCUDA.dll") || FromDriver("libcurl.so.4") {
		t.Error("what comes with the driver is told wrong")
	}
}

func TestPresent(t *testing.T) {
	linux := &Finder{GOOS: "linux",
		Cache: func() string {
			return "1234 libs found in cache `/etc/ld.so.cache'\n" +
				"\tlibcurl.so.4 (libc6,x86-64) => /usr/lib/x86_64-linux-gnu/libcurl.so.4\n" +
				"\tlibc.so.6 (libc6,x86-64, OS ABI: Linux 3.2.0) => /lib/x86_64-linux-gnu/libc.so.6\n"
		},
		Exists: func(path string) bool { return filepath.ToSlash(path) == "/usr/lib/wsl/lib/libcuda.so.1" }}
	for lib, want := range map[string]bool{"libcurl.so.4": true, "libc.so.6": true, "libcuda.so.1": true, "libOpenCL.so.1": false, "1234": false} {
		if linux.Present(lib) != want {
			t.Errorf("linux %s: present = %v", lib, !want)
		}
	}
	windows := &Finder{GOOS: "windows", Exists: func(path string) bool {
		return strings.HasSuffix(strings.ToLower(filepath.ToSlash(path)), "/system32/vcruntime140.dll")
	}}
	if !windows.Present("vcruntime140.dll") || windows.Present("msvcp140.dll") {
		t.Error("windows: presence is told wrong")
	}
}

// Imports reads real program files: this test binary is one, and what it
// imports is on the machine that built it.
func TestImportsOfARealProgram(t *testing.T) {
	dir := t.TempDir()
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "program"), self, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("no program"), 0o644); err != nil {
		t.Fatal(err)
	}
	libs, err := Imports(dir)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		joined := strings.Join(libs, " ")
		if !strings.Contains(joined, "kernel32.dll") {
			t.Errorf("imports = %v", libs)
		}
		finder := &Finder{}
		for _, lib := range libs {
			if Notable("windows", lib) {
				t.Errorf("a Go program needs %s", lib)
			}
			if !strings.HasPrefix(lib, "api-ms-") && !finder.Present(lib) {
				t.Errorf("%s is imported but not found on this machine", lib)
			}
		}
	}
}
