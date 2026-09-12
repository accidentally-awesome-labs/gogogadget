// Package knock is the managed Knock notification adapter. Every send is
// written to this project's own Postgres inbox first and only then triggered
// on Knock, so the record a product transaction cares about never depends on
// a third party being reachable.
//
// The trigger runs off the caller's goroutine. The seam contract (AGENTS.md:
// notify.Send logs errors and never returns them) removes the only channel a
// caller could learn about a provider problem through, which makes added
// latency — not a returned error — the failure a managed notifier would
// inflict on a product request. Accepted triggers are drained by Stop.
package knock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/notifications"
	"github.com/jackc/pgx/v5"
)

// defaultBaseURL is Knock's API host. Pinned from the Knock API reference,
// which serves every route under https://api.knock.app/v1:
// https://docs.knock.app/api-reference/overview
const defaultBaseURL = "https://api.knock.app"

// deliverTimeout bounds one trigger. Delivery is best effort, so a provider
// that stops answering must not hold a goroutine open indefinitely.
const deliverTimeout = 10 * time.Second

// Queue is the durable half: the row the product reads back.
type Queue interface {
	Enqueue(context.Context, notifications.Message) error
}

// Members resolves the user ids an organization-wide send reaches. It feeds
// both halves — one durable row per member, and the recipient each Knock
// trigger addresses — because Knock delivers to recipients, not to tenants.
type Members func(context.Context, string) ([]string, error)

// Delivery is the network half.
type Delivery func(context.Context, notifications.Message) error

type Notifier struct {
	Queue   Queue
	Members Members
	Deliver Delivery
	Report  func(context.Context, error)

	inflight sync.WaitGroup
}

func New(q Queue, m Members, d Delivery, report func(context.Context, error)) *Notifier {
	return &Notifier{Queue: q, Members: m, Deliver: d, Report: report}
}

func (n *Notifier) send(c context.Context, m notifications.Message) error {
	if n == nil || n.Queue == nil {
		return fmt.Errorf("knock: queue is required")
	}
	if err := n.Queue.Enqueue(c, m); err != nil {
		n.report(c, err)
		// Notification delivery is best effort. The owning product
		// transaction must not fail because the outbox is temporarily down.
		// Nothing is triggered either: the durable row is the record, and a
		// notification the product cannot read back is not one to announce.
		return nil
	}
	n.dispatch(c, m)
	return nil
}

// dispatch hands one trigger to a detached goroutine. The context is derived
// with WithoutCancel because the caller's request has usually returned by the
// time the provider answers, and a delivery the seam already accepted must
// not be cancelled by that.
func (n *Notifier) dispatch(c context.Context, m notifications.Message) {
	if n.Deliver == nil || m.UserID == "" {
		// With no recipient there is nothing Knock can address. The durable
		// row is already written, which is why this is not an error.
		return
	}
	n.inflight.Add(1)
	go func() {
		defer n.inflight.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c), deliverTimeout)
		defer cancel()
		if err := n.Deliver(ctx, m); err != nil {
			n.report(ctx, err)
		}
	}()
}

func (n *Notifier) report(c context.Context, err error) {
	if n == nil || n.Report == nil || err == nil {
		return
	}
	n.Report(c, err)
}

func (n *Notifier) Send(c context.Context, o, u, k, t, b, url string) error {
	return n.send(c, notifications.Message{OrgID: o, UserID: u, Kind: k, Title: t, Body: b, URL: url})
}

// SendOrg fans out over the organization's members, which is the same policy
// the Postgres notifications adapter applies: one durable row per member.
// Selecting Knock must not change what the in-app inbox contains.
func (n *Notifier) SendOrg(c context.Context, o, k, t, b, url string) error {
	if n == nil || n.Queue == nil {
		return fmt.Errorf("knock: queue is required")
	}
	m := notifications.Message{OrgID: o, Kind: k, Title: t, Body: b, URL: url}
	if n.Members == nil {
		return n.send(c, m)
	}
	members, err := n.Members(c, o)
	if err != nil {
		n.report(c, err)
		// The membership read drives the fanout, not the record: write the
		// organization-scoped row so the notification is not lost with it.
		return n.send(c, m)
	}
	for _, member := range members {
		m.UserID = member
		if err := n.send(c, m); err != nil {
			return err
		}
	}
	return nil
}

var _ notifications.Notifier = (*Notifier)(nil)
var _ apphost.Lifecycle = (*Notifier)(nil)
var _ apphost.HealthChecker = (*Notifier)(nil)

// Stop waits for the triggers this notifier accepted. It is idempotent and
// returns ctx.Err() when the caller's deadline expires first.
func (n *Notifier) Stop(ctx context.Context) error {
	if n == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		n.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (n *Notifier) Health(ctx context.Context) error {
	if n == nil || n.Queue == nil {
		return fmt.Errorf("knock: queue is required")
	}
	// A managed adapter with no trigger is the local adapter wearing a
	// provider's name; that state is unhealthy, not merely quiet.
	if n.Deliver == nil {
		return fmt.Errorf("knock: workflow trigger is required")
	}
	if checker, ok := n.Queue.(apphost.HealthChecker); ok {
		return checker.Health(ctx)
	}
	return ctx.Err()
}

// Client triggers Knock workflows over the Knock REST API with plain
// net/http. Provider clients are written this way throughout the repository
// (see internal/billing/polar) rather than taking an SDK dependency.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewClient(apiKey, baseURL string) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("knock: KNOCK_API_KEY is required")
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  strings.TrimSpace(apiKey),
		http:    &http.Client{Timeout: deliverTimeout},
	}, nil
}

