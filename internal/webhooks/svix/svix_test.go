package svix

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/jackc/pgx/v5/pgconn"
)

type queueFunc func(context.Context, string, string, any) error

func (f queueFunc) Enqueue(c context.Context, o, t string, d any) error { return f(c, o, t, d) }

type outboxRow struct {
	orgID, eventType string
	data             any
}

// recordingQueue is the durable half under test: the outbox row that records
// the event.
type recordingQueue struct {
	mu   sync.Mutex
	rows []outboxRow
	err  error
}

func (q *recordingQueue) Enqueue(_ context.Context, orgID, eventType string, data any) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	q.rows = append(q.rows, outboxRow{orgID: orgID, eventType: eventType, data: data})
	return nil
}

func (q *recordingQueue) recorded() []outboxRow {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]outboxRow(nil), q.rows...)
}

type reports struct {
	mu   sync.Mutex
	errs []error
}

func (r *reports) report(_ context.Context, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

func (r *reports) all() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error(nil), r.errs...)
}

type capturedRequest struct {
	mu                              sync.Mutex
	count                           int
	path, method, auth, contentType string
	accept                          string
	body                            []byte
}

func (c *capturedRequest) record(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	c.path, c.method = r.URL.Path, r.Method
	c.auth = r.Header.Get("Authorization")
	c.contentType = r.Header.Get("Content-Type")
	c.accept = r.Header.Get("Accept")
	c.body = body
}

func (c *capturedRequest) requests() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// newSvixFake spins an httptest server that records the last request and
// replies with the given status/body. baseURL points at the fake, so no real
// traffic happens. The clock is fixed so the payload is assertable.
func newSvixFake(t *testing.T, status int, body string, capture *capturedRequest) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient("testsk_auth_token", srv.URL, fixedClock)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func fixedClock() time.Time {
	return time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC)
}

