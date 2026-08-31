package contract

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/stretchr/testify/require"
	"testing"
)

// Run exercises the provider-neutral Sender contract for every adapter.
func Run(t *testing.T, factory func() mail.Sender) {
	t.Helper()
	msg := mail.Message{To: "contract@example.com", Subject: "Contract subject", HTML: "<p>Contract body</p>", Text: "Contract body"}
	require.NoError(t, factory().Send(context.Background(), msg))
}
