// Package pool reads what mining pools currently pay.
package pool

import (
	"context"
	"time"
)

// Offer is what one pool pays for one algorithm, and how to connect to it.
type Offer struct {
	Pool string
	// Algorithm is the normalized name, PoolAlgorithm the pool's spelling.
	Algorithm     string
	PoolAlgorithm string

	Host string
	Port int
	// TLSPort is 0 if the pool has no TLS port for the algorithm.
	TLSPort int
	User    string
	Pass    string

	// Prices are in BTC per day for one hash per second, as the pool
	// reports them. Whether they include FeePercent is the pool's business.
	Price     float64 // current estimate
	Price24h  float64 // estimate averaged over the last 24h
	Actual24h float64 // actually paid over the last 24h

	FeePercent float64
	// Hashrate is the total hashrate of the pool on this algorithm.
	Hashrate float64
	Workers  int
	Updated  time.Time
}

// Source is a pool that can be asked for its offers.
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]Offer, error)
}
