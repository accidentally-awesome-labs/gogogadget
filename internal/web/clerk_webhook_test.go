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

func userCreatedPayload(id, emailID, email, first, last string) []byte {
	return []byte(fmt.Sprintf(`{
	  "type": "user.created",
	  "data": {
	    "id": %q,
	    "email_addresses": [{"id": %q, "email_address": %q}],
	    "primary_email_address_id": %q,
	    "first_name": %q, "last_name": %q, "image_url": "https://img.clerk.com/x.png"
	  }
	}`, id, emailID, email, emailID, first, last))
}

func TestUserCreatedWebhook(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	payload := userCreatedPayload("user_wh1", "em_1", "wh1@example.com", "Will", "Hughes")
	msgID := "msg_wh1"

	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, signSvix(t, testWebhookSecret, msgID, payload))
	assert.Equal(t, http.StatusOK, code)

	// Mirror row exists with the display name.
	u, err := s.q.GetUserByID(ctx, "user_wh1")
	require.NoError(t, err)
	assert.Equal(t, "wh1@example.com", string(u.Email))
	assert.Equal(t, "Will Hughes", u.Name)

	// Welcome email job enqueued (rendered bodies in the payload).
	var kind string
	var jobPayload []byte
	require.NoError(t, s.db.QueryRow(ctx, `SELECT kind, payload FROM jobs WHERE kind = 'email.welcome'`).Scan(&kind, &jobPayload))
	assert.Contains(t, string(jobPayload), "wh1@example.com")

	// Duplicate delivery → still 200, no second welcome job.
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload, signSvix(t, testWebhookSecret, msgID, payload))
	assert.Equal(t, http.StatusOK, code)
	var n int
	require.NoError(t, s.db.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = 'email.welcome'`).Scan(&n))
	assert.Equal(t, 1, n)

	_ = s.q.DeleteUser(ctx, "user_wh1")
}

func TestClerkWebhookBadSignature(t *testing.T) {
	s := integrationServer(t, nil)
	payload := userCreatedPayload("user_bad", "em_1", "bad@example.com", "B", "A")
	h := signSvix(t, "whsec_aGVsbG8td29ybGQtdGhpcy1pcy13cm9uZw==", "msg_bad", payload)
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, h)
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestMembershipWebhook(t *testing.T) {
	s := integrationServer(t, nil)
	ctx := t.Context()
	seedUser(t, s, "user_mem", "mem@example.com", "Mem")
	seedOrg(t, s, "org_mem", "mem")

	payload := []byte(`{
	  "type": "organizationMembership.created",
	  "data": {"organization": {"id": "org_mem"}, "public_user_data": {"user_id": "user_mem"}, "role": "org:admin"}
	}`)
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, signSvix(t, testWebhookSecret, "msg_mem1", payload))
	assert.Equal(t, http.StatusOK, code)

	m, err := s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: "org_mem", UserID: "user_mem"})
	require.NoError(t, err)
	assert.Equal(t, "org:admin", m.Role)

	// Custom roles must not wedge the webhook (no CHECK constraint).
	payload2 := []byte(`{
	  "type": "organizationMembership.updated",
	  "data": {"organization": {"id": "org_mem"}, "public_user_data": {"user_id": "user_mem"}, "role": "org:billing_manager"}
	}`)
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload2, signSvix(t, testWebhookSecret, "msg_mem2", payload2))
	assert.Equal(t, http.StatusOK, code)
	m, _ = s.q.GetMembership(ctx, sqlc.GetMembershipParams{OrgID: "org_mem", UserID: "user_mem"})
	assert.Equal(t, "org:billing_manager", m.Role)

	// Deleted → row gone.
	payload3 := []byte(`{
	  "type": "organizationMembership.deleted",
	  "data": {"organization": {"id": "org_mem"}, "public_user_data": {"user_id": "user_mem"}, "role": "org:billing_manager"}
	}`)
	code, _, _ = serve(t, s, "POST", "/webhooks/clerk", payload3, signSvix(t, testWebhookSecret, "msg_mem3", payload3))
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
		OrgID: "org_del", ProviderSubscriptionID: pgtype.Text{String: "sub_del", Valid: true},
		ProviderCustomerID: "cust_del", ProductKey: "pro", Status: "active",
	})
	require.NoError(t, err)

	payload := []byte(`{"type": "organization.deleted", "data": {"id": "org_del", "name": "Del", "slug": "del"}}`)
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, signSvix(t, testWebhookSecret, "msg_del1", payload))
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
		OrgID: "org_del2", ProviderSubscriptionID: pgtype.Text{String: "sub_del2", Valid: true},
		ProviderCustomerID: "cust_del2", ProductKey: "pro", Status: "active",
	})
	require.NoError(t, err)

	payload := []byte(`{"type": "organization.deleted", "data": {"id": "org_del2", "name": "Del2", "slug": "del2"}}`)
	code, _, _ := serve(t, s, "POST", "/webhooks/clerk", payload, signSvix(t, testWebhookSecret, "msg_del2", payload))
	assert.Equal(t, http.StatusInternalServerError, code, "revoke failure must 500 so Clerk retries")

	// Mirror row retained (the delete never ran).
	_, err = s.q.GetOrgByID(ctx, "org_del2")
	require.NoError(t, err)
}
