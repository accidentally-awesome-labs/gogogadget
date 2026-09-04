package web

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The receiver at /webhooks/clerk is provider-neutral: it records
// idempotency and mirrors an identity.Event into local rows. These fixtures
// are therefore written in the wire format of the adapter the harness
// selects (the dev adapter). Signature verification and each hosted
// provider's payload shape are contract-tested in the adapter packages.

func userCreatedPayload(id, email, name string) []byte {
	return []byte(fmt.Sprintf(
		`{"type":"user.created","data":{"id":%q,"email":%q,"name":%q,"avatar_url":"https://img.example.com/x.png"}}`,
		id, email, name))
}

func orgPayload(eventType, id, name, slug string) []byte {
	return []byte(fmt.Sprintf(`{"type":%q,"data":{"id":%q,"name":%q,"slug":%q}}`, eventType, id, name, slug))
}

func membershipPayload(eventType, orgID, userID, role string) []byte {
	return []byte(fmt.Sprintf(
		`{"type":%q,"data":{"organization_id":%q,"user_id":%q,"role":%q}}`, eventType, orgID, userID, role))
}

func TestUserCreatedWebhook(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	payload := userCreatedPayload("user_wh1", "wh1@example.com", "Will Hughes")
	msgID := "msg_wh1"

	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, identityDelivery(msgID))
	assert.Equal(t, http.StatusOK, code)

	// Mirror row is reached through the provider-subject mapping.
	mapping, err := s.q.GetIdentitySubject(ctx, sqlc.GetIdentitySubjectParams{Provider: "dev", Subject: "user_wh1"})
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
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload, identityDelivery(msgID))
	assert.Equal(t, http.StatusOK, code)
	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = 'email.welcome'`).Scan(&n))
	assert.Equal(t, 1, n)
	_ = s.q.DeleteUser(ctx, mapping.UserID)
}

// TestIdentityWebhookRejectedByAdapter proves the receiver surfaces an
// adapter refusal as 400 without touching the database. The refusal here is
// a payload the dev envelope cannot parse; hosted adapters refuse on
// signature, which their own suites cover.
func TestIdentityWebhookRejectedByAdapter(t *testing.T) {
	s := integrationServer(t, nil)
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", []byte(`{"type":"user.created","data":{}}`), identityDelivery("msg_bad"))
	assert.Equal(t, http.StatusBadRequest, code)
}

// TestIdentityWebhookRequiresDeliveryID proves the receiver refuses an event
// with no adapter-supplied message id: without one there is no idempotency
// key, and a retry would double-process.
func TestIdentityWebhookRequiresDeliveryID(t *testing.T) {
	s := integrationServer(t, nil)
	payload := userCreatedPayload("user_noid", "noid@example.com", "No Id")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, http.Header{})
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestMembershipWebhook(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedUser(t, s, "user_mem", "mem@example.com", "Mem")
	seedOrg(t, s, "org_mem", "mem")

	payload := membershipPayload("organizationMembership.created", "org_mem", "user_mem", "org:admin")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, identityDelivery("msg_mem1"))
	assert.Equal(t, http.StatusOK, code)

	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: "org_mem", UserID: "user_mem"})
	require.NoError(t, err)
	assert.Equal(t, "org:admin", m.Role)

	// Custom roles must not wedge the webhook (no CHECK constraint).
	payload2 := membershipPayload("organizationMembership.updated", "org_mem", "user_mem", "org:billing_manager")
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload2, identityDelivery("msg_mem2"))
	assert.Equal(t, http.StatusOK, code)
	m, _ = s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: "org_mem", UserID: "user_mem"})
	assert.Equal(t, "org:billing_manager", m.Role)

	// Deleted → row gone.
	payload3 := membershipPayload("organizationMembership.deleted", "org_mem", "user_mem", "org:billing_manager")
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload3, identityDelivery("msg_mem3"))
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

	payload := orgPayload("organization.deleted", "org_del", "Del", "del")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, identityDelivery("msg_del1"))
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

	payload := orgPayload("organization.deleted", "org_del2", "Del2", "del2")
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, identityDelivery("msg_del2"))
	assert.Equal(t, http.StatusInternalServerError, code, "revoke failure must 500 so the provider retries")

	// Mirror row retained (the delete never ran).
	_, err = s.q.GetOrgByID(ctx, "org_del2")
	require.NoError(t, err)
}
