// Package web wires HTTP routing and middleware.
package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	cfg     config.Config
	log     *slog.Logger
	db      *pgxpool.Pool
	version string
	mux     *http.ServeMux
}

func NewServer(cfg config.Config, log *slog.Logger, db *pgxpool.Pool, version string) *Server {
	s := &Server{
		cfg:     cfg,
		log:     log,
		db:      db,
		version: version,
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s
}

// Handler applies the global middleware stack. The order is load-bearing —
// see docs/architecture: recover → requestID → accessLog → rateLimit →
// secureHeaders → sessionLoad (identity step) → csrf → routes.
func (s *Server) Handler() http.Handler {
	h := http.Handler(s.mux)
	h = s.csrf(h)
	// sessionLoad (Clerk claims, optional) is inserted HERE by the identity step.
	h = s.secureHeaders(h)
	h = s.rateLimit(h)
	h = s.accessLog(h)
	h = s.requestID(h)
	h = s.recover(h)
	return maxBytes(h, 10<<20) // 10 MB request cap on every route
}

func maxBytes(next http.Handler, n int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, n)
		next.ServeHTTP(w, r)
	})
}

// GET /healthz — liveness. Never touches the database.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
}

// GET /readyz — readiness. 200 only when a DB ping succeeds.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db not configured"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
