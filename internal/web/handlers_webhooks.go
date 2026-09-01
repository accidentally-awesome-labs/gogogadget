package web

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"time"
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
	_, err = s.q.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{ID: msgID, Provider: evt.Provider, EventType: evt.Type})
	if errors.Is(err, pgx.ErrNoRows) {
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
		u, err := s.identityUser(ctx, evt.Provider, *evt.User)
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
		mapped, err := s.q.GetIdentitySubject(ctx, sqlc.GetIdentitySubjectParams{Provider: evt.Provider, Subject: evt.User.Subject})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.q.DeleteUser(ctx, mapped.UserID); err != nil {
			return err
		}
	case "organization.created", "organization.updated":
		if evt.Organization == nil {
			return errors.New("identity webhook: missing organization")
		}
		_, err := s.identityOrg(ctx, evt.Provider, *evt.Organization)
		return err
	case "organization.deleted":
		if evt.Organization == nil {
			return errors.New("identity webhook: missing organization")
		}
		mapped, err := s.q.GetIdentityOrganization(ctx, sqlc.GetIdentityOrganizationParams{Provider: evt.Provider, Subject: evt.Organization.Subject})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.q.DeleteOrg(ctx, mapped.OrgID)
	case "organizationMembership.created", "organizationMembership.updated", "organizationMembership.deleted":
		if evt.Membership == nil {
			return errors.New("identity webhook: missing membership")
		}
		m := evt.Membership
		user, err := s.q.GetIdentitySubject(ctx, sqlc.GetIdentitySubjectParams{Provider: evt.Provider, Subject: m.UserSubject})
		if err != nil {
			return fmt.Errorf("identity webhook user subject %q: %w", m.UserSubject, err)
		}
		org, err := s.q.GetIdentityOrganization(ctx, sqlc.GetIdentityOrganizationParams{Provider: evt.Provider, Subject: m.OrganizationSubject})
		if err != nil {
			return fmt.Errorf("identity webhook organization subject %q: %w", m.OrganizationSubject, err)
		}
		if evt.Type == "organizationMembership.deleted" {
			if err := s.q.DeleteMembership(ctx, sqlc.DeleteMembershipParams{OrgID: org.OrgID, UserID: user.UserID}); err != nil {
				return err
			}
			audit.Log(ctx, s.q, org.OrgID, user.UserID, "member.left", nil)
			return nil
		}
		return s.q.UpsertMembership(ctx, sqlc.UpsertMembershipParams{OrgID: org.OrgID, UserID: user.UserID, Role: m.Role})
	}
	return nil
}

func (s *Server) identityUser(ctx context.Context, provider string, event identity.UserEvent) (sqlc.User, error) {
	mapped, err := s.q.GetIdentitySubject(ctx, sqlc.GetIdentitySubjectParams{Provider: provider, Subject: event.Subject})
	if err == nil {
		return s.q.GetUserByID(ctx, mapped.UserID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, err
	}
	if _, err := s.q.GetUserByEmail(ctx, event.Email); err == nil {
		return sqlc.User{}, identity.ErrLinkRequired
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, err
	}
	id, err := opaqueWebhookID("usr_")
	if err != nil {
		return sqlc.User{}, err
	}
	u, err := s.q.UpsertUser(ctx, sqlc.UpsertUserParams{UserID: id, Email: event.Email, Name: event.Name, AvatarUrl: event.AvatarURL})
	if err != nil {
		return sqlc.User{}, err
	}
	inserted, err := s.q.InsertIdentitySubject(ctx, sqlc.InsertIdentitySubjectParams{Provider: provider, Subject: event.Subject, UserID: u.UserID})
	if err == nil {
		return insertedUser(s.q, ctx, inserted.UserID)
	}
	_ = s.q.DeleteUser(ctx, u.UserID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, err
	}
	mapped, err = s.q.GetIdentitySubject(ctx, sqlc.GetIdentitySubjectParams{Provider: provider, Subject: event.Subject})
	if err != nil {
		return sqlc.User{}, err
	}
	return s.q.GetUserByID(ctx, mapped.UserID)
}

func insertedUser(q *sqlc.Queries, ctx context.Context, id string) (sqlc.User, error) {
	return q.GetUserByID(ctx, id)
}

func (s *Server) identityOrg(ctx context.Context, provider string, event identity.OrganizationEvent) (sqlc.Org, error) {
	mapped, err := s.q.GetIdentityOrganization(ctx, sqlc.GetIdentityOrganizationParams{Provider: provider, Subject: event.Subject})
	if err == nil {
		return s.q.GetOrgByID(ctx, mapped.OrgID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Org{}, err
	}
	if _, err := s.q.GetOrgBySlug(ctx, event.Slug); err == nil {
		return sqlc.Org{}, identity.ErrLinkRequired
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Org{}, err
	}
	id, err := opaqueWebhookID("org_")
	if err != nil {
		return sqlc.Org{}, err
	}
	o, err := s.q.UpsertOrg(ctx, sqlc.UpsertOrgParams{OrgID: id, Name: event.Name, Slug: event.Slug, ImageUrl: event.ImageURL})
	if err != nil {
		return sqlc.Org{}, err
	}
	inserted, err := s.q.InsertIdentityOrganization(ctx, sqlc.InsertIdentityOrganizationParams{Provider: provider, Subject: event.Subject, OrgID: o.OrgID})
	if err == nil {
		return s.q.GetOrgByID(ctx, inserted.OrgID)
	}
	_ = s.q.DeleteOrg(ctx, o.OrgID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Org{}, err
	}
	mapped, err = s.q.GetIdentityOrganization(ctx, sqlc.GetIdentityOrganizationParams{Provider: provider, Subject: event.Subject})
	if err != nil {
		return sqlc.Org{}, err
	}
	return s.q.GetOrgByID(ctx, mapped.OrgID)
}

func opaqueWebhookID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + fmt.Sprintf("%x", b[:]), nil
}
