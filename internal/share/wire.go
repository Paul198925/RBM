// Package share exchanges measured hashrates with a share server: it fetches
// what other rigs measured, and sends what this one did if the user agreed.
//
// What is sent holds a card model, a miner program with its version, an
// algorithm, a hashrate and a power draw, under a random number the rig made
// up for itself. No wallet, no rig name.
package share

import (
	"errors"
	"math"
	"regexp"
)

// maxEntries is how many measurements one report may hold.
const maxEntries = 600

// Report is what one rig measured for one device of a model.
type Report struct {
	Device     string  `json:"device"`
	Miner      string  `json:"miner"`
	Version    string  `json:"version,omitempty"`
	Algorithm  string  `json:"algorithm"`
	Hashrate   float64 `json:"hashrate"`
	PowerWatts float64 `json:"power_watts,omitempty"`
}

// submission is what a rig sends.
type submission struct {
	// Client is an id the rig made up, so that its later reports replace
	// its earlier ones instead of counting twice.
	Client  string   `json:"client"`
	Reports []Report `json:"reports"`
}

// listing is what the server hands out: per device, miner and algorithm
// the middle of what the rigs reported.
type listing struct {
	Estimates []Report `json:"estimates"`
}

var (
	clientRE = regexp.MustCompile(`^[0-9a-f]{32}$`)
	nameRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	textRE   = regexp.MustCompile(`^[A-Za-z0-9 ()@.+_/-]{1,80}$`)
)

// valid reports whether r is something a rig could have measured. It is
// applied to what is sent and to what is received: a figure from elsewhere
// is not trusted because of where it comes from.
func (r Report) valid() bool {
	return textRE.MatchString(r.Device) && nameRE.MatchString(r.Miner) && nameRE.MatchString(r.Algorithm) &&
		(r.Version == "" || (len(r.Version) <= 32 && textRE.MatchString(r.Version))) &&
		r.Hashrate > 0 && r.Hashrate < 1e18 && !math.IsNaN(r.Hashrate) &&
		r.PowerWatts >= 0 && r.PowerWatts <= 2000
}

var errStatus = errors.New("unexpected status")
