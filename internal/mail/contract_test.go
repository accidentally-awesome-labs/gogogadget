package mail

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// runSenderContract is shared by adapter contract tests through this seam's
// documented behavior: adapters receive a fully rendered Message and surface
// delivery errors to callers.
func runSenderContract(t *testing.T, factory func(t *testing.T) Sender) {
	t.Helper()
	msg := Message{To: "contract@example.com", Subject: "Contract subject", HTML: "<p>Contract body</p>", Text: "Contract body"}
	s := factory(t)
	require.NoError(t, s.Send(context.Background(), msg))
}
