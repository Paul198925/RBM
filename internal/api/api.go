// Package api serves the web interface and the HTTP API behind it: what the
// miner is doing, and the configuration to read and change.
package api

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"rbm/internal/config"
	"rbm/internal/device"
	"rbm/internal/engine"
	"rbm/internal/pool"
	"rbm/internal/stats"
	"rbm/internal/sysinfo"
)

//go:embed web
var webFiles embed.FS

// PasswordMask stands for a password that is set. The real one never leaves
// the program; sending the mask back means "keep it".
const PasswordMask = "********"

// Backend is the running application as the API sees it.
type Backend interface {
	// Waiting says why nothing is mined, empty while the engine runs.
	Waiting() string
	Status() engine.Status
	System() System
	// SetPaused stops all mining or takes it up again.
	SetPaused(paused bool)
	Devices() []DeviceStatus
	Offers() []pool.Offer
	Balances() BalanceInfo
	// Activity returns the miner runs of the last days, newest first.
	Activity() ([]stats.Run, error)
	Config() config.Config
	// SaveConfig validates, stores and applies a new configuration.
	SaveConfig(cfg config.Config) error
	Meta(ctx context.Context) Meta
	ResetBenchmark(hashKey string)
	Enable(hashKey string)
	Log() []string
	MinerLogs() ([]LogFile, error)
	MinerLog(name string) (string, error)
}

// System describes the application and the machine it runs on.
type System struct {
	Started time.Time `json:"started"`
	Paused  bool      `json:"paused"`
	// Memory is nil if it could not be read.
	Memory *sysinfo.Memory `json:"memory,omitempty"`
}

// DeviceStatus is a device with what it reports about itself right now.
type DeviceStatus struct {
	device.Device
	// Telemetry is nil for devices that report nothing, like the CPU.
	Telemetry *device.Telemetry `json:"telemetry,omitempty"`
	// MaxTemperatureC is the highest temperature seen since the start.
	MaxTemperatureC float64 `json:"max_temperature_c,omitempty"`
}

// BalanceInfo is what the pools hold for the configured wallets.
type BalanceInfo struct {
	Balances []pool.Balance `json:"balances"`
	// Error says why a balance is missing or old, empty if all is well.
	Error string `json:"error,omitempty"`
}

// Meta is what the interface needs to know to offer sensible choices.
type Meta struct {
	Version   string      `json:"version"`
	ConfigDir string      `json:"config_dir"`
	Miners    []MinerInfo `json:"miners"`
	Regions   []string    `json:"zpool_regions"`
	// Currencies is empty if the pool could not be asked.
	Currencies []pool.PayoutCurrency `json:"payout_currencies"`
}

// MinerInfo describes a miner program the application knows.
type MinerInfo struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	Vendor     string   `json:"vendor"`
	FeePercent float64  `json:"fee_percent"`
	Algorithms []string `json:"algorithms"`
}

// LogFile is the log of one miner run.
type LogFile struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type statusResponse struct {
	Waiting string `json:"waiting"`
	System  System `json:"system"`
	engine.Status
}

type configResponse struct {
	Config config.Config `json:"config"`
	Meta   Meta          `json:"meta"`
}

type hashKeyRequest struct {
	HashKey string `json:"hash_key"`
}

