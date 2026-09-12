package knock

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
	"github.com/gogogadget/gogogadget/internal/notifications"
	"github.com/jackc/pgx/v5"
)

type queueFunc func(context.Context, notifications.Message) error

func (f queueFunc) Enqueue(c context.Context, m notifications.Message) error { return f(c, m) }

// recordingQueue is the durable half under test: what the product reads back.
type recordingQueue struct {
	mu       sync.Mutex
	messages []notifications.Message
	err      error
}

func (q *recordingQueue) Enqueue(_ context.Context, m notifications.Message) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.err != nil {
		return q.err
	}
	q.messages = append(q.messages, m)
	return nil
}

func (q *recordingQueue) recorded() []notifications.Message {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]notifications.Message(nil), q.messages...)
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

// newKnockFake spins an httptest server that records the last request and
// replies with the given status/body. baseURL points at the fake, so no real
// traffic happens.
func newKnockFake(t *testing.T, status int, body string, capture *capturedRequest) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient("sk_test_12345", srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestTriggerPostsWorkflowRunRequest(t *testing.T) {
	var capture capturedRequest
	c := newKnockFake(t, 200, `{"workflow_run_id":"123e4567-e89b-12d3-a456-426614174000"}`, &capture)

	err := c.Trigger(context.Background(), notifications.Message{
		OrgID: "org_1", UserID: "usr_1", Kind: "project.created",
		Title: "Project created", Body: "Atlas is live", URL: "/app/projects/1",
	})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	if capture.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", capture.method)
	}
	if capture.path != "/v1/workflows/project.created/trigger" {
		t.Fatalf("path = %q", capture.path)
	}
	if capture.auth != "Bearer sk_test_12345" {
		t.Fatalf("authorization = %q", capture.auth)
	}
	if capture.contentType != "application/json" {
		t.Fatalf("content-type = %q", capture.contentType)
	}

	var body struct {
		Recipients []string          `json:"recipients"`
		Tenant     string            `json:"tenant"`
		Data       map[string]string `json:"data"`
	}
	if err := json.Unmarshal(capture.body, &body); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if len(body.Recipients) != 1 || body.Recipients[0] != "usr_1" {
		t.Fatalf("recipients = %v", body.Recipients)
	}
	if body.Tenant != "org_1" {
		t.Fatalf("tenant = %q", body.Tenant)
	}
	want := map[string]string{"kind": "project.created", "title": "Project created", "body": "Atlas is live", "url": "/app/projects/1"}
	for key, value := range want {
		if body.Data[key] != value {
			t.Fatalf("data[%s] = %q, want %q", key, body.Data[key], value)
		}
	}
}

