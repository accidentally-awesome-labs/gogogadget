package web

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The receiver at /webhooks/clerk is provider-neutral: it records
// idempotency and mirrors an identity.Event into local rows. So are these
// fixtures now — each one is a typed identity.Event that the seam's own
// double encodes (identityDelivery). They used to be hand-written JSON in
// the wire format of the adapter the harness happened to select, which is a
// coupling no import-based check can see, because this file imports no
// adapter. Signature verification and each hosted provider's payload shape
// are contract-tested in the adapter packages.

func userCreatedDelivery(deliveryID, subject, email, name string) ([]byte, http.Header) {
	return identityDelivery(deliveryID, identity.Event{
		Type: "user.created",
		User: &identity.UserEvent{Subject: subject, Email: email, Name: name, AvatarURL: "https://img.example.com/x.png"},
	})
}

func orgDelivery(deliveryID, eventType, subject, name, slug string) ([]byte, http.Header) {
	return identityDelivery(deliveryID, identity.Event{
		Type:         eventType,
		Organization: &identity.OrganizationEvent{Subject: subject, Name: name, Slug: slug},
	})
}

func membershipDelivery(deliveryID, eventType, orgSubject, userSubject, role string) ([]byte, http.Header) {
	return identityDelivery(deliveryID, identity.Event{
		Type:       eventType,
		Membership: &identity.MembershipEvent{OrganizationSubject: orgSubject, UserSubject: userSubject, Role: role},
	})
}

