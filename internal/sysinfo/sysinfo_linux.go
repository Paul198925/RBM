package sysinfo

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// ReadMemory returns the memory figures of the machine.
func ReadMemory() (Memory, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Memory{}, err
	}
	return parseMeminfo(string(data))
}

func parseMeminfo(text string) (Memory, error) {
	kib := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
			kib[name] = v
		}
	}
	if kib["MemTotal"] == 0 {
		return Memory{}, errors.New("meminfo: no MemTotal")
	}
	m := Memory{
		TotalMiB: kib["MemTotal"] / 1024,
		FreeMiB:  kib["MemAvailable"] / 1024,
	}
	// Linux hands out memory more freely than Windows: what counts is the
	// RAM that can be freed plus the swap that is left
	m.CommitLimitMiB = (kib["MemTotal"] + kib["SwapTotal"]) / 1024
	m.CommitFreeMiB = (kib["MemAvailable"] + kib["SwapFree"]) / 1024
	return m, nil
}
