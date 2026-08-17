package jobs

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/jackc/pgx/v5/pgtype"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

// WebhookDeliverPayload is the enqueue contract for webhook.deliver.
type WebhookDeliverPayload struct {
	DeliveryID int64 `json:"delivery_id"`
}

// deliverWebhook POSTs the stored payload to the endpoint URL, signed with
// the standard-webhooks scheme (webhook-id/-timestamp/-signature), then
// records the outcome: 2xx → success; anything else → error (existing 2^n
// backoff), and at max attempts → dead + the endpoint owner is notified.
func (w *Worker) deliverWebhook(ctx context.Context, job sqlc.Job) error {
	var p WebhookDeliverPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return err
	}
	d, err := w.q.GetWebhookDelivery(ctx, p.DeliveryID)
	if err != nil {
		return err
	}
	ep, err := w.q.GetWebhookEndpoint(ctx, sqlc.GetWebhookEndpointParams{ID: d.EndpointID, ClerkOrgID: d.ClerkOrgID})
	if err != nil {
		return err
	}
	if ep.DisabledAt.Valid {
		return w.q.MarkDeliveryDead(ctx, sqlc.MarkDeliveryDeadParams{ID: d.ID, LastError: "endpoint disabled"})
	}

	status, attemptErr := w.postWebhook(ctx, ep.Url, ep.Secret, d.Payload)
	pgStatus := pgtype.Int4{Int32: status, Valid: status > 0}
	if attemptErr == nil {
		return w.q.MarkDeliverySuccess(ctx, sqlc.MarkDeliverySuccessParams{ID: d.ID, LastResponseStatus: pgStatus})
	}
	// Record the attempt; the caller (ProcessOne) applies backoff/dead-letter.
	_ = w.q.RecordDeliveryAttempt(ctx, sqlc.RecordDeliveryAttemptParams{
		ID: d.ID, LastResponseStatus: pgStatus, LastError: attemptErr.Error(),
	})
	if job.Attempts+1 >= job.MaxAttempts {
		if err := w.q.MarkDeliveryDead(ctx, sqlc.MarkDeliveryDeadParams{ID: d.ID, LastError: attemptErr.Error()}); err != nil {
			return err
		}
		notify.Send(ctx, w.q, d.ClerkOrgID, ep.CreatedBy, "webhook.failed",
			"Webhook delivery failed permanently", ep.Url+" — "+attemptErr.Error(), "/app/settings/webhooks")
	}
	return attemptErr
}

// postWebhook signs and POSTs, returning the HTTP status (0 when the request
// never reached the server). 2xx → nil error.
func (w *Worker) postWebhook(ctx context.Context, rawURL, secret string, payload []byte) (int32, error) {
	if err := w.WebhookGuard(ctx, rawURL); err != nil {
		return 0, err
	}
	wh, err := standardwebhooks.NewWebhookRaw([]byte(secret))
	if err != nil {
		return 0, err
	}
	msgID := "msg_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	ts := time.Now()
	sig, err := wh.Sign(msgID, ts, payload)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", msgID)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set("webhook-signature", sig)

	client := &http.Client{Timeout: 10 * time.Second, Transport: w.WebhookTransport}
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

func isPublicIP(a netip.Addr) bool {
	a = a.Unmap() // ::ffff:0.0.0.0 must classify like 0.0.0.0
	return !(a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified() || a.IsMulticast())
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
