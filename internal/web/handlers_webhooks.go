package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/jackc/pgx/v5"
	svix "github.com/svix/svix-webhooks/go"
)

// POST /webhooks/clerk — Clerk delivers via Svix, so deliveries carry
// svix-id/svix-timestamp/svix-signature headers. (Polar carries webhook-*
// headers instead — the two header families are why one verification lib
// cannot cover both providers.)
func (s *Server) handleClerkWebhook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ClerkWebhookSecret == "" {
		http.Error(w, "clerk webhooks not configured", http.StatusServiceUnavailable)
		return
	}
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	wh, err := svix.NewWebhook(s.cfg.ClerkWebhookSecret)
	if err != nil {
		s.log.Error("clerk webhook init", "error", err)
		http.Error(w, "webhook config", http.StatusInternalServerError)
		return
	}
	if err := wh.Verify(payload, r.Header); err != nil {
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	msgID := r.Header.Get("svix-id")
	if msgID == "" {
		http.Error(w, "missing svix-id", http.StatusBadRequest)
		return
	}
	var evt identity.ClerkEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
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

	if err := s.processClerkEvent(ctx, evt); err != nil {
		s.log.Error("clerk webhook process", "type", evt.Type, "error", err)
		http.Error(w, "processing failed", http.StatusInternalServerError) // 5xx → Clerk retries
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) processClerkEvent(ctx context.Context, evt identity.ClerkEvent) error {
	switch evt.Type {
	case "user.created", "user.updated":
		id, profile, err := identity.ParseUserData(evt.Data)
		if err != nil {
			return err
		}
		if _, err := s.q.UpsertUser(ctx, sqlc.UpsertUserParams{
			ClerkUserID: id, Email: profile.Email, Name: profile.Name, AvatarUrl: profile.AvatarURL,
		}); err != nil {
			return err
		}
		if s.cfg.AdminEmail != "" && strings.EqualFold(profile.Email, s.cfg.AdminEmail) {
			if err := s.q.SetUserAdminByEmail(ctx, sqlc.SetUserAdminByEmailParams{Email: profile.Email, IsAdmin: true}); err != nil {
				return err
			}
		}
		if evt.Type == "user.created" {
			msg, err := mail.WelcomeMessage(s.cfg.AppURL, profile.Email, profile.Name)
			if err != nil {
				return err
			}
			if err := jobs.EnqueueEmail(ctx, s.q, jobs.KindWelcome, msg, "", time.Time{}); err != nil {
				return err
			}
		}
		return nil

	case "user.deleted":
		id, err := identity.ParseUserDeletedData(evt.Data)
		if err != nil {
			return err
		}
		return s.q.DeleteUser(ctx, id)

	case "organization.created", "organization.updated":
		id, name, slug, imageURL, err := identity.ParseOrgData(evt.Data)
		if err != nil {
			return err
		}
		_, err = s.q.UpsertOrg(ctx, sqlc.UpsertOrgParams{ClerkOrgID: id, Name: name, Slug: slug, ImageUrl: imageURL})
		return err

	case "organization.deleted":
		id, _, _, _, err := identity.ParseOrgData(evt.Data)
		if err != nil {
			return err
		}
		// FIRST revoke billing: an org must never be deleted while Polar keeps
		// charging it. API failure → 500 so Clerk retries.
		sub, err := s.q.GetSubscriptionByOrg(ctx, id)
		if err == nil && sub.PolarSubscriptionID.Valid &&
			(sub.Status == "active" || sub.Status == "trialing" || sub.Status == "past_due") {
			if s.billingClient == nil {
				s.log.Warn("org deleted with live subscription but billing is not configured; cannot revoke", "org", id)
			} else if err := s.billingClient.RevokeSubscription(ctx, sub.PolarSubscriptionID.String); err != nil {
				return err
			}
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return s.q.DeleteOrg(ctx, id)

	case "organizationMembership.created", "organizationMembership.updated":
		orgID, userID, role, err := identity.ParseMembershipData(evt.Data)
		if err != nil {
			return err
		}
		// Role is stored raw: no CHECK constraint, so buyer-added custom roles
		// never wedge membership webhooks.
		existing, merr := s.q.GetMembership(ctx, sqlc.GetMembershipParams{ClerkOrgID: orgID, ClerkUserID: userID})
		if merr != nil && !errors.Is(merr, pgx.ErrNoRows) {
			return merr
		}
		if err := s.q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{ClerkOrgID: orgID, ClerkUserID: userID, Role: role}); err != nil {
			return err
		}
		switch {
		case errors.Is(merr, pgx.ErrNoRows) && evt.Type == "organizationMembership.created":
			audit.Log(ctx, s.q, orgID, userID, "member.joined", map[string]any{"role": role})
		case merr == nil && existing.Role != role:
			audit.Log(ctx, s.q, orgID, userID, "member.role_changed", map[string]any{"from": existing.Role, "to": role})
		}
		return nil

	case "organizationMembership.deleted":
		orgID, userID, _, err := identity.ParseMembershipData(evt.Data)
		if err != nil {
			return err
		}
		if err := s.q.DeleteMembership(ctx, sqlc.DeleteMembershipParams{ClerkOrgID: orgID, ClerkUserID: userID}); err != nil {
			return err
		}
		audit.Log(ctx, s.q, orgID, userID, "member.left", nil)
		return nil

	default:
		s.log.Info("clerk webhook: unhandled event (ignored)", "type", evt.Type)
		return nil
	}
}
