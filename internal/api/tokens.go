// Package api is the public JSON transport: org-scoped Bearer tokens and the
// versioned /api/v1 surface. It reuses the same sqlc queries and rules as the
// HTML app — a second transport, never parallel logic.
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/ratelimit"
	"github.com/jackc/pgx/v5"
	"log/slog"
)

// GenerateToken returns the plaintext (shown ONCE at creation) and the
// SHA-256 hex that is stored. Format: ggg_ + 32 random bytes (base64url).
func GenerateToken() (plaintext, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	plaintext = "ggg_" + base64.RawURLEncoding.EncodeToString(b[:])
	return plaintext, HashToken(plaintext), nil
}

// HashToken is the storage form: SHA-256 hex of the plaintext.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// WriteError emits the API error shape: {"error":{"code","message"}}.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Middleware authenticates Bearer tokens against api_tokens and enforces the
// per-token request budget.
type Middleware struct {
	Q *sqlc.Queries
	// Limiter is the per-token budget, shared across every guarded route so
	// a client cannot multiply its allowance by spreading calls over
	// endpoints. Nil disables token limiting (tests that construct the
	// middleware directly); NewMiddleware wires the configured one.
	Limiter *ratelimit.Keyed
}

// NewMiddleware builds the API guard with a per-token budget of rpm requests
// per minute (burst 2×).
func NewMiddleware(q *sqlc.Queries, rpm int) *Middleware {
	if rpm < 1 {
		rpm = 60
	}
	return &Middleware{Q: q, Limiter: ratelimit.PerMinute(rpm)}
}

// RequireAPIToken guards a route group with a minimum scope ("read" or
// "write"; write satisfies read). Success sets ctxOrg + touches last_used_at.
// Failures are 401 JSON; insufficient scope is 403.
func (m *Middleware) RequireAPIToken(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if len(auth) <= len(prefix) || auth[:len(prefix)] != prefix {
			WriteError(w, http.StatusUnauthorized, "unauthorized", "Missing or malformed Authorization header (want: Bearer ggg_…).")
			return
		}
		token, err := m.Q.GetAPITokenByHash(r.Context(), HashToken(auth[len(prefix):]))
		if errors.Is(err, pgx.ErrNoRows) {
			WriteError(w, http.StatusUnauthorized, "unauthorized", "Invalid or revoked API token.")
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "internal_error", "Token lookup failed.")
			return
		}
		if token.ExpiresAt.Valid && token.ExpiresAt.Time.Before(time.Now()) {
			WriteError(w, http.StatusUnauthorized, "unauthorized", "API token expired.")
			return
		}
		if !scopeSatisfies(token.Scope, scope) {
			WriteError(w, http.StatusForbidden, "forbidden", "This token's scope ("+token.Scope+") cannot perform a "+scope+" operation.")
			return
		}

		// Budget is spent by authenticated identity, not by address: keyed on
		// the token row id rather than the plaintext, so the limiter map
		// never holds a live credential. Checked after scope so a 429 always
		// means "you are over budget", never "you were also unauthorized".
		if m.Limiter != nil && !m.Limiter.Allow(strconv.FormatInt(token.ID, 10)) {
			w.Header().Set("Retry-After", "1")
			WriteError(w, http.StatusTooManyRequests, "rate_limited",
				"This API token is over its request budget. Retry shortly, or spread work across tokens.")
			return
		}

		org, err := m.Q.GetOrgByClerkID(r.Context(), token.ClerkOrgID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "internal_error", "Organization lookup failed.")
			return
		}
		// Async last-used touch — never on the request path.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := m.Q.TouchAPIToken(ctx, token.ID); err != nil {
				slog.Error("api token touch", "error", err)
			}
		}()

		next.ServeHTTP(w, r.WithContext(identity.WithOrg(r.Context(), &org)))
	})
}

// scopeSatisfies: write satisfies read.
func scopeSatisfies(have, want string) bool {
	switch want {
	case "read":
		return have == "read" || have == "write"
	case "write":
		return have == "write"
	default:
		return false
	}
}
