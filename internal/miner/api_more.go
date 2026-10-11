package miner

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// ccminerAPI talks the plain text protocol of ccminer and its many forks:
// one command per TCP connection, the answer a line of KEY=value pairs.
type ccminerAPI struct{}

func (ccminerAPI) Poll(ctx context.Context, port int) (Sample, error) {
	line, err := tcpCommand(ctx, port, "summary")
	if err != nil {
		return Sample{}, err
	}
	fields := map[string]float64{}
	for _, pair := range strings.Split(strings.TrimRight(line, "|\x00\r\n "), ";") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if v, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
			fields[strings.ToUpper(strings.TrimSpace(key))] = v
		}
	}
	if len(fields) == 0 {
		return Sample{}, fmt.Errorf("ccminer api: unexpected answer %.80q", line)
	}
	// newer forks report H/s next to the classic kH/s, which runs out of
	// digits for slow algorithms
	hashrate := fields["KHS"] * 1000
	if hs := fields["HS"]; hs > hashrate {
		hashrate = hs
	}
	return Sample{
		Time: time.Now(),
		Algorithms: map[string]AlgoSample{"": {
			Hashrate: hashrate,
			Accepted: int64(fields["ACC"]),
			Rejected: int64(fields["REJ"]),
		}},
	}, nil
}

// tcpCommand sends one line to a miner on this machine and returns the
// first line of the answer.
func tcpCommand(ctx context.Context, port int, command string) (string, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(conn, "%s\n", command); err != nil {
		return "", err
	}
	line, err := bufio.NewReaderSize(conn, 64<<10).ReadString('\n')
	// the miner closes the connection after its answer, often without a
	// line end
	if line == "" && err != nil {
		return "", err
	}
	return line, nil
}

// srbminerAPI reads the status document of SRBMiner-Multi.
type srbminerAPI struct{}

type srbminerStatus struct {
	MiningTime float64 `json:"mining_time"`
	Algorithms []struct {
		Name     string `json:"name"`
		Hashrate struct {
			GPU struct {
				Total float64 `json:"total"`
			} `json:"gpu"`
			CPU struct {
				Total float64 `json:"total"`
			} `json:"cpu"`
		} `json:"hashrate"`
		Shares struct {
			Accepted int64 `json:"accepted"`
			Rejected int64 `json:"rejected"`
		} `json:"shares"`
	} `json:"algorithms"`
	GPUDevices []struct {
		Power float64 `json:"asic_power"`
	} `json:"gpu_devices"`
}

func (srbminerAPI) Poll(ctx context.Context, port int) (Sample, error) {
	var status srbminerStatus
	if err := getJSON(ctx, port, "/", &status); err != nil {
		return Sample{}, err
	}
	sample := Sample{Time: time.Now(), Algorithms: map[string]AlgoSample{}}
	for _, gpu := range status.GPUDevices {
		sample.PowerWatts += gpu.Power
	}
	if len(status.Algorithms) > 0 {
		a := status.Algorithms[0]
		hashrate := a.Hashrate.GPU.Total + a.Hashrate.CPU.Total
		// in its first seconds the miner reports figures from its start-up
		// tests, not from mining
		if status.MiningTime < 20 {
			hashrate = 0
		}
		sample.Algorithms[""] = AlgoSample{Hashrate: hashrate, Accepted: a.Shares.Accepted, Rejected: a.Shares.Rejected}
	}
	return sample, nil
}
