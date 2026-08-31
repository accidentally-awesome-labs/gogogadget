package resend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gogogadget/gogogadget/internal/mail"
	mailcontract "github.com/gogogadget/gogogadget/internal/mail/contract"
	"github.com/stretchr/testify/require"
)

type sendEmailRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
	Text    string   `json:"text"`
}

func TestResendSenderContract(t *testing.T) {
	var got sendEmailRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/emails", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"email-1"}`))
	}))
	t.Cleanup(srv.Close)
	sender := NewResendSender("re_test_key", "noreply@example.com")
	base, err := url.Parse(srv.URL)
	require.NoError(t, err)
	sender.client.BaseURL = base
	msg := mail.Message{To: "contract@example.com", Subject: "Contract subject", HTML: "<p>Contract body</p>", Text: "Contract body"}
	require.NoError(t, sender.Send(context.Background(), msg))
	require.Equal(t, "noreply@example.com", got.From)
	mailcontract.Run(t, func() mail.Sender { return sender })
	require.Equal(t, []string{msg.To}, got.To)
	require.Equal(t, msg.Subject, got.Subject)
	require.Equal(t, msg.HTML, got.HTML)
	require.Equal(t, msg.Text, got.Text)
}

func TestResendSenderProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"invalid from"}`))
	}))
	t.Cleanup(srv.Close)
	sender := NewResendSender("re_test_key", "bad")
	base, err := url.Parse(srv.URL)
	require.NoError(t, err)
	sender.client.BaseURL = base
	require.Error(t, sender.Send(context.Background(), mail.Message{To: "x@example.com", Subject: "s", HTML: "b"}))
}

func TestResendHealthReportsProviderStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		ok   bool
	}{{"success", http.StatusOK, true}, {"failure", http.StatusUnauthorized, false}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(`{"data":[]}`))
			}))
			defer srv.Close()
			sender := NewResendSender("re_test_key", "from@example.com")
			base, err := url.Parse(srv.URL + "/")
			require.NoError(t, err)
			sender.client.BaseURL = base
			err = sender.Health(context.Background())
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
