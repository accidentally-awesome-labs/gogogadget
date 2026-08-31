package contract

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/stretchr/testify/require"
	"testing"
)

// Run exercises the provider-neutral Sender contract for every adapter,
// including propagation of caller cancellation.
func Run(t *testing.T, factory func() mail.Sender) {
	t.Helper()
	sender := factory()
	require.NotNil(t, sender)
	msg := mail.Message{To: "contract@example.com", Subject: "Contract subject", HTML: "<p>Contract body</p>", Text: "Contract body"}
	require.NoError(t, sender.Send(context.Background(), msg))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sender.Send(cancelled, msg), context.Canceled)
}
