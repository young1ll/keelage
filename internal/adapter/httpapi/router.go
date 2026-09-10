// Package httpapi builds the HTTP routers for the daemon (over UDS) and the
// server (over TCP). Week 1 exposes only /healthz.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Health is the /healthz response body.
type Health struct {
	OK      bool   `json:"ok"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// New returns a router with request-id, recoverer and /healthz.
func New(name, version string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Health{OK: true, Name: name, Version: version})
	})
	return r
}
