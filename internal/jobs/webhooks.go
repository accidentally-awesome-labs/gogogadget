package jobs

import "time"

// The enqueue side of outbound webhooks. internal/webhooks writes a delivery
// row and queues a webhook.deliver job; the sender that drains those rows is a
// separate module, and a project may run the emitter without it — an
// undispatched kind dead-letters as module_uninstalled, which is a reported
// outcome rather than a build failure. So the enqueue contract and the rotation
// window live with the emitter, not with the sender.

// WebhookRotationGrace is how long a rotated-out secret keeps signing
// alongside the new one, so receivers can roll over without dropped
// deliveries. The janitor clears previous secrets past this window, and the
// sender signs with both while it is open.
const WebhookRotationGrace = 24 * time.Hour

// WebhookDeliverPayload is the enqueue contract for webhook.deliver.
type WebhookDeliverPayload struct {
	DeliveryID int64 `json:"delivery_id"`
}
