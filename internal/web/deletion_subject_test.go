package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

type deletionSpy struct{ subject string }

func (s *deletionSpy) DeleteUser(_ context.Context, subject string) error {
	s.subject = subject
	return nil
}

func TestAccountDeletionPassesProviderSubject(t *testing.T) {
	spy := &deletionSpy{}
	s := integrationServer(t, func(d *Deps) { d.IdentityDeleter = spy })
	seedMembership(t, s, "user_delete_subject", "org_delete_subject", "org:admin")
	form := url.Values{"confirm_email": {"user_delete_subject@example.com"}}
	code, _, _ := postForm(t, s, "/app/settings/account/delete", form, sessionCookie("user_delete_subject", "org_delete_subject", "org:admin"))
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "user_delete_subject", spy.subject)
	require.NotEqual(t, "usr_user_delete_subject", spy.subject)
}
