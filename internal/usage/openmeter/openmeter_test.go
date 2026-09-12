package openmeter

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
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
)

type queueFunc func(context.Context, string, string, int64, string, map[string]any) error

func (f queueFunc) Enqueue(c context.Context, o, n string, v int64, e string, m map[string]any) error {
	return f(c, o, n, v, e, m)
}

type usageRow struct {
	orgID, name, externalID string
	value                   int64
}

// recordingQueue is the durable half under test: the row local aggregation
// and invoicing read.
type recordingQueue struct {
	mu   sync.Mutex
	rows []usageRow
	err  error
}

func (q *recordingQueue) Enqueue(_ context.Context, orgID, name string, value int64, externalID string, _ map[string]any) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	q.rows = append(q.rows, usageRow{orgID: orgID, name: name, externalID: externalID, value: value})
	return nil
}

func (q *recordingQueue) recorded() []usageRow {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]usageRow(nil), q.rows...)
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
	c.body = body
}

func (c *capturedRequest) requests() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

func (c *capturedRequest) lastBody() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.body...)
}

// newOpenMeterFake spins an httptest server that records the last request and
// replies with the given status/body. baseURL points at the fake, so no real
// traffic happens. The clock is fixed so the CloudEvent is assertable.
func newOpenMeterFake(t *testing.T, status int, body string, capture *capturedRequest) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient("om_test_key", srv.URL, fixedClock)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func fixedClock() time.Time {
	return time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC)
}

func TestIngestPostsCloudEvent(t *testing.T) {
	var capture capturedRequest
	c := newOpenMeterFake(t, http.StatusNoContent, ``, &capture)

	err := c.Ingest(context.Background(), "org_1", "api_request", 3, "ue-42", map[string]any{"route": "/hello"})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	if capture.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", capture.method)
	}
	if capture.path != "/api/v1/events" {
		t.Fatalf("path = %q", capture.path)
	}
	if capture.auth != "Bearer om_test_key" {
		t.Fatalf("authorization = %q", capture.auth)
	}
	// The ingest endpoint is content-negotiated: a plain application/json
	// body is not a CloudEvent.
	if capture.contentType != "application/cloudevents+json" {
		t.Fatalf("content-type = %q", capture.contentType)
	}

	var event struct {
		SpecVersion string         `json:"specversion"`
		Type        string         `json:"type"`
		ID          string         `json:"id"`
		Time        string         `json:"time"`
		Source      string         `json:"source"`
		Subject     string         `json:"subject"`
		Data        map[string]any `json:"data"`
	}
	if err := json.Unmarshal(capture.body, &event); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if event.SpecVersion != "1.0" {
		t.Fatalf("specversion = %q", event.SpecVersion)
	}
	if event.Type != "api_request" {
		t.Fatalf("type = %q", event.Type)
	}
	// (source, id) is OpenMeter's deduplication key, so the caller's dedup
	// hint has to be the event id for a replay to collapse.
	if event.ID != "ue-42" {
		t.Fatalf("id = %q", event.ID)
	}
	if event.Source != "gogogadget" {
		t.Fatalf("source = %q", event.Source)
	}
	if event.Subject != "org_1" {
		t.Fatalf("subject = %q", event.Subject)
	}
	if event.Time != "2026-09-12T10:30:00Z" {
		t.Fatalf("time = %q", event.Time)
	}
	if event.Data["value"] != float64(3) {
		t.Fatalf("data.value = %v, want the metered amount", event.Data["value"])
	}
	if event.Data["route"] != "/hello" {
		t.Fatalf("data.route = %v, want the caller's metadata beside the value", event.Data["route"])
	}
}

