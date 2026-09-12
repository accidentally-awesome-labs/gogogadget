// Package openmeter is the managed OpenMeter usage adapter. Every event is
// written to this project's own usage_events table first and only then
// ingested by OpenMeter, so the row entitlement checks and invoices read back
// never depends on a third party being reachable.
//
// Ingest runs off the caller's goroutine. usage.Record logs errors and never
// returns them (AGENTS.md), and it is called on request paths that meter per
// call — so the harm a managed recorder could still do is latency on every
// one of them, not a returned error. Accepted events are drained by Stop.
package openmeter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/usage"
)

// defaultBaseURL is OpenMeter Cloud's host; a self-hosted deployment is
// selected through OPENMETER_URL: https://openmeter.io/docs/api
const defaultBaseURL = "https://openmeter.cloud"

// eventSource is the CloudEvents `source` every event carries. OpenMeter
// deduplicates on (source, id), so this value is part of that key and is
// fixed rather than derived:
// https://openmeter.io/docs/metering/events/usage-events
const eventSource = "gogogadget"

// deliverTimeout bounds one ingest. Delivery is best effort, so a provider
// that stops answering must not hold a goroutine open indefinitely.
const deliverTimeout = 10 * time.Second

// Queue is the durable half: the row local aggregation reads.
type Queue interface {
	Enqueue(context.Context, string, string, int64, string, map[string]any) error
}

// Delivery is the network half.
type Delivery func(context.Context, string, string, int64, string, map[string]any) error

type Recorder struct {
	Queue   Queue
	Deliver Delivery
	Report  func(context.Context, error)

	inflight sync.WaitGroup
}

func New(q Queue, d Delivery, report func(context.Context, error)) *Recorder {
	return &Recorder{Queue: q, Deliver: d, Report: report}
}

func (r *Recorder) Record(c context.Context, o, n string, v int64, e string, m map[string]any) error {
	if r == nil || r.Queue == nil {
		return fmt.Errorf("openmeter: queue is required")
	}
	if err := r.Queue.Enqueue(c, o, n, v, e, m); err != nil {
		r.report(c, err)
		// Usage is recorded by the product transaction; forwarding is
		// eventually consistent and must not fail that transaction. Nothing
		// is ingested either: the local row is what the invoice reconciles
		// against, and usage it cannot account for is not usage to report.
		return nil
	}
	r.dispatch(c, o, n, v, e, m)
	return nil
}

// dispatch hands one ingest to a detached goroutine. The context is derived
// with WithoutCancel because the metered request has usually returned by the
// time OpenMeter answers, and an event the seam already accepted must not be
// cancelled by that.
func (r *Recorder) dispatch(c context.Context, o, n string, v int64, e string, m map[string]any) {
	if r.Deliver == nil {
		return
	}
	r.inflight.Add(1)
	go func() {
		defer r.inflight.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c), deliverTimeout)
		defer cancel()
		if err := r.Deliver(ctx, o, n, v, e, m); err != nil {
			r.report(ctx, err)
		}
	}()
}

func (r *Recorder) report(c context.Context, err error) {
	if r == nil || r.Report == nil || err == nil {
		return
	}
	r.Report(c, err)
}

var _ usage.Recorder = (*Recorder)(nil)
var _ apphost.Lifecycle = (*Recorder)(nil)
var _ apphost.HealthChecker = (*Recorder)(nil)

// Stop waits for the events this recorder accepted. It is idempotent and
// returns ctx.Err() when the caller's deadline expires first.
func (r *Recorder) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		r.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Recorder) Health(ctx context.Context) error {
	if r == nil || r.Queue == nil {
		return fmt.Errorf("openmeter: queue is required")
	}
	// A managed adapter with no delivery is the local adapter wearing a
	// provider's name; that state is unhealthy, not merely quiet.
	if r.Deliver == nil {
		return fmt.Errorf("openmeter: event ingest is required")
	}
	if checker, ok := r.Queue.(apphost.HealthChecker); ok {
		return checker.Health(ctx)
	}
	return ctx.Err()
}

// Client ingests usage events over the OpenMeter REST API with plain
// net/http. Provider clients are written this way throughout the repository
// (see internal/billing/polar) rather than taking an SDK dependency.
type Client struct {
	baseURL string
	apiKey  string
	now     func() time.Time
	http    *http.Client
}

func NewClient(apiKey, baseURL string, now func() time.Time) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("openmeter: OPENMETER_API_KEY is required")
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if now == nil {
		now = time.Now
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  strings.TrimSpace(apiKey),
		now:     now,
		http:    &http.Client{Timeout: deliverTimeout},
	}, nil
}