func TestSendPostsMessageCreateRequest(t *testing.T) {
	var capture capturedRequest
	c := newSvixFake(t, 202, `{"id":"msg_2abc"}`, &capture)

	err := c.Send(context.Background(), "org_1", "project.created", map[string]any{"id": "proj_1", "name": "Atlas"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if capture.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", capture.method)
	}
	if capture.path != "/api/v1/app/org_1/msg/" {
		t.Fatalf("path = %q", capture.path)
	}
	if capture.auth != "Bearer testsk_auth_token" {
		t.Fatalf("authorization = %q", capture.auth)
	}
	if capture.contentType != "application/json" || capture.accept != "application/json" {
		t.Fatalf("content-type = %q, accept = %q", capture.contentType, capture.accept)
	}

	var body struct {
		EventType string `json:"eventType"`
		Payload   struct {
			Type       string         `json:"type"`
			OccurredAt string         `json:"occurred_at"`
			Data       map[string]any `json:"data"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(capture.body, &body); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if body.EventType != "project.created" {
		t.Fatalf("eventType = %q", body.EventType)
	}
	// Svix never injects the event type into the payload, and a subscriber
	// must read the same envelope whichever adapter fanned the event out.
	if body.Payload.Type != "project.created" {
		t.Fatalf("payload.type = %q", body.Payload.Type)
	}
	if body.Payload.OccurredAt != "2026-09-12T10:30:00Z" {
		t.Fatalf("payload.occurred_at = %q", body.Payload.OccurredAt)
	}
	if body.Payload.Data["name"] != "Atlas" {
		t.Fatalf("payload.data = %v", body.Payload.Data)
	}
}

func TestSendReportsProviderRefusal(t *testing.T) {
	var capture capturedRequest
	c := newSvixFake(t, 401, `{"detail":"unauthorized"}`, &capture)
	err := c.Send(context.Background(), "org_1", "project.created", map[string]any{})
	if err == nil {
		t.Fatal("a 401 was accepted")
	}
	for _, want := range []string{"401", "/api/v1/app/org_1/msg/", "unauthorized"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

// A 2xx with no message id means nothing was created; the adapter has no
// other signal that Svix accepted the event.
func TestSendRequiresMessageID(t *testing.T) {
	var capture capturedRequest
	c := newSvixFake(t, 202, `{}`, &capture)
	if err := c.Send(context.Background(), "org_1", "project.created", map[string]any{}); err == nil {
		t.Fatal("a response with no message id was accepted")
	}
}

func TestSendRefusesUnaddressableEvent(t *testing.T) {
	var capture capturedRequest
	c := newSvixFake(t, 202, `{"id":"msg_1"}`, &capture)
	if err := c.Send(context.Background(), "", "project.created", nil); err == nil {
		t.Fatal("an event with no organization was accepted; the org names the Svix application")
	}
	if err := c.Send(context.Background(), "org_1", "", nil); err == nil {
		t.Fatal("an event with no type was accepted")
	}
	if capture.requests() != 0 {
		t.Fatalf("%d request(s) reached the provider", capture.requests())
	}
}

func TestEnqueueFailureIsReportedAndSwallowed(t *testing.T) {
	want := errors.New("outbox down")
	var seen reports
	var capture capturedRequest
	c := newSvixFake(t, 202, `{"id":"msg_1"}`, &capture)
	e := New(queueFunc(func(context.Context, string, string, any) error { return want }), c.Send, seen.report)

	if err := e.Emit(context.Background(), "org_1", "project.created", map[string]any{}); err != nil {
		t.Fatalf("Emit returned enqueue failure: %v", err)
	}
	if err := e.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := seen.all(); len(got) != 1 || !errors.Is(got[0], want) {
		t.Fatalf("reported = %v, want %v", got, want)
	}
	// The outbox row is this adapter's record of the event; fanning out an
	// event it cannot account for would leave nothing to reconcile against.
	if capture.requests() != 0 {
		t.Fatalf("%d message(s) were created after the durable write failed", capture.requests())
	}
}

// The reds that matter most: the provider is down, the outbox row still
// lands, and the caller never learns about it through an error or latency.
func TestProviderFailureKeepsTheOutboxRowAndSparesTheCaller(t *testing.T) {
	var capture capturedRequest
	c := newSvixFake(t, 500, `boom`, &capture)
	queue := &recordingQueue{}
	var seen reports
	e := New(queue, c.Send, seen.report)

	if err := e.Emit(context.Background(), "org_1", "project.created", map[string]any{"id": "proj_1"}); err != nil {
		t.Fatalf("Emit returned a provider failure: %v", err)
	}
	if err := e.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	rows := queue.recorded()
	if len(rows) != 1 || rows[0].orgID != "org_1" || rows[0].eventType != "project.created" {
		t.Fatalf("outbox rows = %v", rows)
	}
	if capture.requests() != 1 {
		t.Fatalf("provider requests = %d, want 1", capture.requests())
	}
	if got := seen.all(); len(got) != 1 || !strings.Contains(got[0].Error(), "500") {
		t.Fatalf("reported = %v, want one 500", got)
	}
}

// A cancelled caller must not cancel a message the seam already accepted.
func TestAcceptedMessageSurvivesCallerCancellation(t *testing.T) {
	var capture capturedRequest
	c := newSvixFake(t, 202, `{"id":"msg_1"}`, &capture)
	e := New(&recordingQueue{}, c.Send, nil)

	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Emit(ctx, "org_1", "project.created", map[string]any{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	cancel()
	if err := e.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if capture.requests() != 1 {
		t.Fatalf("provider requests = %d, want 1", capture.requests())
	}
}

// fakeDB is the slice of the pool the durable half writes through.
type fakeDB struct {
	sql     string
	args    []any
	execErr error
	pingErr error
}

func (f *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.sql, f.args = sql, args
	return pgconn.CommandTag{}, f.execErr
}

func (f *fakeDB) Ping(context.Context) error { return f.pingErr }

func TestDurableQueueWritesTheOutboxRow(t *testing.T) {
	db := &fakeDB{}
	q := outboxQueue{db: db}
	if err := q.Enqueue(context.Background(), "org_1", "project.created", map[string]any{"id": "proj_1"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !strings.Contains(db.sql, "INSERT INTO webhook_outbox") {
		t.Fatalf("statement = %q", db.sql)
	}
	if len(db.args) != 3 || db.args[0] != "org_1" || db.args[1] != "project.created" {
		t.Fatalf("args = %v", db.args)
	}
	if payload, ok := db.args[2].([]byte); !ok || !strings.Contains(string(payload), `"proj_1"`) {
		t.Fatalf("payload arg = %v", db.args[2])
	}
	// Health is the outbox's health: a managed emitter whose durable path is
	// gone is not ready, whatever the provider says.
	db.pingErr = errors.New("no connection")
	if err := q.Health(context.Background()); err == nil {
		t.Fatal("an unreachable database reported healthy")
	}
}

func TestNewModuleRefusesAMissingAPIKey(t *testing.T) {
	h := apphost.Map(map[string]string{"APP_ENV": "production"}, fixedClock(), "v-test")
	_, err := NewModule(context.Background(), h, Deps{Pool: &fakeDB{}})
	if err == nil {
		t.Fatal("the managed adapter booted with no credentials")
	}
	if !strings.Contains(err.Error(), "SVIX_API_KEY") {
		t.Fatalf("refusal %q does not name the key it needs", err)
	}
}

func TestNewModuleRequiresDatabase(t *testing.T) {
	h := apphost.Map(map[string]string{"SVIX_API_KEY": "testsk_auth_token"}, fixedClock(), "v-test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("the adapter booted with no durable path")
	}
}

// End to end through the module: the declared environment reaches the client,
// the outbox row lands, and the message is created.
func TestNewModuleWiresTheOutboxRowAndTheMessage(t *testing.T) {
	var capture capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		_, _ = w.Write([]byte(`{"id":"msg_1"}`))
	}))
	t.Cleanup(srv.Close)

	h := apphost.Map(map[string]string{"SVIX_API_KEY": "testsk_wired", "SVIX_URL": srv.URL}, fixedClock(), "v-test")
	db := &fakeDB{}
	m, err := NewModule(context.Background(), h, Deps{Pool: db})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if err := m.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if err := m.Value.Emit(context.Background(), "org_1", "project.created", map[string]any{"id": "proj_1"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	ctx := stopContext(t)
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if !strings.Contains(db.sql, "webhook_outbox") {
		t.Fatalf("durable statement = %q", db.sql)
	}
	if capture.requests() != 1 || capture.auth != "Bearer testsk_wired" {
		t.Fatalf("provider requests = %d, authorization = %q", capture.requests(), capture.auth)
	}
	// The host clock reaches the payload, so the envelope is not built from
	// an ambient time.Now the runtime cannot fix.
	if !strings.Contains(string(capture.body), "2026-09-12T10:30:00Z") {
		t.Fatalf("payload = %s", capture.body)
	}
}

// The defect this adapter shipped with: a nil delivery is the local adapter
// wearing a provider's name, and health must say so.
func TestHealthRefusesAMissingDelivery(t *testing.T) {
	e := New(&recordingQueue{}, nil, nil)
	if err := e.Health(context.Background()); err == nil {
		t.Fatal("an emitter with no delivery reported healthy")
	}
}

func stopContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}