func TestUserCreatedWebhook(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	payload, headers := userCreatedDelivery("msg_wh1", "user_wh1", "wh1@example.com", "Will Hughes")

	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, headers)
	assert.Equal(t, http.StatusOK, code)

	// Mirror row is reached through the provider-subject mapping.
	mapping, err := s.q.GetIdentitySubject(ctx, sqlc.GetIdentitySubjectParams{Provider: identity.MockProvider, Subject: "user_wh1"})
	require.NoError(t, err)
	u, err := s.q.GetUserByID(ctx, mapping.UserID)
	require.NoError(t, err)
	assert.Equal(t, "wh1@example.com", string(u.Email))
	assert.Equal(t, "Will Hughes", u.Name)

	// Welcome email job enqueued (rendered bodies in the payload).
	var kind string
	var jobPayload []byte
	require.NoError(t, s.db.QueryRow(ctx, `SELECT kind, payload FROM jobs WHERE kind = 'email.welcome'`).Scan(&kind, &jobPayload))
	assert.Contains(t, string(jobPayload), "wh1@example.com")

	// Duplicate delivery → still 200, no second welcome job.
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload, headers)
	assert.Equal(t, http.StatusOK, code)
	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = 'email.welcome'`).Scan(&n))
	assert.Equal(t, 1, n)
	_ = s.q.DeleteUser(ctx, mapping.UserID)
}

// TestIdentityWebhookRejectedByAdapter proves the receiver surfaces an
// adapter refusal as 400 without touching the database. The refusal here is
// an incomplete event — a user event naming no subject, which every adapter
// refuses; hosted adapters also refuse on signature, which their own suites
// cover.
func TestIdentityWebhookRejectedByAdapter(t *testing.T) {
	s := integrationServer(t, nil)
	payload, headers := identityDelivery("msg_bad", identity.Event{Type: "user.created"})
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, headers)
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestIdentityWebhookRequiresDeliveryID proves the receiver refuses an event
// with no adapter-supplied message id: without one there is no idempotency
// key, and a retry would double-process.
func TestIdentityWebhookRequiresDeliveryID(t *testing.T) {
	s := integrationServer(t, nil)
	payload, _ := userCreatedDelivery("", "user_noid", "noid@example.com", "No Id")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, http.Header{})
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestMembershipWebhook(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedUser(t, s, "user_mem", "mem@example.com", "Mem")
	seedOrg(t, s, "org_mem", "mem")

	payload, headers := membershipDelivery("msg_mem1", "organizationMembership.created", "org_mem", "user_mem", "org:admin")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, headers)
	assert.Equal(t, http.StatusOK, code)

	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: "org_mem", UserID: "user_mem"})
	require.NoError(t, err)
	assert.Equal(t, "org:admin", m.Role)

	// Custom roles must not wedge the webhook (no CHECK constraint).
	payload2, headers2 := membershipDelivery("msg_mem2", "organizationMembership.updated", "org_mem", "user_mem", "org:billing_manager")
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload2, headers2)
	assert.Equal(t, http.StatusOK, code)
	m, _ = s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: "org_mem", UserID: "user_mem"})
	assert.Equal(t, "org:billing_manager", m.Role)

	// Deleted → row gone.
	payload3, headers3 := membershipDelivery("msg_mem3", "organizationMembership.deleted", "org_mem", "user_mem", "org:billing_manager")
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload3, headers3)
	assert.Equal(t, http.StatusOK, code)
	_, err = s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: "org_mem", UserID: "user_mem"})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestOrgDeletedRevokesBilling(t *testing.T) {
	mock := &billing.MockClient{}
	s := integrationServer(t, func(d *Deps) { d.Billing = mock })
	ctx := t.Context()
	seedOrg(t, s, "org_del", "del")

	_, err := s.q.UpsertSubscription(ctx, sqlc.UpsertSubscriptionParams{
		Provider: "polar",
		OrgID:    "org_del", ProviderSubscriptionID: pgtype.Text{String: "sub_del", Valid: true},
		ProviderCustomerID: "cust_del", ProductKey: "pro", Status: "active",
	})
	require.NoError(t, err)

	payload, headers := orgDelivery("msg_del1", "organization.deleted", "org_del", "Del", "del")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, headers)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{"sub_del"}, mock.RevokedIDs, "revoke must fire BEFORE the mirror delete")

	_, err = s.q.GetOrgByID(ctx, "org_del")
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestOrgDeletedRevokeFailureMeans500(t *testing.T) {
	mock := &billing.MockClient{RevokeErr: errors.New("polar down")}
	s := integrationServer(t, func(d *Deps) { d.Billing = mock })
	ctx := t.Context()
	seedOrg(t, s, "org_del2", "del2")

	_, err := s.q.UpsertSubscription(ctx, sqlc.UpsertSubscriptionParams{
		Provider: "polar",
		OrgID:    "org_del2", ProviderSubscriptionID: pgtype.Text{String: "sub_del2", Valid: true},
		ProviderCustomerID: "cust_del2", ProductKey: "pro", Status: "active",
	})
	require.NoError(t, err)

	payload, headers := orgDelivery("msg_del2", "organization.deleted", "org_del2", "Del2", "del2")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, headers)
	assert.Equal(t, http.StatusInternalServerError, code, "revoke failure must 500 so the provider retries")

	// Mirror row retained (the delete never ran).
	_, err = s.q.GetOrgByID(ctx, "org_del2")
	require.NoError(t, err)
}

// A lazily seeded organization carries its subject as a placeholder name,
// because a session arrives before any webhook does. The first
// organization.created delivery has to correct it: an organization named
// "org_2x9f" in every heading is what the mirror looks like if this never runs.
//
// This claim used to sit in ggg/workflow/auth-session's auth_test.go, calling
// orgDelivery - a fixture this payload defines - so the session module's tests
// only compiled in a closure that also installed this one, which no shipped
// profile does.
func TestOrgCreatedCorrectsALazilySeededName(t *testing.T) {
	s := integrationServer(t, nil)
	seedUser(t, s, "user_lazy", "lazy@example.com", "Lazy")
	code, _, _ := serve(t, s, "GET", "/app", nil, nil, sessionCookie("user_lazy", "org_lazy", "org:admin"))
	require.Equal(t, http.StatusOK, code)

	mapping, err := s.q.GetIdentityOrganization(t.Context(),
		sqlc.GetIdentityOrganizationParams{Provider: identity.MockProvider, Subject: "org_lazy"})
	require.NoError(t, err)
	org, err := s.q.GetOrgByID(t.Context(), mapping.OrgID)
	require.NoError(t, err)
	require.Equal(t, "org_lazy", org.Name, "the lazily seeded name is the subject")

	payload, headers := orgDelivery("msg_lazy1", "organization.created", "org_lazy", "Real Name", "org_lazy")
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload, headers)
	require.Equal(t, http.StatusOK, code)
	org, err = s.q.GetOrgByID(t.Context(), mapping.OrgID)
	require.NoError(t, err)
	assert.Equal(t, "Real Name", org.Name)
}
