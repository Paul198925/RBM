// Package miner knows the miner programs: where to get them, how to start
// them and how to ask them for their hashrate.
package miner

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"rbm/internal/algo"
)

//go:embed definitions/*.yaml
var builtin embed.FS

// Definition describes one miner program. It is data, loaded from YAML.
type Definition struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	// API names the adapter that reads the miner's statistics.
	API string `yaml:"api"`
	// Vendor is the kind of device the miner runs on: "NVIDIA" or "CPU".
	Vendor string  `yaml:"vendor"`
	Fee    float64 `yaml:"fee"`
	// Downloads is keyed by operating system (runtime.GOOS).
	Downloads  map[string]Download `yaml:"downloads"`
	Args       []string            `yaml:"args"`
	Scheme     string              `yaml:"scheme"`
	SchemeTLS  string              `yaml:"scheme_tls"`
	Algorithms []Algorithm         `yaml:"algorithms"`
}

type Download struct {
	URL    string `yaml:"url"`
	SHA256 string `yaml:"sha256"`
	// Executable is the file name of the program inside the archive.
	Executable string `yaml:"executable"`
}

type Algorithm struct {
	// Name is the normalized algorithm name.
	Name string `yaml:"name"`
	// MinerName is how the miner spells it, Name if empty.
	MinerName string `yaml:"miner_name"`
	// Fee overrides the miner's developer fee, in percent.
	Fee          *float64 `yaml:"fee"`
	MinMemoryMiB int      `yaml:"min_memory_mib"`
}

var (
	nameRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Builtin returns the definitions that ship with the program, by name.
func Builtin() (map[string]*Definition, error) {
	defs := map[string]*Definition{}
	files, err := fs.Glob(builtin, "definitions/*.yaml")
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		data, err := builtin.ReadFile(file)
		if err != nil {
			return nil, err
		}
		def, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if _, dup := defs[def.Name]; dup {
			return nil, fmt.Errorf("%s: miner %q is defined twice", file, def.Name)
		}
		defs[def.Name] = def
	}
	return defs, nil
}

// Names returns the sorted names of defs.
func Names(defs map[string]*Definition) []string {
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Parse reads and checks one definition.
func Parse(data []byte) (*Definition, error) {
	var def Definition
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&def); err != nil {
		return nil, err
	}
	if err := def.validate(); err != nil {
		return nil, err
	}
	return &def, nil
}

func (d *Definition) validate() error {
	if !nameRE.MatchString(d.Name) {
		return fmt.Errorf("name %q: use lower case letters, digits and '-'", d.Name)
	}
	if d.Version == "" || strings.ContainsAny(d.Version, `/\ `) {
		return fmt.Errorf("%s: version %q", d.Name, d.Version)
	}
	if _, err := NewAPI(d.API); err != nil {
		return fmt.Errorf("%s: %w", d.Name, err)
	}
	if len(d.Args) == 0 || d.Scheme == "" {
		return fmt.Errorf("%s: args and scheme are required", d.Name)
	}
	if len(d.Downloads) == 0 {
		return fmt.Errorf("%s: no downloads", d.Name)
	}
	for goos, dl := range d.Downloads {
		if !strings.HasPrefix(dl.URL, "https://") {
			return fmt.Errorf("%s: download for %s must use https", d.Name, goos)
		}
		if !sha256RE.MatchString(dl.SHA256) {
			return fmt.Errorf("%s: download for %s needs a lower case sha256", d.Name, goos)
		}
		if dl.Executable == "" || strings.ContainsAny(dl.Executable, `/\`) {
			return fmt.Errorf("%s: executable %q for %s must be a file name", d.Name, dl.Executable, goos)
		}
	}
	if len(d.Algorithms) == 0 {
		return fmt.Errorf("%s: no algorithms", d.Name)
	}
	seen := map[string]bool{}
	for _, a := range d.Algorithms {
		if a.Name == "" || a.Name != algo.Normalize(a.Name) {
			return fmt.Errorf("%s: algorithm %q is not a normalized name", d.Name, a.Name)
		}
		if seen[a.Name] {
			return fmt.Errorf("%s: algorithm %q is listed twice", d.Name, a.Name)
		}
		seen[a.Name] = true
	}
	return nil
}

// Algorithm returns the entry for a normalized algorithm name.
func (d *Definition) Algorithm(name string) (Algorithm, bool) {
	for _, a := range d.Algorithms {
		if a.Name == name {
			return a, true
		}
	}
	return Algorithm{}, false
}

// NameForMiner returns the algorithm name as the miner program spells it.
func (a Algorithm) NameForMiner() string {
	if a.MinerName != "" {
		return a.MinerName
	}
	return a.Name
}

// FeePercent returns the developer fee for algorithm a.
func (d *Definition) FeePercent(a Algorithm) float64 {
	if a.Fee != nil {
		return *a.Fee
	}
	return d.Fee
}

// Job is everything a miner needs to know to start mining.
type Job struct {
	Algorithm Algorithm
	// DeviceIndexes are the vendor indexes of the devices to mine on.
	DeviceIndexes []int
	APIPort       int
	Host          string
	Port          int
	TLS           bool
	User          string
	Pass          string
}

var placeholderRE = regexp.MustCompile(`\{[a-z_]+\}`)

// BuildArgs fills the placeholders of the argument template for job.
func (d *Definition) BuildArgs(job Job) ([]string, error) {
	scheme := d.Scheme
	if job.TLS {
		if d.SchemeTLS == "" {
			return nil, fmt.Errorf("%s has no TLS scheme", d.Name)
		}
		scheme = d.SchemeTLS
	}
	indexes := make([]string, len(job.DeviceIndexes))
	for i, index := range job.DeviceIndexes {
		indexes[i] = strconv.Itoa(index)
	}
	values := map[string]string{
		"{api_port}": strconv.Itoa(job.APIPort),
		"{devices}":  strings.Join(indexes, ","),
		"{algo}":     job.Algorithm.NameForMiner(),
		"{scheme}":   scheme,
		"{host}":     job.Host,
		"{port}":     strconv.Itoa(job.Port),
		"{user}":     job.User,
		"{pass}":     job.Pass,
	}

	var unknown []string
	args := make([]string, len(d.Args))
	for i, arg := range d.Args {
		// one pass, so a value that happens to contain braces is not expanded again
		args[i] = placeholderRE.ReplaceAllStringFunc(arg, func(ph string) string {
			v, ok := values[ph]
			if !ok {
				unknown = append(unknown, ph)
			}
			return v
		})
	}
	if len(unknown) > 0 {
		return nil, errors.New(d.Name + ": unknown placeholders " + strings.Join(unknown, ", "))
	}
	return args, nil
}
