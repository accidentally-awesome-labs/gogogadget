package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// polarServers maps the POLAR_SERVER config value to a base URL. Values pinned
// from the Polar API (2026-04) OpenAPI servers block; docs win over guesses.
var polarServers = map[string]string{
	"production": "https://api.polar.sh",
	"sandbox":    "https://sandbox-api.polar.sh",
}

const polarAPIVersion = "2026-04"

// PolarClient implements Client over the Polar REST API with plain net/http
// (the former polarsource/polar-go SDK is archived upstream; raw HTTP is the
// recommended migration). Every request/response shape stays confined to this
// file.
type PolarClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewPolarClient(accessToken, server string) *PolarClient {
	base, ok := polarServers[server]
	if !ok {
		base = polarServers["sandbox"]
	}
	return &PolarClient{
		baseURL: base,
		token:   accessToken,
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *PolarClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("polar: encode request: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Polar-Version", polarAPIVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("polar: %s %s: %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *PolarClient) CreateCheckout(ctx context.Context, p CheckoutParams) (string, error) {
	body := map[string]any{
		"products": []string{p.ProductID},
	}
	if p.SuccessURL != "" {
		body["success_url"] = p.SuccessURL
	}
	if p.CustomerExternalID != "" {
		body["external_customer_id"] = p.CustomerExternalID
	}
	if len(p.Metadata) > 0 {
		body["metadata"] = p.Metadata
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/checkouts/", body, &out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", fmt.Errorf("polar: checkout returned no URL")
	}
	return out.URL, nil
}

func (c *PolarClient) CreatePortalSession(ctx context.Context, customerExternalID string) (string, error) {
	body := map[string]any{"external_customer_id": customerExternalID}
	var out struct {
		CustomerPortalURL string `json:"customer_portal_url"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/customer-sessions/", body, &out); err != nil {
		return "", err
	}
	if out.CustomerPortalURL == "" {
		return "", fmt.Errorf("polar: portal session returned no URL")
	}
	return out.CustomerPortalURL, nil
}

func (c *PolarClient) RevokeSubscription(ctx context.Context, polarSubscriptionID string) error {
	// Polar 2026-04: revoke is DELETE /v1/subscriptions/{id}.
	return c.do(ctx, http.MethodDelete, "/v1/subscriptions/"+polarSubscriptionID, nil, nil)
}

func (c *PolarClient) IngestUsage(ctx context.Context, customerExternalID string, events []UsageEvent) error {
	if len(events) == 0 {
		return nil
	}
	items := make([]map[string]any, 0, len(events))
	for _, e := range events {
		md := map[string]any{}
		for k, v := range e.Metadata {
			md[k] = v
		}
		if e.Value != 1 {
			md["value"] = e.Value
		}
		item := map[string]any{
			"name":                 e.Name,
			"external_customer_id": customerExternalID,
			"metadata":             md,
		}
		if e.ExternalID != "" {
			item["external_id"] = e.ExternalID
		}
		items = append(items, item)
	}
	return c.do(ctx, http.MethodPost, "/v1/events/ingest", map[string]any{"events": items}, nil)
}
