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
	// Coin is the symbol of the one coin this offer mines. It is empty
	// where the pool picks the coin itself and may change it.
	Coin     string
	CoinName string

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
	// LastBlockSeconds is how long ago the pool found its last block of
	// the coin, Blocks24h how many it found in a day. Both are 0 where the
	// pool does not say, as for its algorithm ports.
	LastBlockSeconds float64
	Blocks24h        float64
	Updated          time.Time
}

// Label names the offer for logs: "zpool" or "zpool:RVN".
func (o Offer) Label() string {
	if o.Coin == "" {
		return o.Pool
	}
	return o.Pool + ":" + o.Coin
}

// Source is a pool that can be asked for its offers.
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]Offer, error)
}
