package jobs

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/jackc/pgx/v5/pgtype"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// WebhookRotationGrace is how long a rotated-out secret keeps signing
// alongside the new one, so receivers can roll over without dropped
// deliveries. The janitor clears previous secrets past this window.
const WebhookRotationGrace = 24 * time.Hour

// WebhookDeliverPayload is the enqueue contract for webhook.deliver.
type WebhookDeliverPayload struct {
	DeliveryID int64 `json:"delivery_id"`
}

// deliverWebhook POSTs the stored payload to the endpoint URL, signed with
// the standard-webhooks scheme (webhook-id/-timestamp/-signature), then
// records the outcome: 2xx → success; anything else → error (existing 2^n
// backoff), and at max attempts → dead + the endpoint owner is notified.
func (w *Worker) deliverWebhook(ctx context.Context, p WebhookDeliverPayload, attempt Attempt) error {
	d, err := w.q.GetWebhookDelivery(ctx, p.DeliveryID)
	if err != nil {
		return err
	}
	ep, err := w.q.GetWebhookEndpoint(ctx, sqlc.GetWebhookEndpointParams{ID: d.EndpointID, OrgID: d.OrgID})
	if err != nil {
		return err
	}
	if ep.DisabledAt.Valid {
		return w.q.MarkDeliveryDead(ctx, sqlc.MarkDeliveryDeadParams{ID: d.ID, LastError: "endpoint disabled"})
	}

	// One id per delivery row, stable across every retry of it.
	msgID := webhookMessageID(d.ID)
	status, attemptErr := w.postWebhook(ctx, ep.Url, msgID, signingSecrets(ep, time.Now()), d.Payload)
	pgStatus := pgtype.Int4{Int32: status, Valid: status > 0}
	if attemptErr == nil {
		return w.q.MarkDeliverySuccess(ctx, sqlc.MarkDeliverySuccessParams{ID: d.ID, LastResponseStatus: pgStatus})
	}
	// Record the attempt; the caller (ProcessOne) applies backoff/dead-letter.
	_ = w.q.RecordDeliveryAttempt(ctx, sqlc.RecordDeliveryAttemptParams{
		ID: d.ID, LastResponseStatus: pgStatus, LastError: attemptErr.Error(),
	})
	if attempt.Last() {
		if err := w.q.MarkDeliveryDead(ctx, sqlc.MarkDeliveryDeadParams{ID: d.ID, LastError: attemptErr.Error()}); err != nil {
			return err
		}
		notify.Send(ctx, w.q, d.OrgID, ep.CreatedBy, "webhook.failed",
			"Webhook delivery failed permanently", ep.Url+" — "+attemptErr.Error(), "/app/settings/webhooks")
	}
	return attemptErr
}

// signingSecrets returns the secrets a delivery must sign with: the current
// one always, plus the rotated-out one while its grace window is open.
// Receivers verify against a space-delimited signature list, so a receiver
// holding EITHER secret validates during the rollover.
func signingSecrets(ep sqlc.WebhookEndpoint, now time.Time) []string {
	secrets := []string{ep.Secret}
	if ep.SecretPrevious != "" && ep.SecretRotatedAt.Valid &&
		now.Sub(ep.SecretRotatedAt.Time) < WebhookRotationGrace {
		secrets = append(secrets, ep.SecretPrevious)
	}
	return secrets
}