// Ingest sends one usage event as a CloudEvent.
//
// Shape pinned from the OpenMeter docs: POST /api/v1/events with
// Content-Type: application/cloudevents+json and a CloudEvents 1.0 JSON body
// of {specversion,type,id,time,source,subject,data}
// (https://openmeter.io/docs/metering/events/overview,
// https://openmeter.io/docs/metering/events/usage-events). The API key
// travels as a bearer token (https://openmeter.io/docs/api).
//
// A meter matches on `type` and reads its value out of `data` by JSONPath, so
// the metered amount is carried as data.value and the caller's metadata
// travels beside it. The subject is the org id, which is the customer
// OpenMeter aggregates by.
func (c *Client) Ingest(ctx context.Context, orgID, name string, value int64, externalID string, metadata map[string]any) error {
	if c == nil {
		return fmt.Errorf("openmeter: client is required")
	}
	if orgID == "" {
		return fmt.Errorf("openmeter: organization id names the subject and is required")
	}
	if name == "" {
		return fmt.Errorf("openmeter: event name is required")
	}
	data := make(map[string]any, len(metadata)+1)
	for key, item := range metadata {
		data[key] = item
	}
	data["value"] = value
	id := externalID
	if id == "" {
		// (source, id) is OpenMeter's deduplication key, so an event with no
		// caller-supplied dedup hint gets an identifier of its own rather
		// than colliding with every other event of its type.
		id = randomEventID()
	}
	body, err := json.Marshal(map[string]any{
		"specversion": "1.0",
		"type":        name,
		"id":          id,
		"time":        c.now().UTC().Format(time.RFC3339),
		"source":      eventSource,
		"subject":     orgID,
		"data":        data,
	})
	if err != nil {
		return fmt.Errorf("openmeter: encode request: %w", err)
	}
	const path = "/api/v1/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/cloudevents+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// The ingest endpoint answers 204 with no body, so the status is the
	// whole result; there is nothing to decode.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("openmeter: POST %s: %d: %s", path, resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

func randomEventID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic("crypto/rand: " + err.Error()) // never happens on supported platforms
	}
	return hex.EncodeToString(raw)
}

// Queries is the slice of the generated query set the durable half writes
// through, matching the Postgres usage adapter so both record the event in
// the same table.
type Queries interface {
	InsertUsageEvent(context.Context, sqlc.InsertUsageEventParams) (sqlc.UsageEvent, error)
}

// The manifest declares this module's database.queries need as *sqlc.Queries,
// so the generated boot assigns exactly that into Deps.Queries.
var _ Queries = (*sqlc.Queries)(nil)

type queryQueue struct{ q Queries }

func (q queryQueue) Enqueue(ctx context.Context, orgID, name string, value int64, externalID string, metadata map[string]any) error {
	if q.q == nil {
		return fmt.Errorf("openmeter: queries are required")
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = q.q.InsertUsageEvent(ctx, sqlc.InsertUsageEventParams{
		OrgID: orgID, Name: name, Value: value, ExternalID: externalID, Metadata: raw,
	})
	return err
}

type Deps struct {
	Queries Queries
	// APIKey and BaseURL come from this adapter's own declared environment
	// when unset, the way llm-openai-compatible and search-typesense read
	// theirs. The generated boot passes neither.
	APIKey, BaseURL string
}

type Module struct{ Value *Recorder }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Queries == nil {
		return nil, fmt.Errorf("openmeter: database queries are required")
	}
	var now func() time.Time
	if h != nil {
		now = h.Now
		if d.APIKey == "" {
			d.APIKey = h.Env("OPENMETER_API_KEY")
		}
		if d.BaseURL == "" {
			d.BaseURL = h.Env("OPENMETER_URL")
		}
	}
	// Selecting this adapter is an explicit decision to meter through
	// OpenMeter. A missing key refuses the boot rather than degrading to the
	// local adapter under a provider's name.
	client, err := NewClient(d.APIKey, d.BaseURL, now)
	if err != nil {
		return nil, err
	}
	return &Module{Value: New(queryQueue{q: d.Queries}, client.Ingest, hostReporter(h))}, nil
}

// hostReporter turns a delivery failure into a log line, which is all the
// seam allows: usage.Record never returns an error to its caller.
func hostReporter(h apphost.Host) func(context.Context, error) {
	if h == nil {
		return nil
	}
	log := h.Log()
	if log == nil {
		return nil
	}
	return func(ctx context.Context, err error) {
		log.ErrorContext(ctx, "openmeter usage ingest failed", "error", err)
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
		return fmt.Errorf("openmeter: recorder is required")
	}
	return m.Value.Health(ctx)
}
