package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/young1ll/keelage/internal/core/supply"
	"github.com/young1ll/keelage/internal/port"
)

// HookBudget is the server-side deadline for one hook request. The client
// gives up at 50 ms; the server stops a little earlier so it never does
// work nobody is waiting for.
const HookBudget = 40 * time.Millisecond

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// MountDaemon adds the daemon's local API: hook events and the read-side
// queries the MCP tools use.
func MountDaemon(r chi.Router, hooks port.HookService, query port.ContextQuery) {
	r.Post("/v1/hook", func(w http.ResponseWriter, req *http.Request) {
		var ev supply.Event
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&ev); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), HookBudget)
		defer cancel()
		res, err := hooks.Hook(ctx, ev)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	r.Get("/v1/what_touches", func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		res, err := query.WhatTouches(req.Context(), q.Get("anchor"), q.Get("repo"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	r.Get("/v1/related", func(w http.ResponseWriter, req *http.Request) {
		res, err := query.Related(req.Context(), req.URL.Query().Get("id"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
}
