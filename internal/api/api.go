// Package api serves the state of the miner over HTTP, read-only, for
// dashboards and scripts.
package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"rbm/internal/device"
	"rbm/internal/engine"
)

const index = `rbm status api

GET /api/status    what every device group is mining, with hashrate, power,
                   profit, the candidates and what is taken out after failures
GET /api/devices   the GPUs and the CPU of this rig
`

// Handler returns the routes of the API. status is asked on every request.
func Handler(status func() engine.Status, devices []device.Device) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, status())
	})
	mux.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, devices)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(index))
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
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