// Trigger runs the workflow named by the message kind for one recipient.
//
// Shape pinned from the Knock API reference: POST
// /v1/workflows/{key}/trigger carrying {recipients,tenant,data} and
// answering {workflow_run_id}
// (https://docs.knock.app/api-reference/workflows/trigger). The API key
// travels as a bearer token
// (https://docs.knock.app/api-reference/overview/authentication).
func (c *Client) Trigger(ctx context.Context, m notifications.Message) error {
	if c == nil {
		return fmt.Errorf("knock: client is required")
	}
	if m.Kind == "" {
		return fmt.Errorf("knock: message kind names the workflow and is required")
	}
	if m.UserID == "" {
		return fmt.Errorf("knock: message recipient is required")
	}
	body := map[string]any{
		"recipients": []string{m.UserID},
		"data": map[string]any{
			"kind":  m.Kind,
			"title": m.Title,
			"body":  m.Body,
			"url":   m.URL,
		},
	}
	if m.OrgID != "" {
		body["tenant"] = m.OrgID
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("knock: encode request: %w", err)
	}
	path := "/v1/workflows/" + url.PathEscape(m.Kind) + "/trigger"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("knock: POST %s: %d: %s", path, resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	var out struct {
		WorkflowRunID string `json:"workflow_run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("knock: decode response: %w", err)
	}
	if out.WorkflowRunID == "" {
		return fmt.Errorf("knock: trigger returned no workflow run id")
	}
	return nil
}

// Queries is the slice of the generated query set the durable half writes
// through. It is an interface so that half is testable without a database,
// and it is narrow so the manifest's database.queries need stays honest.
type Queries interface {
	GetNotificationPreference(context.Context, sqlc.GetNotificationPreferenceParams) (sqlc.NotificationPreference, error)
	InsertNotification(context.Context, sqlc.InsertNotificationParams) (sqlc.Notification, error)
	ListMembersByOrg(context.Context, string) ([]sqlc.ListMembersByOrgRow, error)
}

// The manifest declares this module's database.queries need as *sqlc.Queries,
// so the generated boot assigns exactly that into Deps.Queries.
var _ Queries = (*sqlc.Queries)(nil)

// queryQueue is the durable path, and it applies exactly the policy
// notifications-postgres applies: a user who turned the in-app channel off
// gets no row. That preference is scoped to the in-app channel by its own
// column name, so it suppresses the row and not the Knock trigger — the
// channels Knock delivers have their own preferences, in Knock.
type queryQueue struct{ q Queries }

func (q queryQueue) Enqueue(ctx context.Context, m notifications.Message) error {
	if q.q == nil {
		return fmt.Errorf("knock: queries are required")
	}
	if m.UserID != "" {
		pref, err := q.q.GetNotificationPreference(ctx, sqlc.GetNotificationPreferenceParams{UserID: m.UserID, Kind: m.Kind})
		if err == nil && !pref.InApp {
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	_, err := q.q.InsertNotification(ctx, sqlc.InsertNotificationParams{
		OrgID: m.OrgID, UserID: m.UserID, Kind: m.Kind, Title: m.Title, Body: m.Body, Url: m.URL,
	})
	return err
}

func orgMembers(q Queries) Members {
	return func(ctx context.Context, orgID string) ([]string, error) {
		rows, err := q.ListMembersByOrg(ctx, orgID)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.UserID)
		}
		return ids, nil
	}
}

type Deps struct {
	Queries Queries
	// APIKey and BaseURL come from this adapter's own declared environment
	// when unset, the way llm-openai-compatible and search-typesense read
	// theirs. The generated boot passes neither.
	APIKey, BaseURL string
}

type Module struct{ Value *Notifier }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Queries == nil {
		return nil, fmt.Errorf("knock: database queries are required")
	}
	if h != nil {
		if d.APIKey == "" {
			d.APIKey = h.Env("KNOCK_API_KEY")
		}
		if d.BaseURL == "" {
			d.BaseURL = h.Env("KNOCK_URL")
		}
	}
	// Selecting this adapter is an explicit decision to notify through Knock.
	// A missing key refuses the boot rather than degrading to the local
	// adapter under a provider's name.
	client, err := NewClient(d.APIKey, d.BaseURL)
	if err != nil {
		return nil, err
	}
	return &Module{Value: New(queryQueue{q: d.Queries}, orgMembers(d.Queries), client.Trigger, hostReporter(h))}, nil
}

// hostReporter turns a delivery failure into a log line, which is all the
// seam allows: notify.Send never returns an error to its caller.
func hostReporter(h apphost.Host) func(context.Context, error) {
	if h == nil {
		return nil
	}
	log := h.Log()
	if log == nil {
		return nil
	}
	return func(ctx context.Context, err error) {
		log.ErrorContext(ctx, "knock notification delivery failed", "error", err)
	}
}

func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return nil
	}
	return m.Value.Stop(ctx)
}

func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("knock: notifier is required")
	}
	return m.Value.Health(ctx)
}
