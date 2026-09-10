package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/merkle"
	"github.com/young1ll/keelage/internal/port"
)

// LoginService is the device login (app.Login).
type LoginService interface {
	Start(ctx context.Context) (port.DeviceStart, error)
	Poll(ctx context.Context, org, deviceCode string) (port.DevicePoll, error)
}

// ProofSource serves transparency-log proofs for an org (ADR 0004).
type ProofSource interface {
	Checkpoint(ctx context.Context, org string) (merkle.Checkpoint, error)
	Inclusion(ctx context.Context, org string, seq int64) (merkle.Bundle, error)
	Consistency(ctx context.Context, org string, from, to uint64) (merkle.ConsistencyBundle, error)
}

// Server bundles what the team API needs.
type Server struct {
	Auth   port.Authenticator
	Team   port.TeamService
	Login  LoginService
	Proofs ProofSource
	// PublicKey is the server's checkpoint key (keys.EncodePublic form).
	PublicKey string
}

type ctxKey struct{}

// PrincipalFrom returns the authenticated principal of a request.
func PrincipalFrom(ctx context.Context) (port.Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(port.Principal)
	return p, ok
}

// rejection is the 400 body for a refused command (rejected.json shape).
type rejection struct {
	Error  string `json:"error"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

func writeFailure(w http.ResponseWriter, err error) {
	if r, ok := core.AsRejection(err); ok {
		writeJSON(w, http.StatusBadRequest, rejection{Error: r.Error(), Code: r.Code, Reason: r.Reason})
		return
	}
	switch {
	case errors.Is(err, port.ErrUnauthenticated):
		writeErr(w, http.StatusUnauthorized, err)
	case errors.Is(err, port.ErrForbidden):
		writeErr(w, http.StatusForbidden, err)
	case errors.Is(err, port.ErrNotFound):
		writeErr(w, http.StatusNotFound, err)
	case errors.Is(err, port.ErrDaemonKeyMismatch):
		writeErr(w, http.StatusConflict, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}

func decode(w http.ResponseWriter, req *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 32<<20)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h := req.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || tok == "" {
			writeErr(w, http.StatusUnauthorized, port.ErrUnauthenticated)
			return
		}
		p, err := s.Auth.Authenticate(req.Context(), tok)
		if err != nil {
			writeFailure(w, err)
			return
		}
		next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), ctxKey{}, p)))
	})
}

// MountServer adds the team API (spec §4.6 subset for weeks 10–11):
// device login, sync, gates and ledger proofs.
func MountServer(r chi.Router, s *Server) {
	r.Get("/ledger/key", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"public_key": s.PublicKey})
	})
	r.Post("/auth/device/start", func(w http.ResponseWriter, req *http.Request) {
		if s.Login == nil {
			writeErr(w, http.StatusNotImplemented, errors.New("device login is not configured"))
			return
		}
		res, err := s.Login.Start(req.Context())
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	r.Post("/auth/device/poll", func(w http.ResponseWriter, req *http.Request) {
		if s.Login == nil {
			writeErr(w, http.StatusNotImplemented, errors.New("device login is not configured"))
			return
		}
		var in struct {
			Org        string `json:"org"`
			DeviceCode string `json:"device_code"`
		}
		if !decode(w, req, &in) {
			return
		}
		res, err := s.Login.Poll(req.Context(), in.Org, in.DeviceCode)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.authenticate)
		r.Get("/me", func(w http.ResponseWriter, req *http.Request) {
			p, _ := PrincipalFrom(req.Context())
			writeJSON(w, http.StatusOK, p)
		})
		r.Post("/sync/register", func(w http.ResponseWriter, req *http.Request) {
			p, _ := PrincipalFrom(req.Context())
			var in struct {
				DaemonID  string `json:"daemon_id"`
				PublicKey string `json:"public_key"`
			}
			if !decode(w, req, &in) {
				return
			}
			if err := s.Team.RegisterDaemon(req.Context(), p, in.DaemonID, in.PublicKey); err != nil {
				writeFailure(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"daemon_id": in.DaemonID, "user": p.User, "org": p.Org})
		})
		r.Post("/sync/push", func(w http.ResponseWriter, req *http.Request) {
			p, _ := PrincipalFrom(req.Context())
			var in port.PushRequest
			if !decode(w, req, &in) {
				return
			}
			res, err := s.Team.Push(req.Context(), p, in)
			if err != nil {
				writeFailure(w, err)
				return
			}
			writeJSON(w, http.StatusOK, res)
		})
		r.Get("/sync/pull", func(w http.ResponseWriter, req *http.Request) {
			p, _ := PrincipalFrom(req.Context())
			q := req.URL.Query()
			since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
			limit, _ := strconv.Atoi(q.Get("limit"))
			res, err := s.Team.Pull(req.Context(), p, since, limit)
			if err != nil {
				writeFailure(w, err)
				return
			}
			writeJSON(w, http.StatusOK, res)
		})
		r.Post("/gates", func(w http.ResponseWriter, req *http.Request) {
			p, _ := PrincipalFrom(req.Context())
			var in port.GateRequest
			if !decode(w, req, &in) {
				return
			}
			g, err := s.Team.RequestGate(req.Context(), p, in)
			if err != nil {
				writeFailure(w, err)
				return
			}
			writeJSON(w, http.StatusOK, g)
		})
		r.Get("/gates", func(w http.ResponseWriter, req *http.Request) {
			p, _ := PrincipalFrom(req.Context())
			gs, err := s.Team.ListGates(req.Context(), p, accountability.GateState(req.URL.Query().Get("state")))
			if err != nil {
				writeFailure(w, err)
				return
			}
			writeJSON(w, http.StatusOK, gs)
		})
		r.Post("/gates/{id}/resolve", func(w http.ResponseWriter, req *http.Request) {
			p, _ := PrincipalFrom(req.Context())
			var in port.GateResolution
			if !decode(w, req, &in) {
				return
			}
			g, err := s.Team.ResolveGate(req.Context(), p, core.ID(chi.URLParam(req, "id")), in)
			if err != nil {
				writeFailure(w, err)
				return
			}
			writeJSON(w, http.StatusOK, g)
		})
		r.Get("/ledger/checkpoint", func(w http.ResponseWriter, req *http.Request) {
			p, _ := PrincipalFrom(req.Context())
			cp, err := s.Proofs.Checkpoint(req.Context(), p.Org)
			if err != nil {
				writeFailure(w, err)
				return
			}
			writeJSON(w, http.StatusOK, cp)
		})
		r.Get("/ledger/proof/inclusion", func(w http.ResponseWriter, req *http.Request) {
			p, _ := PrincipalFrom(req.Context())
			seq, err := strconv.ParseInt(req.URL.Query().Get("seq"), 10, 64)
			if err != nil || seq < 1 {
				writeErr(w, http.StatusBadRequest, errors.New("seq must be a positive integer"))
				return
			}
			b, err := s.Proofs.Inclusion(req.Context(), p.Org, seq)
			if err != nil {
				writeFailure(w, err)
				return
			}
			writeJSON(w, http.StatusOK, b)
		})
		r.Get("/ledger/proof/consistency", func(w http.ResponseWriter, req *http.Request) {
			p, _ := PrincipalFrom(req.Context())
			from, err1 := strconv.ParseUint(req.URL.Query().Get("from"), 10, 64)
			to, err2 := strconv.ParseUint(req.URL.Query().Get("to"), 10, 64)
			if err1 != nil || err2 != nil || from > to {
				writeErr(w, http.StatusBadRequest, errors.New("from and to must be tree sizes with from <= to"))
				return
			}
			b, err := s.Proofs.Consistency(req.Context(), p.Org, from, to)
			if err != nil {
				writeFailure(w, err)
				return
			}
			writeJSON(w, http.StatusOK, b)
		})
	})
}
