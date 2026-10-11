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
	ID     string `json:"id"`
	Kind   Kind   `json:"kind"`
	Vendor string `json:"vendor,omitempty"`
	Model  string `json:"model"`
	// Index is the device number its vendor tools use (nvidia-smi index).
	Index     int    `json:"index"`
	MemoryMiB int    `json:"memory_mib,omitempty"`
	UUID      string `json:"uuid,omitempty"`
	// PCI is where a GPU sits on the PCI bus, as "bus:device" in two hex
	// digits each, e.g. "01:00". Empty if the driver does not say.
	PCI string `json:"pci,omitempty"`
	// Threads is the number of logical processors, for the CPU.
	Threads int `json:"threads,omitempty"`
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
	out, err := nvidiaSMI(ctx, "--query-gpu=index,name,memory.total,uuid,pci.bus_id")
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
		if len(fields) != 5 {
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
			PCI:       pciAddress(fields[4]),
		})
	}
	return devices, nil
}

// pciAddress shortens nvidia-smi's "00000000:01:00.0" to "01:00": bus and
// device, without the domain and the function. It is empty for anything
// that does not look like such an address.
func pciAddress(busID string) string {
	parts := strings.Split(busID, ":")
	if len(parts) != 3 {
		return ""
	}
	bus, device, _ := parts[1], "", ""
	device, _, _ = strings.Cut(parts[2], ".")
	for _, part := range []string{bus, device} {
		if _, err := strconv.ParseUint(part, 16, 8); err != nil || len(part) != 2 {
			return ""
		}
	}
	return strings.ToLower(bus + ":" + device)
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
