package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// headers are written on top of the files. The web interface rewrites the
// files, so comments inside them would not survive; the explanation of every
// setting is in the interface itself.
var headers = map[string]string{
	FileGeneral:    "General settings: rig name, electricity, switching, CPU mining, web interface.",
	FilePools:      "Pools: the wallets, and per pool what may be mined there.",
	FileMiners:     "Miner programs: which are used, for which algorithms, with which extra arguments.",
	FileAlgorithms: "Algorithms: which may be mined at all.",
	FileDevices:    "Devices: which stay out of mining.",
}

// sections pairs each file with the part of the configuration it holds.
func (c *Config) sections() []struct {
	file string
	data any
} {
	return []struct {
		file string
		data any
	}{
		{FileGeneral, &c.General},
		{FilePools, &c.Pools},
		{FileMiners, &c.Miners},
		{FileAlgorithms, &c.Algorithms},
		{FileDevices, &c.Devices},
	}
}

// LoadDir reads the configuration directory on top of the defaults. Files
// that do not exist leave the defaults in place. Unknown keys are an error,
// so a misspelled option cannot go unnoticed.
func LoadDir(dir string) (*Config, error) {
	cfg := Default()
	for _, s := range cfg.sections() {
		path := filepath.Join(dir, s.file)
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		// an empty file is a valid one: nothing changed from the defaults
		if err := dec.Decode(s.data); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return cfg, nil
}

// SaveDir validates cfg and writes all its files into dir. Each file is
// replaced in one step, so a crash cannot leave half a file behind.
func SaveDir(dir string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, s := range cfg.sections() {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "# %s\n# Edit it here or in the web interface, which also explains every setting.\n\n", headers[s.file])
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(s.data); err != nil {
			return err
		}
		enc.Close()
		if err := writeFile(filepath.Join(dir, s.file), buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// writeFile replaces path with data by way of a temporary file. The files
// hold a wallet address and maybe a password: only the owner may read them.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Exists reports whether dir holds a configuration.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, FileGeneral))
	return err == nil
}

// legacy is the single config.yaml of the first versions.
type legacy struct {
	Worker  string `yaml:"worker"`
	DataDir string `yaml:"data_dir"`
	Zpool   struct {
		Wallet   string `yaml:"wallet"`
		Currency string `yaml:"currency"`
		Region   string `yaml:"region"`
		TLS      bool   `yaml:"tls"`
	} `yaml:"zpool"`
	Power     Power     `yaml:"power"`
	Switching Switching `yaml:"switching"`
	CPU       CPU       `yaml:"cpu"`
	API       struct {
		Listen *string `yaml:"listen"`
	} `yaml:"api"`
}

// MigrateLegacy turns the single file of the first versions into a
// configuration directory. The old file is kept under a new name. It reports
// whether there was something to migrate.
func MigrateLegacy(file, dir string) (bool, error) {
	if Exists(dir) {
		return false, nil
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	cfg := Default()
	old := legacy{
		Worker:    cfg.General.Worker,
		DataDir:   cfg.General.DataDir,
		Power:     cfg.General.Power,
		Switching: cfg.General.Switching,
	}
	old.Zpool.Currency, old.Zpool.Region = cfg.Pools.Zpool.Currency, cfg.Pools.Zpool.Region
	if err := yaml.Unmarshal(data, &old); err != nil {
		return false, fmt.Errorf("%s: %w", file, err)
	}
	cfg.General.Worker, cfg.General.DataDir = old.Worker, old.DataDir
	cfg.General.Power, cfg.General.Switching, cfg.General.CPU = old.Power, old.Switching, old.CPU
	if old.API.Listen != nil {
		cfg.General.API.Listen = *old.API.Listen
	}
	z := &cfg.Pools.Zpool
	z.Wallet, z.Currency, z.Region, z.TLS = old.Zpool.Wallet, old.Zpool.Currency, old.Zpool.Region, old.Zpool.TLS

	if err := SaveDir(dir, cfg); err != nil {
		return false, fmt.Errorf("migrate %s: %w", file, err)
	}
	if err := os.Rename(file, file+".old"); err != nil {
		return false, err
	}
	return true, nil
}
