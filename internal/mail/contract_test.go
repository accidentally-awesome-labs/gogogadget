package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSenderContract is the Sender seam contract. Every implementation a test
// can fake runs it: DevSender (file lands on disk) and ResendSender (via the
// SDK's exported BaseURL — the same injection its own upstream tests use).
// Message builders and sanitizeFilename have their own tests.
func runSenderContract(t *testing.T, factory func(t *testing.T) Sender, assertDelivered func(t *testing.T, msg Message)) {
	t.Helper()
	msg := Message{To: "contract@example.com", Subject: "Contract subject", HTML: "<p>Contract body</p>", Text: "Contract body"}

	s := factory(t)
	require.NoError(t, s.Send(context.Background(), msg), "Send must accept a rendered message and return nil on success")
	assertDelivered(t, msg)
}

func TestDevSenderContract(t *testing.T) {
	dir := t.TempDir()
	runSenderContract(t,
		func(t *testing.T) Sender { return NewDevSender(newTestLogger(), dir) },
		func(t *testing.T, msg Message) {
			matches, err := filepath.Glob(filepath.Join(dir, "*.html"))
			require.NoError(t, err)
			require.Len(t, matches, 1, "exactly one file per send")
			raw, err := os.ReadFile(matches[0])
			require.NoError(t, err)
			assert.Contains(t, string(raw), msg.HTML, "file carries the rendered HTML body")
		})
}

func TestResendSenderContract(t *testing.T) {
	var got SendEmailRequestCapture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/emails", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"email-1"}`))
	}))
	t.Cleanup(srv.Close)

	rs := NewResendSender("re_test_key", "noreply@example.com")
	base, err := url.Parse(srv.URL)
	require.NoError(t, err)
	rs.client.BaseURL = base // exported field; upstream tests inject the same way

	runSenderContract(t,
		func(t *testing.T) Sender { return rs },
		func(t *testing.T, msg Message) {
			assert.Equal(t, "noreply@example.com", got.From)
			assert.Equal(t, []string{msg.To}, got.To)
			assert.Equal(t, msg.Subject, got.Subject)
			assert.Equal(t, msg.HTML, got.Html)
			assert.Equal(t, msg.Text, got.Text)
		})
}

func TestResendSenderContractProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"invalid from"}`))
	}))
	t.Cleanup(srv.Close)

	rs := NewResendSender("re_test_key", "bad")
	base, err := url.Parse(srv.URL)
	require.NoError(t, err)
	rs.client.BaseURL = base

	err = rs.Send(context.Background(), Message{To: "x@example.com", Subject: "s", HTML: "b"})
	require.Error(t, err, "provider 4xx must surface as an error, never a silent drop")
}

// SendEmailRequestCapture mirrors resend.SendEmailRequest without exporting
// the SDK type into the test surface.
type SendEmailRequestCapture struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Html    string   `json:"html"`
	Text    string   `json:"text"`
}