// postWebhook signs and POSTs, returning the HTTP status (0 when the request
// never reached the server). 2xx → nil error. Multiple secrets produce a
// space-delimited signature list (standard-webhooks §signature).
//
// msgID is the DELIVERY id, not a fresh value per attempt. standard-webhooks
// defines webhook-id as unique per message but identical when the same message
// is resent after a failure, and that stability is the whole basis of receiver
// deduplication. Generating it here from the clock gave every retry under the
// 2^n backoff a different id, so a receiver could not tell a retry from a new
// event - while this same product relies on exactly that contract for its own
// inbound traffic, keying `webhook_events` on the incoming message id. Telling
// customers to deduplicate and then defeating it is the worst of both.
//
// The 5-minute visibility lease makes it matter more, not less: a duplicated
// claim produces two POSTs of one delivery, and a stable id is what lets the
// receiver collapse them.
func (w *Worker) postWebhook(ctx context.Context, rawURL string, msgID string, secrets []string, payload []byte) (int32, error) {
	if err := w.WebhookGuard(ctx, rawURL); err != nil {
		return 0, err
	}
	ts := time.Now()
	sigs := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		wh, err := standardwebhooks.NewWebhookRaw([]byte(secret))
		if err != nil {
			return 0, err
		}
		sig, err := wh.Sign(msgID, ts, payload)
		if err != nil {
			return 0, err
		}
		sigs = append(sigs, sig)
	}
	sig := strings.Join(sigs, " ")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", msgID)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set("webhook-signature", sig)

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: w.WebhookTransport,
		// A delivery is a signed POST to one declared endpoint, not a fetch.
		// Following a redirect would let a customer endpoint send that signed
		// payload somewhere the guard never classified — including back to
		// http://, because Go's default policy carries the request across
		// schemes. There is no legitimate reason for a webhook receiver to
		// redirect, so this refuses rather than re-validating a hop.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("webhook endpoint redirected to %s; delivery targets must not redirect", req.URL.Redacted())
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return int32(resp.StatusCode), nil
	}
	return int32(resp.StatusCode), fmt.Errorf("webhook endpoint returned %d", resp.StatusCode)
}

// --- SSRF guard ---

// guardWebhookURL enforces the delivery policy BEFORE any bytes move:
// https only, resolvable host, and no private/loopback/link-local/unspecified
// addresses. Local testing uses a tunnel (or the disabled seeded example).
func guardWebhookURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	if u.Scheme != "https" {
		return errors.New("webhook URL must be https")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("webhook URL has no host")
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("webhook host does not resolve: %s", host)
	}
	for _, a := range addrs {
		if !isPublicIP(a) {
			return fmt.Errorf("webhook host resolves to a private address: %s", host)
		}
	}
	return nil
}

// isPublicIP is an allow-list: an address must be global unicast to pass, and
// then must not fall inside a range that is globally routable on paper but
// internal in practice. Framing it this way rather than as a deny-list is the
// point — a new address family or a range nobody enumerated fails closed.
func isPublicIP(a netip.Addr) bool {
	a = a.Unmap() // ::ffff:0.0.0.0 must classify like 0.0.0.0
	// IsGlobalUnicast already excludes loopback, link-local, multicast, and the
	// unspecified address, in both families.
	if !a.IsGlobalUnicast() {
		return false
	}
	if a.IsPrivate() || a.IsInterfaceLocalMulticast() || a.IsLinkLocalMulticast() {
		return false
	}
	for _, block := range nonRoutableBlocks {
		if block.Contains(a) {
			return false
		}
	}
	return true
}

// nonRoutableBlocks are global-unicast ranges that must never be a delivery
// target. IsPrivate covers RFC 1918 and fc00::/7 and nothing else, so these are
// the gaps that matter for a hosted deployment.
var nonRoutableBlocks = []netip.Prefix{
	// RFC 6598 shared address space. Routable-looking and internal in practice:
	// it is what EKS/GKE hand pod networks and what a Tailscale host answers on,
	// so a hosted deployment reaches its own cluster through it.
	netip.MustParsePrefix("100.64.0.0/10"),
	// RFC 6890 "this host on this network" — 0.0.0.0/8 beyond the unspecified
	// address IsGlobalUnicast already rejects.
	netip.MustParsePrefix("0.0.0.0/8"),
	// RFC 5737 documentation ranges and RFC 6890 benchmarking.
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	// RFC 1112 reserved former class E.
	netip.MustParsePrefix("240.0.0.0/4"),
	// IPv6: unspecified/loopback block, documentation, and 6to4/Teredo, which
	// embed an IPv4 address the guard would otherwise never see.
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2002::/16"),
}

// guardedTransport dials only approved IPs — a DNS rebind between the guard
// and the dial cannot smuggle a private address through.
func guardedTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("webhook dial: host %s does not resolve", host)
		}
		for _, ip := range ips {
			if !isPublicIP(ip) {
				return nil, fmt.Errorf("webhook dial: %s is not a public address", host)
			}
		}
		d := net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return t
}

// webhookMessageID derives the standard-webhooks message id from the delivery
// row's primary key. Deriving rather than storing keeps it stable for free: the
// row already exists before the first attempt and its id never changes, so every
// retry recomputes the same value with nothing to persist and nothing to migrate.
func webhookMessageID(deliveryID int64) string {
	return "msg_" + strconv.FormatInt(deliveryID, 36)
}
