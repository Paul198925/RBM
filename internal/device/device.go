// Package device finds the hardware that can mine.
package device

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Kind string

const (
	GPU Kind = "gpu"
	CPU Kind = "cpu"
)

// Device is one GPU or the CPU.
type Device struct {
	// ID is unique on this rig: "GPU#00", "CPU#00".
	ID     string
	Kind   Kind
	Vendor string
	Model  string
	// Index is the device number its vendor tools use (nvidia-smi index).
	Index     int
	MemoryMiB int
	UUID      string
	// Threads is the number of logical processors, for the CPU.
	Threads int
}

const smiTimeout = 15 * time.Second

// Detect lists the NVIDIA GPUs and the CPU of this machine. A machine without
// an NVIDIA driver simply has no GPUs.
func Detect(ctx context.Context) ([]Device, error) {
	devices, err := detectNvidia(ctx)
	if err != nil {
		return nil, err
	}
	devices = append(devices, Device{
		ID:      "CPU#00",
		Kind:    CPU,
		Model:   cpuModel(),
		Threads: runtime.NumCPU(),
	})
	return devices, nil
}

func detectNvidia(ctx context.Context) ([]Device, error) {
	out, err := nvidiaSMI(ctx, "--query-gpu=index,name,memory.total,uuid")
	if errors.Is(err, exec.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseNvidiaGPUs(out)
}

// GPUPower returns the current power draw in watts by nvidia-smi index. GPUs
// that do not report a value are left out.
func GPUPower(ctx context.Context) (map[int]float64, error) {
	out, err := nvidiaSMI(ctx, "--query-gpu=index,power.draw")
	if err != nil {
		return nil, err
	}
	power := map[int]float64{}
	for _, fields := range csvLines(out) {
		if len(fields) != 2 {
			continue
		}
		index, err1 := strconv.Atoi(fields[0])
		watts, err2 := strconv.ParseFloat(fields[1], 64)
		if err1 == nil && err2 == nil {
			power[index] = watts
		}
	}
	return power, nil
}

func nvidiaSMI(ctx context.Context, query string) (string, error) {
	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return "", exec.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, smiTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, query, "--format=csv,noheader,nounits").Output()
	if err != nil {
		return "", fmt.Errorf("nvidia-smi: %w", err)
	}
	return string(out), nil
}

func parseNvidiaGPUs(out string) ([]Device, error) {
	var devices []Device
	for _, fields := range csvLines(out) {
		if len(fields) != 4 {
			return nil, fmt.Errorf("nvidia-smi: unexpected line %q", strings.Join(fields, ", "))
		}
		index, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("nvidia-smi: index %q", fields[0])
		}
		memory, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("nvidia-smi: memory %q", fields[2])
		}
		devices = append(devices, Device{
			ID:        fmt.Sprintf("GPU#%02d", index),
			Kind:      GPU,
			Vendor:    "NVIDIA",
			Model:     strings.TrimPrefix(fields[1], "NVIDIA "),
			Index:     index,
			MemoryMiB: memory,
			UUID:      fields[3],
		})
	}
	return devices, nil
}

func csvLines(out string) [][]string {
	var lines [][]string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		lines = append(lines, fields)
	}
	return lines
}