func TestTriggerReportsProviderRefusal(t *testing.T) {
	var capture capturedRequest
	c := newKnockFake(t, 422, `{"message":"workflow not found"}`, &capture)
	err := c.Trigger(context.Background(), notifications.Message{OrgID: "org_1", UserID: "usr_1", Kind: "missing"})
	if err == nil {
		t.Fatal("a 422 was accepted")
	}
	for _, want := range []string{"422", "/v1/workflows/missing/trigger", "workflow not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

// A 200 with no run id means the request did not become a workflow run. The
// adapter has no other signal that the notification was accepted.
func TestTriggerRequiresWorkflowRunID(t *testing.T) {
	var capture capturedRequest
	c := newKnockFake(t, 200, `{}`, &capture)
	if err := c.Trigger(context.Background(), notifications.Message{OrgID: "o", UserID: "u", Kind: "k"}); err == nil {
		t.Fatal("a response with no workflow run id was accepted")
	}
}

func TestTriggerRefusesUnaddressableMessage(t *testing.T) {
	var capture capturedRequest
	c := newKnockFake(t, 200, `{"workflow_run_id":"run_1"}`, &capture)
	if err := c.Trigger(context.Background(), notifications.Message{OrgID: "o", UserID: "u"}); err == nil {
		t.Fatal("a message with no kind was accepted; the kind names the workflow")
	}
	if err := c.Trigger(context.Background(), notifications.Message{OrgID: "o", Kind: "k"}); err == nil {
		t.Fatal("a message with no recipient was accepted")
	}
	if capture.requests() != 0 {
		t.Fatalf("%d request(s) reached the provider", capture.requests())
	}
}

func TestEnqueueFailureIsReportedAndSwallowed(t *testing.T) {
	want := errors.New("queue down")
	var seen reports
	var capture capturedRequest
	c := newKnockFake(t, 200, `{"workflow_run_id":"run_1"}`, &capture)
	n := New(queueFunc(func(context.Context, notifications.Message) error { return want }), nil, c.Trigger, seen.report)

	if err := n.Send(context.Background(), "org", "user", "kind", "title", "body", "/url"); err != nil {
		t.Fatalf("Send returned enqueue failure: %v", err)
	}
	if err := n.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := seen.all(); len(got) != 1 || !errors.Is(got[0], want) {
		t.Fatalf("reported = %v, want %v", got, want)
	}
	// The durable row is the record. Announcing a notification the product
	// cannot read back would be worse than dropping it.
	if capture.requests() != 0 {
		t.Fatalf("%d trigger(s) ran after the durable write failed", capture.requests())
	}
}

// The reds that matter most: the provider is down, the row still lands, and
// the caller never learns about it through an error or through latency.
func TestProviderFailureKeepsTheDurableRowAndSparesTheCaller(t *testing.T) {
	var capture capturedRequest
	c := newKnockFake(t, 503, `service unavailable`, &capture)
	queue := &recordingQueue{}
	var seen reports
	n := New(queue, nil, c.Trigger, seen.report)

	if err := n.Send(context.Background(), "org_1", "usr_1", "project.created", "t", "b", "/u"); err != nil {
		t.Fatalf("Send returned a provider failure: %v", err)
	}
	if err := n.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if rows := queue.recorded(); len(rows) != 1 || rows[0].UserID != "usr_1" {
		t.Fatalf("durable rows = %v", rows)
	}
	if capture.requests() != 1 {
		t.Fatalf("provider requests = %d, want 1", capture.requests())
	}
	if got := seen.all(); len(got) != 1 || !strings.Contains(got[0].Error(), "503") {
		t.Fatalf("reported = %v, want one 503", got)
	}
}

// A cancelled caller must not cancel a delivery the seam already accepted.
func TestAcceptedTriggerSurvivesCallerCancellation(t *testing.T) {
	var capture capturedRequest
	c := newKnockFake(t, 200, `{"workflow_run_id":"run_1"}`, &capture)
	n := New(&recordingQueue{}, nil, c.Trigger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	if err := n.Send(ctx, "org_1", "usr_1", "kind", "t", "b", "/u"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	cancel()
	if err := n.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if capture.requests() != 1 {
		t.Fatalf("provider requests = %d, want 1", capture.requests())
	}
}

func TestSendOrgWritesAndTriggersOncePerMember(t *testing.T) {
	var capture capturedRequest
	c := newKnockFake(t, 200, `{"workflow_run_id":"run_1"}`, &capture)
	queue := &recordingQueue{}
	n := New(queue, func(context.Context, string) ([]string, error) {
		return []string{"usr_1", "usr_2"}, nil
	}, c.Trigger, nil)

	if err := n.SendOrg(context.Background(), "org_1", "kind", "t", "b", "/u"); err != nil {
		t.Fatalf("SendOrg: %v", err)
	}
	if err := n.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	rows := queue.recorded()
	if len(rows) != 2 || rows[0].UserID != "usr_1" || rows[1].UserID != "usr_2" {
		t.Fatalf("durable rows = %v", rows)
	}
	if capture.requests() != 2 {
		t.Fatalf("provider requests = %d, want 2", capture.requests())
	}
}

// A membership read that fails must not lose the notification: the
// organization-scoped row is still written, and nothing is triggered because
// Knock addresses recipients.
func TestSendOrgFallsBackToAnOrgRowWhenMembershipIsUnreadable(t *testing.T) {
	var capture capturedRequest
	c := newKnockFake(t, 200, `{"workflow_run_id":"run_1"}`, &capture)
	queue := &recordingQueue{}
	var seen reports
	n := New(queue, func(context.Context, string) ([]string, error) {
		return nil, errors.New("members unavailable")
	}, c.Trigger, seen.report)

	if err := n.SendOrg(context.Background(), "org_1", "kind", "t", "b", "/u"); err != nil {
		t.Fatalf("SendOrg: %v", err)
	}
	if err := n.Stop(stopContext(t)); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if rows := queue.recorded(); len(rows) != 1 || rows[0].UserID != "" || rows[0].OrgID != "org_1" {
		t.Fatalf("durable rows = %v", rows)
	}
	if capture.requests() != 0 {
		t.Fatalf("%d trigger(s) ran with no recipient", capture.requests())
	}
	if len(seen.all()) != 1 {
		t.Fatalf("reported = %v, want the membership failure", seen.all())
	}
}

// fakeQueries is the generated query set the durable half writes through.
type fakeQueries struct {
	preference  sqlc.NotificationPreference
	preferErr   error
	inserted    []sqlc.InsertNotificationParams
	members     []sqlc.ListMembersByOrgRow
	memberError error
}

func (f *fakeQueries) GetNotificationPreference(_ context.Context, _ sqlc.GetNotificationPreferenceParams) (sqlc.NotificationPreference, error) {
	return f.preference, f.preferErr
}

func (f *fakeQueries) InsertNotification(_ context.Context, arg sqlc.InsertNotificationParams) (sqlc.Notification, error) {
	f.inserted = append(f.inserted, arg)
	return sqlc.Notification{}, nil
}

func (f *fakeQueries) ListMembersByOrg(_ context.Context, _ string) ([]sqlc.ListMembersByOrgRow, error) {
	return f.members, f.memberError
}

// Selecting Knock must not change what the in-app inbox contains: the
// preference is honored exactly as notifications-postgres honors it.
func TestDurableQueueHonorsTheInAppPreference(t *testing.T) {
	muted := &fakeQueries{preference: sqlc.NotificationPreference{InApp: false}}
	if err := (queryQueue{q: muted}).Enqueue(context.Background(), notifications.Message{UserID: "usr_1", Kind: "k"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if len(muted.inserted) != 0 {
		t.Fatalf("a muted kind was written: %v", muted.inserted)
	}

	unset := &fakeQueries{preferErr: pgx.ErrNoRows}
	if err := (queryQueue{q: unset}).Enqueue(context.Background(), notifications.Message{OrgID: "org_1", UserID: "usr_1", Kind: "k", Title: "t", Body: "b", URL: "/u"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if len(unset.inserted) != 1 || unset.inserted[0].Url != "/u" {
		t.Fatalf("inserted = %v", unset.inserted)
	}

	broken := &fakeQueries{preferErr: errors.New("database down")}
	if err := (queryQueue{q: broken}).Enqueue(context.Background(), notifications.Message{UserID: "usr_1", Kind: "k"}); err == nil {
		t.Fatal("a preference read failure was swallowed by the queue")
	}
}

func TestNewModuleRefusesAMissingAPIKey(t *testing.T) {
	h := apphost.Map(map[string]string{"APP_ENV": "production"}, time.Now(), "v-test")
	_, err := NewModule(context.Background(), h, Deps{Queries: &fakeQueries{}})
	if err == nil {
		t.Fatal("the managed adapter booted with no credentials")
	}
	if !strings.Contains(err.Error(), "KNOCK_API_KEY") {
		t.Fatalf("refusal %q does not name the key it needs", err)
	}
}

func TestNewModuleRequiresQueries(t *testing.T) {
	h := apphost.Map(map[string]string{"KNOCK_API_KEY": "sk_test_12345"}, time.Now(), "v-test")
	if _, err := NewModule(context.Background(), h, Deps{}); err == nil {
		t.Fatal("the adapter booted with no durable path")
	}
}

// End to end through the module: the declared environment reaches the client,
// the durable row lands, and the trigger runs.
func TestNewModuleWiresTheDurableRowAndTheTrigger(t *testing.T) {
	var capture capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		_, _ = w.Write([]byte(`{"workflow_run_id":"run_1"}`))
	}))
	t.Cleanup(srv.Close)

	h := apphost.Map(map[string]string{"KNOCK_API_KEY": "sk_live_wired", "KNOCK_URL": srv.URL}, time.Now(), "v-test")
	queries := &fakeQueries{preferErr: pgx.ErrNoRows}
	m, err := NewModule(context.Background(), h, Deps{Queries: queries})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if err := m.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if err := m.Value.Send(context.Background(), "org_1", "usr_1", "project.created", "t", "b", "/u"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ctx := stopContext(t)
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if len(queries.inserted) != 1 {
		t.Fatalf("durable rows = %v", queries.inserted)
	}
	if capture.requests() != 1 || capture.auth != "Bearer sk_live_wired" {
		t.Fatalf("provider requests = %d, authorization = %q", capture.requests(), capture.auth)
	}
}

// The defect this adapter shipped with: a nil trigger is the local adapter
// wearing a provider's name, and health must say so.
func TestHealthRefusesAMissingTrigger(t *testing.T) {
	n := New(&recordingQueue{}, nil, nil, nil)
	if err := n.Health(context.Background()); err == nil {
		t.Fatal("a notifier with no trigger reported healthy")
	}
}

func stopContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}