// Without a caller dedup hint the event still needs an id of its own, or
// every event of a type collides under OpenMeter's (source, id) key.
func TestIngestGeneratesAnEventIDWhenTheCallerHasNoHint(t *testing.T) {
	var capture capturedRequest
	c := newOpenMeterFake(t, http.StatusNoContent, ``, &capture)
	seen := map[string]bool{}
	for range 2 {
		if err := c.Ingest(context.Background(), "org_1", "api_request", 1, "", nil); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
		var event struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(capture.lastBody(), &event); err != nil {
			t.Fatalf("request body: %v", err)
		}
		if event.ID == "" {
			t.Fatal("event carried no id")
		}
		if seen[event.ID] {
			t.Fatalf("id %q was reused; two events would collapse into one", event.ID)
		}
		seen[event.ID] = true
	}
}

func TestIngestReportsProviderRefusal(t *testing.T) {
	var capture capturedRequest
	c := newOpenMeterFake(t, 400, `{"message":"unknown meter"}`, &capture)
	err := c.Ingest(context.Background(), "org_1", "api_request", 1, "", nil)
	if err == nil {
		t.Fatal("a 400 was accepted")
	}
	for _, want := range []string{"400", "/api/v1/events", "unknown meter"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

func TestIngestRefusesUnmeterableEvent(t *testing.T) {
	var capture capturedRequest
	c := newOpenMeterFake(t, http.StatusNoContent, ``, &capture)
	if err := c.Ingest(context.Background(), "", "api_request", 1, "", nil); err == nil {
		t.Fatal("an event with no organization was accepted; the org is the metered subject")
	}
	if err := c.Ingest(context.Background(), "org_1", "", 1, "", nil); err == nil {
		t.Fatal("an event with no name was accepted; the name matches the meter")
	}
	if capture.requests() != 0 {
		t.Fatalf("%d request(s) reached the provider", capture.requests())
	}
}

func TestEnqueueFailureIsReportedAndSwallowed(t *testing.T) {
	want := errors.New("queue down")
	var seen reports
	var capture capturedRequest
	c := newOpenMeterFake(t, http.StatusNoContent, ``, &capture)
	r := New(queueFunc(func(context.Context, string, string, int64, string, map[string]any) error { return want }), c.Ingest, seen.report)

	if err := r.Record(context.Background(), "org_1", "api_request", 1, "", nil); err != nil {
		t.Fatalf("Record returned enqueue failure: %v", err)
	}
	if err := r.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := seen.all(); len(got) != 1 || !errors.Is(got[0], want) {
		t.Fatalf("reported = %v, want %v", got, want)
	}
	// The local row is what an invoice reconciles against; reporting usage
	// that has no local record would make the two disagree by design.
	if capture.requests() != 0 {
		t.Fatalf("%d event(s) were ingested after the durable write failed", capture.requests())
	}
}

// The reds that matter most: the provider is down, the local row still lands,
// and the caller never learns about it through an error or latency.
func TestProviderFailureKeepsTheLocalRowAndSparesTheCaller(t *testing.T) {
	var capture capturedRequest
	c := newOpenMeterFake(t, 503, `service unavailable`, &capture)
	queue := &recordingQueue{}
	var seen reports
	r := New(queue, c.Ingest, seen.report)

	if err := r.Record(context.Background(), "org_1", "api_request", 7, "ue-7", nil); err != nil {
		t.Fatalf("Record returned a provider failure: %v", err)
	}
	if err := r.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	rows := queue.recorded()
	if len(rows) != 1 || rows[0].value != 7 || rows[0].externalID != "ue-7" {
		t.Fatalf("local rows = %v", rows)
	}
	if capture.requests() != 1 {
		t.Fatalf("provider requests = %d, want 1", capture.requests())
	}
	if got := seen.all(); len(got) != 1 || !strings.Contains(got[0].Error(), "503") {
		t.Fatalf("reported = %v, want one 503", got)
	}
}

// A cancelled caller must not cancel an ingest the seam already accepted.
func TestAcceptedIngestSurvivesCallerCancellation(t *testing.T) {
	var capture capturedRequest
	c := newOpenMeterFake(t, http.StatusNoContent, ``, &capture)
	r := New(&recordingQueue{}, c.Ingest, nil)

	ctx, cancel := context.WithCancel(context.Background())
	if err := r.Record(ctx, "org_1", "api_request", 1, "", nil); err != nil {
		t.Fatalf("Record: %v", err)
	}
	cancel()
	if err := r.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if capture.requests() != 1 {
		t.Fatalf("provider requests = %d, want 1", capture.requests())
	}
}

// fakeQueries is the generated query set the durable half writes through.
type fakeQueries struct {
	inserted []sqlc.InsertUsageEventParams
	err      error
}

func (f *fakeQueries) InsertUsageEvent(_ context.Context, arg sqlc.InsertUsageEventParams) (sqlc.UsageEvent, error) {
	f.inserted = append(f.inserted, arg)
	return sqlc.UsageEvent{}, f.err
}

func TestDurableQueueWritesTheUsageRow(t *testing.T) {
	q := &fakeQueries{}
	if err := (queryQueue{q: q}).Enqueue(context.Background(), "org_1", "api_request", 5, "ue-5", map[string]any{"route": "/hello"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if len(q.inserted) != 1 {
		t.Fatalf("inserted = %v", q.inserted)
	}
	row := q.inserted[0]
	if row.OrgID != "org_1" || row.Name != "api_request" || row.Value != 5 || row.ExternalID != "ue-5" {
		t.Fatalf("row = %+v", row)
	}
	if !strings.Contains(string(row.Metadata), `"/hello"`) {
		t.Fatalf("metadata = %s", row.Metadata)
	}

	broken := &fakeQueries{err: errors.New("database down")}
	if err := (queryQueue{q: broken}).Enqueue(context.Background(), "org_1", "api_request", 1, "", nil); err == nil {
		t.Fatal("an insert failure was swallowed by the queue")
	}
}

func TestNewModuleRefusesAMissingAPIKey(t *testing.T) {
	h := apphost.Map(map[string]string{"APP_ENV": "production"}, fixedClock(), "v-test")
	_, err := NewModule(context.Background(), h, Deps{Queries: &fakeQueries{}})
	if err == nil {
		t.Fatal("the managed adapter booted with no credentials")
	}
	if !strings.Contains(err.Error(), "OPENMETER_API_KEY") {
		t.Fatalf("refusal %q does not name the key it needs", err)
	}
}

func TestNewModuleRequiresQueries(t *testing.T) {
	h := apphost.Map(map[string]string{"OPENMETER_API_KEY": "om_test_key"}, fixedClock(), "v-test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("the adapter booted with no durable path")
	}
}

// End to end through the module: the declared environment reaches the client,
// the local row lands, and the event is ingested.
func TestNewModuleWiresTheLocalRowAndTheIngest(t *testing.T) {
	var capture capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	h := apphost.Map(map[string]string{"OPENMETER_API_KEY": "om_wired", "OPENMETER_URL": srv.URL}, fixedClock(), "v-test")
	queries := &fakeQueries{}
	m, err := NewModule(context.Background(), h, Deps{Queries: queries})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if err := m.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if err := m.Value.Record(context.Background(), "org_1", "api_request", 2, "ue-2", nil); err != nil {
		t.Fatalf("Record: %v", err)
	}
	ctx := stopContext(t)
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if len(queries.inserted) != 1 {
		t.Fatalf("local rows = %v", queries.inserted)
	}
	if capture.requests() != 1 || capture.auth != "Bearer om_wired" {
		t.Fatalf("provider requests = %d, authorization = %q", capture.requests(), capture.auth)
	}
	// The host clock reaches the event, so its time is not an ambient
	// time.Now the runtime cannot fix.
	if !strings.Contains(string(capture.lastBody()), "2026-09-12T10:30:00Z") {
		t.Fatalf("event = %s", capture.lastBody())
	}
}

// The defect this adapter shipped with: a nil ingest is the local adapter
// wearing a provider's name, and health must say so.
func TestHealthRefusesAMissingIngest(t *testing.T) {
	r := New(&recordingQueue{}, nil, nil)
	if err := r.Health(context.Background()); err == nil {
		t.Fatal("a recorder with no ingest reported healthy")
	}
}

func stopContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}
