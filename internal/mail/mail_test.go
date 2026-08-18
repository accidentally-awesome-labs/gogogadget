package mail

import (
	"io"
	"log/slog"
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

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
