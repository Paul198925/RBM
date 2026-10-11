package device

import (
	"context"
	"strconv"
)

// Telemetry is what a GPU reports about itself at one moment. A value the
// card does not report is 0.
type Telemetry struct {
	TemperatureC    float64 `json:"temperature_c"`
	FanPercent      float64 `json:"fan_percent"`
	PowerWatts      float64 `json:"power_watts"`
	PowerLimitWatts float64 `json:"power_limit_watts"`
	// PowerDefaultWatts is the limit the card comes with.
	PowerDefaultWatts  float64 `json:"power_default_watts"`
	UtilizationPercent float64 `json:"utilization_percent"`
	MemoryUsedMiB      float64 `json:"memory_used_mib"`
	ClockMHz           float64 `json:"clock_mhz"`
	MemoryClockMHz     float64 `json:"memory_clock_mhz"`
}

const telemetryQuery = "--query-gpu=index,temperature.gpu,fan.speed,power.draw,power.limit,utilization.gpu,memory.used,clocks.gr,clocks.mem,power.default_limit"

// GPUTelemetry reads the current state of the NVIDIA GPUs, by nvidia-smi
// index.
func GPUTelemetry(ctx context.Context) (map[int]Telemetry, error) {
	out, err := nvidiaSMI(ctx, telemetryQuery)
	if err != nil {
		return nil, err
	}
	return parseTelemetry(out), nil
}

func parseTelemetry(out string) map[int]Telemetry {
	result := map[int]Telemetry{}
	for _, fields := range csvLines(out) {
		if len(fields) != 10 {
			continue
		}
		index, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		// "[N/A]" and the like read as 0
		value := func(i int) float64 {
			v, _ := strconv.ParseFloat(fields[i], 64)
			return v
		}
		result[index] = Telemetry{
			TemperatureC:       value(1),
			FanPercent:         value(2),
			PowerWatts:         value(3),
			PowerLimitWatts:    value(4),
			UtilizationPercent: value(5),
			MemoryUsedMiB:      value(6),
			ClockMHz:           value(7),
			MemoryClockMHz:     value(8),
			PowerDefaultWatts:  value(9),
		}
	}
	return result
}