// Handler returns the routes of the interface.
func Handler(b Backend) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		status := b.Status()
		if status.Groups == nil {
			status.Groups = []engine.GroupStatus{}
		}
		writeJSON(w, statusResponse{Waiting: b.Waiting(), System: b.System(), Status: status})
	})
	mux.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, b.Devices())
	})
	mux.HandleFunc("GET /api/offers", func(w http.ResponseWriter, r *http.Request) {
		offers := b.Offers()
		out := make([]offerJSON, len(offers))
		for i, o := range offers {
			out[i] = offerJSON{Pool: o.Pool, Algorithm: o.Algorithm, Coin: o.Coin, CoinName: o.CoinName,
				Price: o.Price, Price24h: o.Price24h, Actual24h: o.Actual24h, FeePercent: o.FeePercent,
				Hashrate: o.Hashrate, Workers: o.Workers, Updated: o.Updated}
		}
		writeJSON(w, out)
	})

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		cfg := b.Config()
		if cfg.General.API.Password != "" {
			cfg.General.API.Password = PasswordMask
		}
		writeJSON(w, configResponse{Config: cfg, Meta: b.Meta(r.Context())})
	})
	mux.HandleFunc("PUT /api/config", func(w http.ResponseWriter, r *http.Request) {
		// decoded on top of the current configuration: what the request
		// leaves out stays as it is
		cfg := b.Config()
		current := cfg.General.API.Password
		if !readJSON(w, r, &cfg) {
			return
		}
		if cfg.General.API.Password == PasswordMask {
			cfg.General.API.Password = current
		}
		if err := b.SaveConfig(cfg); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	action := func(do func(hashKey string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var req hashKeyRequest
			if !readJSON(w, r, &req) {
				return
			}
			if req.HashKey == "" {
				writeError(w, http.StatusBadRequest, "hash_key is missing")
				return
			}
			do(req.HashKey)
			w.WriteHeader(http.StatusNoContent)
		}
	}
	mux.HandleFunc("POST /api/benchmarks/reset", action(b.ResetBenchmark))
	mux.HandleFunc("POST /api/candidates/enable", action(b.Enable))

	mux.HandleFunc("POST /api/pause", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Paused *bool `json:"paused"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		if req.Paused == nil {
			writeError(w, http.StatusBadRequest, "paused is missing")
			return
		}
		b.SetPaused(*req.Paused)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/balances", func(w http.ResponseWriter, r *http.Request) {
		info := b.Balances()
		if info.Balances == nil {
			info.Balances = []pool.Balance{}
		}
		writeJSON(w, info)
	})

	mux.HandleFunc("GET /api/activity", func(w http.ResponseWriter, r *http.Request) {
		runs, err := b.Activity()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if runs == nil {
			runs = []stats.Run{}
		}
		writeJSON(w, runs)
	})
	mux.HandleFunc("GET /api/activity.csv", func(w http.ResponseWriter, r *http.Request) {
		runs, err := b.Activity()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="rbm-activity.csv"`)
		out := csv.NewWriter(w)
		out.Write([]string{"started", "ended", "minutes", "group", "miner", "algorithm", "pool", "coin", "benchmark", "end",
			"hashrate", "earned_btc", "energy_kwh", "accepted", "rejected"})
		for _, run := range runs {
			out.Write([]string{
				run.Started.Format(time.RFC3339), run.Ended.Format(time.RFC3339),
				strconv.FormatFloat(run.Ended.Sub(run.Started).Minutes(), 'f', 1, 64),
				run.Group, run.Miner, run.Algorithm, run.Pool, run.Coin, strconv.FormatBool(run.Benchmark), run.End,
				strconv.FormatFloat(run.Hashrate, 'f', 2, 64), strconv.FormatFloat(run.EarnedBTC, 'g', -1, 64),
				strconv.FormatFloat(run.EnergyKWh, 'f', 4, 64), strconv.FormatInt(run.Accepted, 10), strconv.FormatInt(run.Rejected, 10),
			})
		}
		out.Flush()
	})

	mux.HandleFunc("GET /api/log", func(w http.ResponseWriter, r *http.Request) {
		lines := b.Log()
		if lines == nil {
			lines = []string{}
		}
		writeJSON(w, lines)
	})
	mux.HandleFunc("GET /api/minerlogs", func(w http.ResponseWriter, r *http.Request) {
		files, err := b.MinerLogs()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if files == nil {
			files = []LogFile{}
		}
		writeJSON(w, files)
	})
	mux.HandleFunc("GET /api/minerlogs/{name}", func(w http.ResponseWriter, r *http.Request) {
		text, err := b.MinerLog(r.PathValue("name"))
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "no such log")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, text)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})

	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServerFS(static))

	return guard(b, mux)
}

type offerJSON struct {
	Pool       string    `json:"pool"`
	Algorithm  string    `json:"algorithm"`
	Coin       string    `json:"coin"`
	CoinName   string    `json:"coin_name"`
	Price      float64   `json:"price"`
	Price24h   float64   `json:"price_24h"`
	Actual24h  float64   `json:"actual_24h"`
	FeePercent float64   `json:"fee_percent"`
	Hashrate   float64   `json:"hashrate"`
	Workers    int       `json:"workers"`
	Updated    time.Time `json:"updated"`
}

// guard decides who may use the interface. It can change the payout address,
// so being reachable is not enough:
//
//   - with a password set, every request needs it;
//   - without one, only this machine may connect, and only under a local
//     name - a web page elsewhere cannot point a name of its own at
//     127.0.0.1 and talk to us through the browser;
//   - a change additionally needs a header that a browser only sends for
//     pages of this interface, and must not come from another origin.
func guard(b Backend, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		password := b.Config().General.API.Password
		if password != "" {
			_, given, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(given), []byte(password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="rbm", charset="UTF-8"`)
				writeError(w, http.StatusUnauthorized, "password required")
				return
			}
		} else if !local(r) {
			writeError(w, http.StatusForbidden,
				"without a password the interface only works on the machine rbm runs on; set api.password to use it from elsewhere")
			return
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-RBM-Request") == "" {
				writeError(w, http.StatusForbidden, "changes need the X-RBM-Request header")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					writeError(w, http.StatusForbidden, "changes from another origin are refused")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// local reports whether the request comes from this machine and addresses it
// by a local name.
func local(r *http.Request) bool {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	if ip := net.ParseIP(remote); ip == nil || !ip.IsLoopback() {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// readJSON decodes the request body into v and answers the request itself
// if that fails.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return false
	}
	return true
}

// Server is a running API server.
type Server struct {
	server   *http.Server
	listener net.Listener
}

// Start listens on addr and serves handler in the background. A port that
// cannot be bound is reported here, not later.
func Start(addr string, handler http.Handler) (*Server, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{
		listener: listener,
		server: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       2 * time.Minute,
		},
	}
	go s.server.Serve(listener)
	return s, nil
}

// Addr returns the address the server listens on.
func (s *Server) Addr() string { return s.listener.Addr().String() }

// Close stops the server, giving running requests a moment to finish.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}
