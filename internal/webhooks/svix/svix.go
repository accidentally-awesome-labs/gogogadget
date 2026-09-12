// Package svix is the managed Svix outbound-webhook adapter. Every event is
// written to this project's own webhook_outbox first and only then created as
// a Svix message, so the product mutation's record of the event never depends
// on a third party being reachable.
//
// The message create runs off the caller's goroutine. webhooks.Emit logs
// errors and never returns them (AGENTS.md), so the only harm a managed
// emitter could still do to a product request is hold it open while a
// provider decides whether to answer. Accepted messages are drained by Stop.
package svix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/webhooks"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultBaseURL is Svix's API host. Svix also publishes regional hosts
// (api.us.svix.com, api.eu.svix.com) which are selected through SVIX_URL:
// https://docs.svix.com/quickstart
const defaultBaseURL = "https://api.svix.com"

// deliverTimeout bounds one message create. Delivery is best effort, so a
// provider that stops answering must not hold a goroutine open indefinitely.
const deliverTimeout = 10 * time.Second

// Queue is the durable half: the outbox row that records the event.
type Queue interface {
	Enqueue(context.Context, string, string, any) error
}

// Delivery is the network half.
type Delivery func(context.Context, string, string, any) error

type Emitter struct {
	Queue   Queue
	Deliver Delivery
	Report  func(context.Context, error)

	inflight sync.WaitGroup
}

func New(q Queue, d Delivery, report func(context.Context, error)) *Emitter {
	return &Emitter{Queue: q, Deliver: d, Report: report}
}

func (e *Emitter) Emit(c context.Context, o, t string, d any) error {
	if e == nil || e.Queue == nil {
		return fmt.Errorf("svix: queue is required")
	}
	if err := e.Queue.Enqueue(c, o, t, d); err != nil {
		e.report(c, err)
		// The product mutation is already durable; delivery enqueue is
		// observable best effort and must not roll the mutation back. Nothing
		// is sent to Svix either: the outbox row is this adapter's record of
		// the event, and an event it cannot account for is not one to fan out.
		return nil
	}
	e.dispatch(c, o, t, d)
	return nil
}

// dispatch hands one message to a detached goroutine. The context is derived
// with WithoutCancel because the request that emitted the event has usually
// returned by the time Svix answers, and a message the seam already accepted
// must not be cancelled by that.
func (e *Emitter) dispatch(c context.Context, o, t string, d any) {
	if e.Deliver == nil {
		return
	}
	e.inflight.Add(1)
	go func() {
		defer e.inflight.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c), deliverTimeout)
		defer cancel()
		if err := e.Deliver(ctx, o, t, d); err != nil {
			e.report(ctx, err)
		}
	}()
}

func (e *Emitter) report(c context.Context, err error) {
	if e == nil || e.Report == nil || err == nil {
		return
	}
	e.Report(c, err)
}

var _ webhooks.Emitter = (*Emitter)(nil)
var _ apphost.Lifecycle = (*Emitter)(nil)
var _ apphost.HealthChecker = (*Emitter)(nil)

// Stop waits for the messages this emitter accepted. It is idempotent and
// returns ctx.Err() when the caller's deadline expires first.
func (e *Emitter) Stop(ctx context.Context) error {
	if e == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		e.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Emitter) Health(ctx context.Context) error {
	if e == nil || e.Queue == nil {
		return fmt.Errorf("svix: queue is required")
	}
	// A managed adapter with no delivery is the local adapter wearing a
	// provider's name; that state is unhealthy, not merely quiet.
	if e.Deliver == nil {
		return fmt.Errorf("svix: message delivery is required")
	}
	if checker, ok := e.Queue.(apphost.HealthChecker); ok {
		return checker.Health(ctx)
	}
	return ctx.Err()
}

// Client creates Svix messages over the Svix REST API with plain net/http.
// Provider clients are written this way throughout the repository (see
// internal/billing/polar) rather than taking an SDK dependency.
type Client struct {
	baseURL string
	token   string
	now     func() time.Time
	http    *http.Client
}

func NewClient(token, baseURL string, now func() time.Time) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("svix: SVIX_API_KEY is required")
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if now == nil {
		now = time.Now
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   strings.TrimSpace(token),
		now:     now,
		http:    &http.Client{Timeout: deliverTimeout},
	}, nil
}

