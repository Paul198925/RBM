// Package sysdeps looks at what miner programs need from the system they
// run on: the shared libraries they import that they do not bring along.
// It reads the program files and runs nothing.
package sysdeps

import (
	"debug/elf"
	"debug/pe"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Imports lists the libraries that the programs and libraries below dir
// import and that are not below dir themselves: what the system has to
// provide. Files that are neither Linux nor Windows programs are passed
// over. On Windows the names of libraries are compared without regard to
// case, as the system does, and are returned in lower case.
func Imports(dir string) ([]string, error) {
	have := map[string]bool{}
	want := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		if f, err := elf.Open(path); err == nil {
			defer f.Close()
			have[entry.Name()] = true
			if soname, _ := f.DynString(elf.DT_SONAME); len(soname) > 0 {
				have[soname[0]] = true
			}
			libs, _ := f.ImportedLibraries()
			for _, lib := range libs {
				want[lib] = true
			}
			return nil
		}
		if f, err := pe.Open(path); err == nil {
			defer f.Close()
			have[strings.ToLower(entry.Name())] = true
			// the symbols come as "name:library"
			symbols, _ := f.ImportedSymbols()
			for _, symbol := range symbols {
				if i := strings.LastIndexByte(symbol, ':'); i >= 0 && i < len(symbol)-1 {
					want[strings.ToLower(symbol[i+1:])] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var missing []string
	for lib := range want {
		if !have[lib] {
			missing = append(missing, lib)
		}
	}
	sort.Strings(missing)
	return missing, nil
}
