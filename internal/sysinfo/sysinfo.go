// Package sysinfo reads how much memory the machine has left.
package sysinfo

// Memory describes the memory of the machine, in MiB.
type Memory struct {
	TotalMiB float64 `json:"total_mib"`
	FreeMiB  float64 `json:"free_mib"`
	// CommitLimitMiB is how much programs may reserve in total: RAM plus
	// page file (swap). CommitFreeMiB is what is left of that. A miner
	// that builds a DAG needs free commit of about the DAG's size, however
	// much RAM is free.
	CommitLimitMiB float64 `json:"commit_limit_mib"`
	CommitFreeMiB  float64 `json:"commit_free_mib"`
}

const mib = 1 << 20
