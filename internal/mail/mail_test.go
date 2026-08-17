package mail

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"
)

func TestMessageBuildersRenderHTMLAndText(t *testing.T) {
	appURL := "http://localhost:8080"

	welcome, err := WelcomeMessage(language.English, appURL, "a@example.com", "Ada")
	require.NoError(t, err)
	assert.Equal(t, "a@example.com", welcome.To)
	assert.Equal(t, "Welcome to GoGoGadget", welcome.Subject)
	assert.Contains(t, welcome.HTML, "<table", "HTML part must use the email layout")
	assert.Contains(t, welcome.HTML, "Ada")
	assert.Contains(t, welcome.HTML, appURL+"/app")
	assert.Contains(t, welcome.Text, "Ada")
	assert.NotContains(t, welcome.Text, "<table", "text part must not carry markup")

	failed, err := PaymentFailedMessage(language.English, appURL, "b@example.com")
	require.NoError(t, err)
	assert.Contains(t, failed.HTML, "payment failed")
	assert.Contains(t, failed.Text, appURL+"/app/settings/billing")

	canceled, err := SubscriptionCanceledMessage(language.English, appURL, "c@example.com", "2026-02-15")
	require.NoError(t, err)
	assert.Contains(t, canceled.HTML, "2026-02-15")
	assert.Contains(t, canceled.Text, "2026-02-15")

	trial, err := TrialEndingMessage(language.English, appURL, "d@example.com", "2026-02-01")
	require.NoError(t, err)
	assert.Contains(t, trial.HTML, "2026-02-01")
	assert.Contains(t, trial.Text, "2026-02-01")
}

func TestDevSenderWritesFile(t *testing.T) {
	dir := t.TempDir()
	s := NewDevSender(newTestLogger(), dir)
	err := s.Send(context.Background(), Message{To: "x@example.com", Subject: "s", HTML: "<b>body</b>", Text: "body"})
	require.NoError(t, err)
	assertFileExists(t, dir)
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func assertFileExists(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.html"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	raw, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), "<b>body</b>")
}
