package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/jackc/pgx/v5"
)

// POST /webhooks/clerk — Clerk delivers via Svix, so deliveries carry
// svix-id/svix-timestamp/svix-signature headers. (Polar carries webhook-*
// headers instead — the two header families are why one verification lib
// cannot cover both providers.)
func (s *Server) handleClerkWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if s.cfg.ClerkWebhookSecret == "" && !s.cfg.DevAuthBypass {
		http.Error(w, "clerk webhooks not configured", http.StatusServiceUnavailable)
		return
	}
	evt, err := s.identityWebhook.Verify(r.Context(), payload, r.Header)
	if err != nil {
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	msgID := evt.ID
	if msgID == "" {
		http.Error(w, "missing svix-id", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	// Idempotency: ErrNoRows means this delivery was already processed.
	_, err = s.q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: msgID, Provider: "clerk", EventType: evt.Type})
	if errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		http.Error(w, "idempotency", http.StatusInternalServerError)
		return
	}

	if err := s.processIdentityEvent(ctx, evt); err != nil {
		s.log.Error("clerk webhook process", "type", evt.Type, "error", err)
		http.Error(w, "processing failed", http.StatusInternalServerError) // 5xx → Clerk retries
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) processIdentityEvent(ctx context.Context, evt identity.Event) error {
	switch evt.Type {
	case "user.created", "user.updated":
		if evt.User == nil {
			return errors.New("identity webhook: missing user")
		}
		u, err := s.q.UpsertUser(ctx, sqlc.UpsertUserParams{UserID: evt.User.Subject, Email: evt.User.Email, Name: evt.User.Name, AvatarUrl: evt.User.AvatarURL})
		if err != nil {
			return err
		}
		if evt.Type == "user.created" {
			msg, err := mail.WelcomeMessage(i18n.ParseOrDefault(u.Locale), s.cfg.AppURL, evt.User.Email, evt.User.Name)
			if err != nil {
				return err
			}
			return jobs.EnqueueEmail(ctx, s.q, jobs.KindWelcome, msg, "", time.Time{})
		}
	case "user.deleted":
		if evt.User == nil {
			return errors.New("identity webhook: missing user")
		}
		return s.q.DeleteUser(ctx, evt.User.Subject)
	case "organization.created", "organization.updated":
		if evt.Organization == nil {
			return errors.New("identity webhook: missing organization")
		}
		_, err := s.q.UpsertOrg(ctx, sqlc.UpsertOrgParams{OrgID: evt.Organization.Subject, Name: evt.Organization.Name, Slug: evt.Organization.Slug, ImageUrl: evt.Organization.ImageURL})
		return err
	case "organization.deleted":
		if evt.Organization == nil {
			return errors.New("identity webhook: missing organization")
		}
		return s.q.DeleteOrg(ctx, evt.Organization.Subject)
	case "organizationMembership.created", "organizationMembership.updated", "organizationMembership.deleted":
		if evt.Membership == nil {
			return errors.New("identity webhook: missing membership")
		}
		m := evt.Membership
		if evt.Type == "organizationMembership.deleted" {
			if err := s.q.DeleteMembership(ctx, sqlc.DeleteMembershipParams{OrgID: m.OrganizationSubject, UserID: m.UserSubject}); err != nil {
				return err
			}
			audit.Log(ctx, s.q, m.OrganizationSubject, m.UserSubject, "member.left", nil)
			return nil
		}
		return s.q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{OrgID: m.OrganizationSubject, UserID: m.UserSubject, Role: m.Role})
	}
	return nil
}
