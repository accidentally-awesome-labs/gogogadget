package clerk

import (
	"context"
	"errors"
	"net/http"

	svix "github.com/svix/svix-webhooks/go"

	"github.com/gogogadget/gogogadget/internal/identity"
)

// messageIDHeader is Clerk's delivery id. Clerk delivers through Svix, so its
// header family is svix-id/svix-timestamp/svix-signature — a provider detail
// that must never leak past this package. Polar's webhook-* family is why one
// verification library cannot cover both providers.
const messageIDHeader = "svix-id"

// Webhook is the Clerk webhook receiver contract: svix signature verification
// plus Clerk payload parsing, in and out through identity.Event.
type Webhook struct{ Secret string }

func (w Webhook) Verify(_ context.Context, payload []byte, headers http.Header) (identity.Event, error) {
	if w.Secret == "" {
		return identity.Event{}, errors.New("identity clerk: webhook secret is required")
	}
	wh, err := svix.NewWebhook(w.Secret)
	if err != nil {
		return identity.Event{}, err
	}
	if err := wh.Verify(payload, headers); err != nil {
		return identity.Event{}, err
	}
	return parseEvent(headers.Get(messageIDHeader), payload)
}

var _ identity.Webhook = Webhook{}