// payload is the body a subscriber receives. It is webhooks.Envelope's shape
// on purpose: which adapter fanned the event out must not change what a
// customer's endpoint parses. Svix never injects the event type into the
// payload (https://docs.svix.com/quickstart), so `type` is carried here.
type payload struct {
	Type       string          `json:"type"`
	OccurredAt string          `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

// Send creates one Svix message for the organization's consumer application.
//
// Shape pinned from the Svix quickstart: POST
// /api/v1/app/{app_id}/msg/ carrying {eventType,payload} under a bearer
// token, answering a message object with an id
// (https://docs.svix.com/quickstart). The application id is the org id: Svix
// accepts a caller-defined uid wherever it accepts its own ids, which is what
// keeps Svix identifiers out of this project's tables.
func (c *Client) Send(ctx context.Context, orgID, eventType string, data any) error {
	if c == nil {
		return fmt.Errorf("svix: client is required")
	}
	if orgID == "" {
		return fmt.Errorf("svix: organization id names the application and is required")
	}
	if eventType == "" {
		return fmt.Errorf("svix: event type is required")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("svix: encode event data: %w", err)
	}
	body, err := json.Marshal(map[string]any{
		"eventType": eventType,
		"payload": payload{
			Type:       eventType,
			OccurredAt: c.now().UTC().Format(time.RFC3339),
			Data:       raw,
		},
	})
	if err != nil {
		return fmt.Errorf("svix: encode request: %w", err)
	}
	path := "/api/v1/app/" + url.PathEscape(orgID) + "/msg/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("svix: POST %s: %d: %s", path, resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("svix: decode response: %w", err)
	}
	if out.ID == "" {
		return fmt.Errorf("svix: message create returned no id")
	}
	return nil
}

// DB is the slice of the pool the durable half writes through, matching the
// Postgres webhooks adapter so both record the event in the same table.
type DB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Ping(context.Context) error
}

// The manifest declares this module's database.pool need as *pgxpool.Pool, so
// the generated boot assigns exactly that into Deps.Pool.
var _ DB = (*pgxpool.Pool)(nil)

type outboxQueue struct{ db DB }

func (o outboxQueue) Enqueue(ctx context.Context, orgID, eventType string, data any) error {
	if o.db == nil {
		return fmt.Errorf("svix: database is required")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = o.db.Exec(ctx, `INSERT INTO webhook_outbox (org_id,event_type,payload) VALUES ($1,$2,$3)`, orgID, eventType, raw)
	return err
}

func (o outboxQueue) Health(ctx context.Context) error {
	if o.db == nil {
		return fmt.Errorf("svix: database is required")
	}
	return o.db.Ping(ctx)
}

type Deps struct {
	Pool DB
	// APIKey and BaseURL come from this adapter's own declared environment
	// when unset, the way llm-openai-compatible and search-typesense read
	// theirs. The generated boot passes neither.
	APIKey, BaseURL string
}

type Module struct{ Value *Emitter }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Pool == nil {
		return nil, fmt.Errorf("svix: database is required")
	}
	var now func() time.Time
	if h != nil {
		now = h.Now
		if d.APIKey == "" {
			d.APIKey = h.Env("SVIX_API_KEY")
		}
		if d.BaseURL == "" {
			d.BaseURL = h.Env("SVIX_URL")
		}
	}
	// Selecting this adapter is an explicit decision to fan out through Svix.
	// A missing token refuses the boot rather than degrading to the local
	// adapter under a provider's name.
	client, err := NewClient(d.APIKey, d.BaseURL, now)
	if err != nil {
		return nil, err
	}
	return &Module{Value: New(outboxQueue{db: d.Pool}, client.Send, hostReporter(h))}, nil
}

// hostReporter turns a delivery failure into a log line, which is all the
// seam allows: webhooks.Emit never returns an error to its caller.
func hostReporter(h apphost.Host) func(context.Context, error) {
	if h == nil {
		return nil
	}
	log := h.Log()
	if log == nil {
		return nil
	}
	return func(ctx context.Context, err error) {
		log.ErrorContext(ctx, "svix webhook delivery failed", "error", err)
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
		return fmt.Errorf("svix: emitter is required")
	}
	return m.Value.Health(ctx)
}
