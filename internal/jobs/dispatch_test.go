package jobs

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/mail"
)

func dispatchTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// A persisted row whose kind no module provides any more — the module was
// removed while its work was still queued — must die on the first claim. Eight
// exponential-backoff retries of a handler that cannot exist wastes hours of
// queue capacity and buries the real signal, so the reason is explicit.
func TestUnknownKindDeadLettersImmediately(t *testing.T) {
	pool, queries := testdb.Open(t, "jobs_dispatch")
	defer pool.Close()

	worker := NewWorker(queries, mail.NewDevSender(dispatchTestLogger(), t.TempDir()), dispatchTestLogger())

	id, err := queries.EnqueueJob(context.Background(), sqlc.EnqueueJobParams{
		Kind: "module.that.was.removed", Payload: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	worked, err := worker.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !worked {
		t.Fatal("ProcessOne did not claim the queued job")
	}

	var doneAt *string
	var lastError *string
	var attempts int32
	row := pool.QueryRow(context.Background(),
		"SELECT done_at::text, last_error, attempts FROM jobs WHERE id = $1", id)
	if err := row.Scan(&doneAt, &lastError, &attempts); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if doneAt == nil {
		t.Fatal("unknown kind was left claimable; it must be dead-lettered")
	}
	if lastError == nil || *lastError != "module_uninstalled" {
		t.Fatalf("last_error = %v, want module_uninstalled", lastError)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1; the job must not burn retries", attempts)
	}

	// A dead-lettered unknown kind is still visible as dead and still
	// requeueable, so reinstalling the module makes the work recoverable.
	jobs, err := queries.ListJobs(context.Background(), sqlc.ListJobsParams{Filter: "", Lim: 10, Off: 0})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	var status string
	for _, j := range jobs {
		if j.ID == id {
			status = j.Status
		}
	}
	if status != "dead" {
		t.Fatalf("admin status = %q, want dead", status)
	}
	if err := queries.RequeueDeadJob(context.Background(), id); err != nil {
		t.Fatalf("RequeueDeadJob: %v", err)
	}
	var requeued *string
	if err := pool.QueryRow(context.Background(),
		"SELECT done_at::text FROM jobs WHERE id = $1", id).Scan(&requeued); err != nil {
		t.Fatalf("read requeued job: %v", err)
	}
	if requeued != nil {
		t.Fatal("a dead unknown-kind job could not be requeued")
	}
}

// Define is the module-facing way to declare a job. maxAttempts==0 means "use
// the default" rather than "never retry", because a zero in a struct literal is
// almost always an omission, and a job that never retries is a silent data-loss
// bug the first time the network blips.
func TestDefineNormalizesMaxAttempts(t *testing.T) {
	type payload struct {
		OrgID string `json:"org_id"`
	}
	handled := ""
	definition := Define("test.kind", true, 0, func(_ context.Context, p payload) error {
		handled = p.OrgID
		return nil
	})

	if definition.Kind != "test.kind" {
		t.Fatalf("Kind = %q", definition.Kind)
	}
	if !definition.Schedulable {
		t.Fatal("Schedulable was dropped")
	}
	if definition.MaxAttempts != DefaultMaxAttempts {
		t.Fatalf("MaxAttempts = %d, want %d for an omitted value",
			definition.MaxAttempts, DefaultMaxAttempts)
	}
	if err := definition.Handle(context.Background(), []byte(`{"org_id":"org_1"}`), Attempt{Number: 1, Max: 8}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if handled != "org_1" {
		t.Fatalf("payload did not reach the handler: %q", handled)
	}

	// An explicit value is honoured, including a deliberate single attempt.
	if got := Define("k", false, 1, func(context.Context, payload) error { return nil }).MaxAttempts; got != 1 {
		t.Fatalf("explicit MaxAttempts = %d, want 1", got)
	}
}

// A malformed payload is the handler's failure, not the dispatcher's: it must
// surface as an error the retry machinery can see rather than a panic.
func TestDefineReportsMalformedPayload(t *testing.T) {
	type payload struct {
		N int `json:"n"`
	}
	definition := Define("k", false, 3, func(context.Context, payload) error { return nil })
	if err := definition.Handle(context.Background(), []byte(`{"n":"not-a-number"}`), Attempt{Number: 1, Max: 3}); err == nil {
		t.Fatal("Handle accepted a malformed payload")
	}
}

// The enqueue contract carries the declared attempt budget onto the row, and the
// row is dispatch truth from then on. Without this a job enqueued before a
// module changed its budget would silently adopt the new one mid-flight.
func TestEnqueueRecordsDeclaredAttemptBudget(t *testing.T) {
	pool, queries := testdb.Open(t, "jobs_attempts")
	defer pool.Close()

	id, err := queries.EnqueueJob(context.Background(), sqlc.EnqueueJobParams{
		Kind: "k", Payload: []byte(`{}`), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	var maxAttempts int32
	if err := pool.QueryRow(context.Background(),
		"SELECT max_attempts FROM jobs WHERE id = $1", id).Scan(&maxAttempts); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if maxAttempts != 3 {
		t.Fatalf("max_attempts = %d, want the declared 3", maxAttempts)
	}
}

// The default attempt budget is written in three places: the Go constant, the
// column default, and the enqueue query's fallback. They must agree, or a job
// enqueued through a different path silently gets a different retry budget.
func TestAttemptBudgetDefaultsAgree(t *testing.T) {
	pool, queries := testdb.Open(t, "jobs_defaults")
	defer pool.Close()

	var columnDefault int
	if err := pool.QueryRow(context.Background(), `
		SELECT (column_default)::int FROM information_schema.columns
		WHERE table_name = 'jobs' AND column_name = 'max_attempts'`).Scan(&columnDefault); err != nil {
		t.Fatalf("read column default: %v", err)
	}
	if columnDefault != DefaultMaxAttempts {
		t.Fatalf("jobs.max_attempts column default = %d, jobs.DefaultMaxAttempts = %d",
			columnDefault, DefaultMaxAttempts)
	}

	// The enqueue fallback: passing 0 must land the same default.
	id, err := queries.EnqueueJob(context.Background(), sqlc.EnqueueJobParams{
		Kind: "k", Payload: []byte(`{}`), MaxAttempts: 0,
	})
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	var stored int
	if err := pool.QueryRow(context.Background(),
		"SELECT max_attempts FROM jobs WHERE id = $1", id).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if stored != DefaultMaxAttempts {
		t.Fatalf("enqueue fallback stored %d, want %d", stored, DefaultMaxAttempts)
	}
}

// A module that declares a non-default attempt budget must actually get it. The
// budget travels through Enqueue onto the row, so this is the only place it can
// be applied — the worker reads the row, never the declaration.
//
// Every kind currently declares the default, so this drives a non-default value
// through the generated table rather than asserting a value the column default
// would produce anyway.
func TestEnqueueAppliesDeclaredBudget(t *testing.T) {
	pool, queries := testdb.Open(t, "jobs_declared")
	defer pool.Close()
	ctx := context.Background()

	declaredAttempts["test.budget"] = 3
	defer delete(declaredAttempts, "test.budget")

	if err := Enqueue(ctx, queries, "test.budget", map[string]string{}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	var got int32
	if err := pool.QueryRow(ctx,
		`SELECT max_attempts FROM jobs WHERE kind = 'test.budget'`).Scan(&got); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if got != 3 {
		t.Fatalf("max_attempts = %d, want the declared 3", got)
	}

	// An undeclared kind still enqueues — a project may queue work before the
	// module that handles it is installed — and falls back to the default.
	if err := Enqueue(ctx, queries, "not.declared", map[string]string{}); err != nil {
		t.Fatalf("Enqueue undeclared: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT max_attempts FROM jobs WHERE kind = 'not.declared'`).Scan(&got); err != nil {
		t.Fatalf("read undeclared row: %v", err)
	}
	if int(got) != DefaultMaxAttempts {
		t.Fatalf("undeclared max_attempts = %d, want the default %d", got, DefaultMaxAttempts)
	}
}
