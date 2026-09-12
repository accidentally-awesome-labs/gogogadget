// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry runs it, and no derivative ever
// receives it. It asserts about THIS repository's maintained managed
// reference targets — the Neon/Clerk/Polar/Resend/R2/PostHog/Sentry/
// OpenAI-compatible/Upstash/Typesense/Ably/OTLP accounts the framework keeps
// working — never about the source the registry distributes.
//
// # What this is
//
// The managed-target canary suite: tier 2 of the provider verification the
// roadmap names (content/docs/roadmap.md) and the plan's binding constraint
// fixes — "Provider CI uses fake HTTP/local protocol containers; optional live
// canaries are not contributor gates."
//
// Tier 1 is the contributor gate and stays that way: every managed adapter's
// package test drives an httptest fake or a local protocol container, runs in
// `make check`, and needs no account. Those fakes encode a BELIEF about each
// provider's wire shape. Nothing in this repository ever checked that belief
// against the provider, so the failure mode is silent: the provider changes a
// header, a path or a response field, every fake stays green, and the
// derivative breaks in production.
//
// This suite is the check. Each row drives the real provider with live
// credentials and asserts the same wire shape its fake asserts — drift, not
// liveness. A row that can only observe "the call returned 2xx" says so in
// its Asserts prose, because that is worth little and pretending otherwise is
// worse than admitting it.
//
// # Tier and cost
//
// It is not a contributor gate and must never become one:
//   - It is opt-in through GGG_LIVE_CANARY=1. CI's `live-canary` workflow
//     (workflow_dispatch plus one weekly schedule, never push/pull_request,
//     never in a needs chain) is the only thing that sets it, and the skip
//     everywhere else carries InapplicableSkipMarker.
//   - Every row skips itself, with its own reason naming its own missing
//     keys, when its credentials are absent. A contributor with GGG_LIVE_CANARY=1
//     and no secrets gets one reasoned [inapplicable] line per row and a
//     pass, never a failure.
//   - Each row runs under liveCanaryRowDeadline, so the whole suite is
//     bounded at len(liveCanaryProviders) times that even when a provider
//     hangs. The bound is computed into the gate's own skip message rather
//     than written down here, because a row count in prose is a number that
//     goes stale the next time a provider is added.
//
// # Safety
//
// Three rules, stated per row and enforced here rather than left to each
// probe's good intentions:
//
//  1. Cleanup is declared. liveCanaryCleans means the probe removes what it
//     created through the seam (storage Delete, search Delete, cache Delete).
//     liveCanaryLeavesBounded means the seam has no delete but the remote
//     state is bounded by a stable identifier or a TTL. liveCanaryLeavesImmutable
//     means the run leaves remote state that CANNOT be removed through the
//     seam — real mail, an ingested analytics event, a Sentry issue, a
//     published channel message, an exported audit entry — and the Leaves
//     prose tells the operator exactly what accumulates. content/docs/testing.md
//     repeats it, because the person paying for the account is not the person
//     reading this file.
//  2. No probe ever points at production. A row declares the selectors that
//     would aim it at a production tenant (Polar's POLAR_SERVER enum is the
//     one that exists today) and liveCanaryForbiddenSelector REFUSES the run
//     — a hard failure, not a skip, because an operator who set it asked for
//     something this suite will not do.
//  3. No failure may print a credential. Probes return errors instead of
//     calling t.Fatal, and the one reporting path runs every rendered message
//     through liveCanaryEnv.Redact, which scrubs each row's credential values
//     and any URL userinfo inside them. The provider and the endpoint are
//     named; the secret is not.
//
// # Coverage is a row, not a file
//
// TestEveryManagedAdapterIsCanariedOrExcused walks the registry, finds every
// module publishing a `managed` service target, and refuses one that appears
// in neither liveCanaryProviders nor liveCanaryNothingToProbe. Adding a
// managed adapter is therefore a row; it cannot be forgotten. The same test
// refuses a dead row and checks each row's adapter-configuration keys still
// exist in the owning manifest, so a renamed env key fails here instead of
// making a canary silently skip forever.

package canary

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/gggcli"

	posthogadapter "github.com/gogogadget/gogogadget/internal/analytics/posthog"
	"github.com/gogogadget/gogogadget/internal/audit"
	auditotlp "github.com/gogogadget/gogogadget/internal/audit_export/otlp"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/billing/polar"
	cacheredis "github.com/gogogadget/gogogadget/internal/cache/redis"
	"github.com/gogogadget/gogogadget/internal/identity/clerk"
	"github.com/gogogadget/gogogadget/internal/llm"
	"github.com/gogogadget/gogogadget/internal/llm/openai"
	"github.com/gogogadget/gogogadget/internal/mail/resend"
	"github.com/gogogadget/gogogadget/internal/notifications"
	knockadapter "github.com/gogogadget/gogogadget/internal/notifications/knock"
	"github.com/gogogadget/gogogadget/internal/observability/sentryadapter"
	"github.com/gogogadget/gogogadget/internal/provision/neon"
	ratelimitredis "github.com/gogogadget/gogogadget/internal/ratelimit/redis"
	"github.com/gogogadget/gogogadget/internal/realtime/ably"
	"github.com/gogogadget/gogogadget/internal/remote"
	"github.com/gogogadget/gogogadget/internal/search"
	"github.com/gogogadget/gogogadget/internal/search/typesense"
	s3adapter "github.com/gogogadget/gogogadget/internal/storage/s3"
	telemetryotlp "github.com/gogogadget/gogogadget/internal/telemetry/otlp"
	openmeteradapter "github.com/gogogadget/gogogadget/internal/usage/openmeter"
	"github.com/gogogadget/gogogadget/internal/webhooks"
	svixadapter "github.com/gogogadget/gogogadget/internal/webhooks/svix"
)

// liveCanaryEnvVar un-skips the suite, mirroring GGG_ERA_WALK and
// GGG_GENESIS_SWEEP. Only the `live-canary` workflow sets it.
const liveCanaryEnvVar = "GGG_LIVE_CANARY"

// liveCanaryRowDeadline bounds one row. A provider that hangs costs one
// deadline, not the job's whole timeout, and the suite's worst case is
// len(liveCanaryProviders) times this.
const liveCanaryRowDeadline = 30 * time.Second

// liveCanaryStableID is the identifier every probe that cannot clean up must
// reuse. Polar deduplicates metered events on ExternalID
// (internal/billing/client.go:19-25), the Redis rate-limit counter is keyed
// and self-expiring (internal/ratelimit/redis/redis.go:28), and PostHog's
// distinct id groups every canary event under one person instead of creating
// a new one per run. Stable, not random: unbounded accumulation in someone
// else's paid account is the failure mode.
const liveCanaryStableID = "ggg-live-canary"

// liveCanaryRedaction replaces a credential value wherever it would be
// rendered. It names the key so a reader can tell WHICH secret was scrubbed
// without learning its value.
func liveCanaryRedaction(key string) string { return "[redacted " + key + "]" }

// liveCanaryKey is one environment variable a row consumes.
type liveCanaryKey struct {
	// Env is the variable's name.
	Env string
	// Required rows skip when it is unset; optional keys widen a probe's
	// assertions when present and are omitted from the skip reason.
	Required bool
	// Credential marks a value that carries authority and must never appear
	// in rendered output. It is deliberately independent of the owning
	// manifest's `secret` flag: POSTHOG_API_KEY is a credential that the
	// manifest does not mark secret
	// (registry/modules/system/analytics-posthog/module.json:201-209), and a
	// canary that leaked it because a manifest was wrong would be this
	// suite's fault, not the manifest's.
	Credential bool
	// Harness marks a canary input rather than adapter configuration — the
	// throwaway sink address, the operator-created collection. Adapter keys
	// are checked against the owning manifest; harness keys are not, because
	// no manifest declares them.
	Harness bool
}

// liveCanaryCleanup is what a run leaves behind, as an enumerated claim
// rather than prose a reader has to interpret.
type liveCanaryCleanup int

const (
	// liveCanaryCleans: the probe removes everything it created, through the
	// seam, and verifies the removal.
	liveCanaryCleans liveCanaryCleanup = iota
	// liveCanaryLeavesBounded: the seam offers no delete, but the remote
	// state is bounded — a stable identifier that is overwritten or
	// deduplicated, or a TTL the adapter sets itself.
	liveCanaryLeavesBounded
	// liveCanaryLeavesImmutable: the run creates remote state that cannot be
	// removed through the seam at all.
	liveCanaryLeavesImmutable
)

func (c liveCanaryCleanup) String() string {
	switch c {
	case liveCanaryCleans:
		return "self-cleaning"
	case liveCanaryLeavesBounded:
		return "leaves bounded state"
	default:
		return "leaves immutable state"
	}
}

// liveCanaryForbidden is a selector value that aims a probe somewhere it must
// never go. It is a refusal, not a skip.
type liveCanaryForbidden struct {
	Env    string
	Values []string
	Why    string
}

// liveCanaryProvider is one managed adapter's canary. Adding a provider is a
// row here; there is no second place to edit.
type liveCanaryProvider struct {
	// Slot, Module and Target are the registry coordinates the row canaries.
	Slot, Module, Target string
	// Keys is every variable the row reads, adapter configuration first.
	Keys []liveCanaryKey
	// Forbid is the production-selector refusal set.
	Forbid []liveCanaryForbidden
	// Cleanup and Leaves are the safety claim and its prose.
	Cleanup liveCanaryCleanup
	Leaves  string
	// Asserts states the wire shape the probe pins, in the same terms the
	// package's httptest fake states it. Where acceptance is all the seam can
	// observe, this says so.
	Asserts string
	// Probe drives the provider. It returns the endpoint it talked to — named
	// on success and on failure — and an error that the caller redacts.
	Probe func(ctx context.Context, env liveCanaryEnv) (string, error)
}

// liveCanaryNothingToProbe is the explicit allowlist: modules that publish a
// `managed` service target and yet make NO network call in the wiring a
// production boot produces, so there is no wire shape for a live canary to
// check. Each entry states why, with the file:line that makes it true. A
// module here is excused from liveCanaryProviders and nowhere else.
//
// Three entries have already left: notifications-knock, webhooks-svix and
// usage-openmeter each wired New(d.Queue, nil, nil) and stopped at the local
// Postgres queue. They now trigger, create and ingest for real, so they are
// rows in liveCanaryProviders instead — TestEveryManagedAdapterIsCanariedOrExcused
// refuses the halfway state in both directions.
var liveCanaryNothingToProbe = map[string]string{
	"ggg/system/feature-flags-launchdarkly": "a different reason, and not DeliveryAdapters': the adapter holds no HTTP client at all. It is function-injected (EnabledFn/ListFn/OverridesFn, internal/flags/launchdarkly/launchdarkly.go:17-33), every mutation returns flags.ErrReadOnly (:46,:47,:54,:55), and NewModule refuses a nil Deps.Client (:74-76) while no generated wiring anywhere provides a launchdarkly.Client — so selecting this adapter cannot boot, let alone reach LaunchDarkly. Canarying it would require the client to exist first",
}

// liveCanaryManifestKeyScopeExceptions records adapter keys a row reads that
// the owning manifest declares WITHOUT scoping them to the canaried target,
// so the generated parser would not enforce them for that target. Each is a
// manifest defect this suite found and cannot fix here: correcting the
// `targets` list is a payload change that needs a module revision bump, and
// bumps are the release owner's act, not a test's.
//
// Keyed `<module>@<target>:<KEY>`. The walk refuses an undeclared mismatch
// AND a dead entry, so fixing a manifest fails here by name.
var liveCanaryManifestKeyScopeExceptions = map[string]string{
	"ggg/system/cache-redis@upstash:CACHE_REDIS_URL":           "registry/modules/system/cache-redis/module.json scopes CACHE_REDIS_URL to ggg/system/cache-redis@valkey only, so the managed upstash target declares a token and no endpoint; the adapter needs both (internal/cache/redis/redis.go:32)",
	"ggg/system/rate-limit-redis@upstash:RATE_LIMIT_REDIS_URL": "registry/modules/system/rate-limit-redis/module.json scopes RATE_LIMIT_REDIS_URL to ggg/system/rate-limit-redis@valkey only, the same defect as cache-redis; the adapter needs both (internal/ratelimit/redis/redis.go:25)",
	"ggg/system/telemetry-otlp@otlp:OTLP_ENDPOINT":             "registry/modules/system/telemetry-otlp/module.json scopes OTLP_ENDPOINT to ggg/system/telemetry-otlp@collector only, so the managed otlp target declares an API key and no endpoint; NewModule reads OTLP_ENDPOINT unconditionally (internal/telemetry/otlp/otlp.go:88)",
}

// liveCanaryProviders is the suite. One row per managed adapter that makes a
// real network call in the wiring a production boot produces.
var liveCanaryProviders = []liveCanaryProvider{
	{
		Slot: "ggg/mail", Module: "ggg/system/mail-resend", Target: "resend",
		Keys: []liveCanaryKey{
			{Env: "RESEND_API_KEY", Required: true, Credential: true},
			{Env: "GGG_CANARY_MAIL_FROM", Required: true, Harness: true},
			{Env: "GGG_CANARY_MAIL_TO", Required: true, Harness: true},
		},
		Cleanup: liveCanaryLeavesImmutable,
		Leaves:  "one real email per run, delivered to GGG_CANARY_MAIL_TO with subject `ggg live canary <RFC3339 date>`. mail.Sender has exactly one method, Send (internal/mail/mail.go:21-23): there is no recall or delete through this seam, so GGG_CANARY_MAIL_TO must be a throwaway sink the operator owns, never a customer address. The send also counts against the account's monthly quota.",
		Asserts: "POST https://api.resend.com/emails still accepts the exact body internal/mail/resend/resend.go:48 sends ({from, to[], subject, html, text}) with a bearer key, and still answers with a JSON object carrying a non-empty `id` — the two halves internal/mail/resend/contract_test.go:42-45 believes. Then the adapter's own health path, GET /domains through resend-go (resend.go:60), still authenticates the key.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			const endpoint = "https://api.resend.com/emails"
			body, err := json.Marshal(map[string]any{
				"from":    env.Get("GGG_CANARY_MAIL_FROM"),
				"to":      []string{env.Get("GGG_CANARY_MAIL_TO")},
				"subject": "ggg live canary " + time.Now().UTC().Format(time.RFC3339),
				"html":    "<p>ggg live canary</p>",
				"text":    "ggg live canary",
			})
			if err != nil {
				return endpoint, err
			}
			var out struct {
				ID string `json:"id"`
			}
			if err := liveCanaryJSON(ctx, http.MethodPost, endpoint, map[string]string{
				"Authorization": "Bearer " + env.Get("RESEND_API_KEY"),
				"Content-Type":  "application/json",
			}, body, &out); err != nil {
				return endpoint, fmt.Errorf("send (POST /emails): %w", err)
			}
			if out.ID == "" {
				return endpoint, fmt.Errorf("send (POST /emails): the response carried no `id`; internal/mail/resend/contract_test.go:42-45 believes it always does")
			}
			if err := resend.NewResendSender(env.Get("RESEND_API_KEY"), env.Get("GGG_CANARY_MAIL_FROM")).Health(ctx); err != nil {
				return endpoint, fmt.Errorf("health (GET /domains via resend-go): %w", err)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/storage", Module: "ggg/system/storage-s3", Target: "r2",
		Keys: []liveCanaryKey{
			{Env: "STORAGE_R2_ACCOUNT_ID", Required: true},
			{Env: "STORAGE_R2_ACCESS_KEY_ID", Required: true, Credential: true},
			{Env: "STORAGE_R2_SECRET_ACCESS_KEY", Required: true, Credential: true},
			{Env: "STORAGE_R2_BUCKET", Required: true},
		},
		Cleanup: liveCanaryCleans,
		Leaves:  "nothing. The probe Puts one object under the `ggg-live-canary/` prefix and Deletes it through storage.Store.Delete (internal/storage/storage.go:30, internal/storage/s3/r2.go:83), then proves the presigned GET of the deleted key 404s. A failed run between Put and Delete can leave one small object at that prefix; the bucket's own lifecycle rule is the backstop.",
		Asserts: "the four S3 shapes the seam depends on, against real Cloudflare R2: SigV4 on a path-style PutObject is accepted (UsePathStyle is set for R2 and MinIO alike, internal/storage/s3/r2.go:38); HeadBucket answers for Health (:91); PresignGetObject still produces a URL carrying X-Amz-Signature and X-Amz-Expires=900 that R2 honours for a 15-minute window (:63-65) and the seam still answers 303 with it in Location (:71-72); and DeleteObject removes the key. These are exactly the assertions internal/storage/s3/protocol_test.go:74-133 makes against an in-process fake.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			endpoint := "https://" + env.Get("STORAGE_R2_ACCOUNT_ID") + ".r2.cloudflarestorage.com/" + env.Get("STORAGE_R2_BUCKET")
			store, err := s3adapter.NewR2Store(ctx,
				env.Get("STORAGE_R2_ACCOUNT_ID"),
				env.Get("STORAGE_R2_ACCESS_KEY_ID"),
				env.Get("STORAGE_R2_SECRET_ACCESS_KEY"),
				env.Get("STORAGE_R2_BUCKET"),
				"",
			)
			if err != nil {
				return endpoint, fmt.Errorf("construct R2 store: %w", err)
			}
			if err := store.Health(ctx); err != nil {
				return endpoint, fmt.Errorf("health (HeadBucket): %w", err)
			}
			key := liveCanaryStableID + "/" + liveCanaryNonce() + ".txt"
			payload := []byte("ggg live canary " + time.Now().UTC().Format(time.RFC3339))
			if _, err := store.Put(ctx, key, "text/plain", bytes.NewReader(payload)); err != nil {
				return endpoint, fmt.Errorf("put (PutObject %s): %w", key, err)
			}
			// Delete is registered before the read assertions so a failing
			// presign cannot leave the object behind.
			defer func() { _ = store.Delete(context.WithoutCancel(ctx), key) }()

			recorder := httptest.NewRecorder()
			if err := store.Serve(ctx, recorder, key, "canary.txt", "text/plain"); err != nil {
				return endpoint, fmt.Errorf("serve (PresignGetObject %s): %w", key, err)
			}
			if recorder.Code != http.StatusSeeOther {
				return endpoint, fmt.Errorf("serve (PresignGetObject): the seam answered %d, want 303 with the presigned URL in Location", recorder.Code)
			}
			presigned := recorder.Header().Get("Location")
			for _, want := range []string{"X-Amz-Signature=", "X-Amz-Expires=900"} {
				if !strings.Contains(presigned, want) {
					return endpoint, fmt.Errorf("serve (PresignGetObject): the presigned URL carries no %s; internal/storage/s3/protocol_test.go:42-45 believes it always does", want)
				}
			}
			served, status, err := liveCanaryGet(ctx, presigned)
			if err != nil {
				return endpoint, fmt.Errorf("read the presigned GET: %w", err)
			}
			if status != http.StatusOK || !bytes.Equal(served, payload) {
				return endpoint, fmt.Errorf("read the presigned GET: %d and %d byte(s), want 200 and the %d byte(s) that were put", status, len(served), len(payload))
			}
			if err := store.Delete(ctx, key); err != nil {
				return endpoint, fmt.Errorf("delete (DeleteObject %s): %w", key, err)
			}
			if _, status, err := liveCanaryGet(ctx, presigned); err != nil {
				return endpoint, fmt.Errorf("re-read the presigned GET after delete: %w", err)
			} else if status != http.StatusNotFound {
				return endpoint, fmt.Errorf("re-read the presigned GET after delete: %d, want 404; the object outlived Delete", status)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/billing", Module: "ggg/system/billing-polar", Target: "polar",
		Keys: []liveCanaryKey{
			{Env: "POLAR_ACCESS_TOKEN", Required: true, Credential: true},
			{Env: "POLAR_PRODUCT_PRO", Required: true},
			{Env: "POLAR_SERVER"},
		},
		Forbid: []liveCanaryForbidden{{
			Env:    "POLAR_SERVER",
			Values: []string{"production"},
			Why:    "the probe creates a checkout session and ingests a metered event, and Polar's metered events are immutable. The sandbox server exists precisely so this is free of consequence (internal/billing/polar/client.go:22-25); unset or `sandbox` is the only selector this canary will run against",
		}},
		Cleanup: liveCanaryLeavesBounded,
		Leaves:  "one checkout session per run (Polar expires unpaid sessions on its own; there is no delete for one on billing.Client, internal/billing/client.go:7-12) and at most ONE metered event ever, because the probe ingests with the stable ExternalID `ggg-live-canary` and Polar deduplicates on it (internal/billing/client.go:19-25). billing.Client.RevokeSubscription exists (:10) but the probe creates no subscription, so it is not used. All of it lands in the sandbox tenant, never production.",
		Asserts: "the three request shapes internal/billing/polar/client_test.go:19-36 fakes, against the real Polar sandbox: every call still needs `Authorization: Bearer` plus `Polar-Version: 2026-04` (client.go:64-65) — a version bump that dropped 2026-04 would fail here and nowhere else; POST /v1/checkouts/ still accepts {products[], success_url, external_customer_id, metadata} and answers with a non-empty `url` (:84-106); and POST /v1/events/ingest still accepts {events:[{name, external_customer_id, metadata, external_id}]} (:128-152). The subscription-event payload the webhook parses cannot be asserted here: it arrives only from a real purchase, so internal/billing/polar/contract_test.go:110-127 remains its only coverage.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			server := strings.TrimSpace(env.Get("POLAR_SERVER"))
			if server == "" {
				server = "sandbox"
			}
			if server != "sandbox" {
				// NewClient silently falls back to sandbox for an
				// unrecognised value (client.go:40-43). Canarying sandbox
				// while the operator named something else is a lie about
				// what ran.
				return "", fmt.Errorf("POLAR_SERVER=%q is not a server this canary recognises; the adapter would silently fall back to sandbox (internal/billing/polar/client.go:40-43), so the run would report on a target nobody selected", server)
			}
			endpoint := "https://sandbox-api.polar.sh"
			client := polar.NewClient(env.Get("POLAR_ACCESS_TOKEN"), server)
			checkout, err := client.CreateCheckout(ctx, billing.CheckoutParams{
				ProductID:          env.Get("POLAR_PRODUCT_PRO"),
				SuccessURL:         "https://example.invalid/" + liveCanaryStableID,
				CustomerExternalID: liveCanaryStableID,
				Metadata:           map[string]string{"org_id": liveCanaryStableID},
			})
			if err != nil {
				return endpoint, fmt.Errorf("create checkout (POST /v1/checkouts/): %w", err)
			}
			if !strings.HasPrefix(checkout, "https://") {
				return endpoint, fmt.Errorf("create checkout (POST /v1/checkouts/): returned %q, want an https checkout URL", checkout)
			}
			if err := client.IngestUsage(ctx, liveCanaryStableID, []billing.UsageEvent{{
				Name:       liveCanaryStableID,
				ExternalID: liveCanaryStableID,
				Value:      1,
			}}); err != nil {
				return endpoint, fmt.Errorf("ingest usage (POST /v1/events/ingest): %w", err)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/identity", Module: "ggg/system/identity-clerk", Target: "clerk",
		Keys: []liveCanaryKey{
			{Env: "CLERK_SECRET_KEY", Required: true, Credential: true},
			{Env: "CLERK_FRONTEND_API_URL", Required: true},
			{Env: "GGG_CANARY_CLERK_USER_SUBJECT", Harness: true},
			{Env: "GGG_CANARY_CLERK_SESSION_JWT", Credential: true, Harness: true},
			{Env: "GGG_CANARY_CLERK_ORG_SUBJECT", Harness: true},
		},
		Cleanup: liveCanaryCleans,
		Leaves:  "nothing. Every call is a read: the JWKS document, and — when GGG_CANARY_CLERK_USER_SUBJECT is set — one user fetch and one subject verification of a user the operator created by hand. Nothing on the identity seam creates a Clerk user (identity.Verifier/UserFetcher/Deleter are verify, read and delete), so the probe has nothing to clean; identity.Deleter.DeleteUser (internal/identity/deleter.go:6) is deliberately NOT called, because deleting the operator's fixture user would make the next run skip.",
		Asserts: "the JWKS document the clerk-sdk jwks client resolves every session token against still exists at CLERK_FRONTEND_API_URL/.well-known/jwks.json and still carries keys[] with kty/kid/alg/use/n/e — the exact shape internal/identity/clerk/contract_test.go:46-56 serves from a fake. With GGG_CANARY_CLERK_USER_SUBJECT: the Clerk Backend API user read (clerk.go:77) and subject verification (clerk.go:44-51) still answer, which today have ZERO drift coverage — NewUserFetcher and NewDeleter build a ClientConfig with only a Key (clerk.go:73,:99), so no fake can reach them. With GGG_CANARY_CLERK_SESSION_JWT: Verify still returns the v2 organisation claim block as neutral claims with the `org:` role prefix already stripped (clerk.go:37, contract_test.go:80-96) — otherwise that block is untested here and the fake remains its only coverage.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			frontend := strings.TrimRight(env.Get("CLERK_FRONTEND_API_URL"), "/")
			endpoint := frontend + "/.well-known/jwks.json"
			var jwks struct {
				Keys []map[string]any `json:"keys"`
			}
			if err := liveCanaryJSON(ctx, http.MethodGet, endpoint, nil, nil, &jwks); err != nil {
				return endpoint, fmt.Errorf("read the JWKS: %w", err)
			}
			if len(jwks.Keys) == 0 {
				return endpoint, fmt.Errorf("read the JWKS: keys[] is empty, so no session token can be verified")
			}
			for index, key := range jwks.Keys {
				for _, field := range []string{"kty", "kid", "alg", "use", "n", "e"} {
					if value, ok := key[field].(string); !ok || value == "" {
						return endpoint, fmt.Errorf("read the JWKS: keys[%d] carries no %q; internal/identity/clerk/contract_test.go:46-56 believes every key does", index, field)
					}
				}
			}
			if subject := env.Get("GGG_CANARY_CLERK_USER_SUBJECT"); subject != "" {
				secret := env.Get("CLERK_SECRET_KEY")
				profile, err := clerk.NewUserFetcher(secret).Fetch(ctx, subject)
				if err != nil {
					return endpoint, fmt.Errorf("fetch the canary user (Clerk Backend API GET /v1/users/%s): %w", subject, err)
				}
				if profile.Email == "" {
					return endpoint, fmt.Errorf("fetch the canary user: the profile carried no email; internal/identity/clerk/clerk.go:81-92 selects one from EmailAddresses and the shape has changed")
				}
				claims, err := clerk.NewVerifier(secret).VerifySubject(ctx, subject)
				if err != nil {
					return endpoint, fmt.Errorf("verify the canary subject (Clerk Backend API): %w", err)
				}
				if claims.UserSubject != subject {
					return endpoint, fmt.Errorf("verify the canary subject: returned %q, want %q", claims.UserSubject, subject)
				}
			}
			if token := env.Get("GGG_CANARY_CLERK_SESSION_JWT"); token != "" {
				claims, err := clerk.NewVerifier(env.Get("CLERK_SECRET_KEY")).Verify(ctx, token)
				if err != nil {
					return endpoint, fmt.Errorf("verify the canary session token against the live JWKS: %w", err)
				}
				if claims.UserSubject == "" {
					return endpoint, fmt.Errorf("verify the canary session token: the claims carried no subject")
				}
				if strings.HasPrefix(claims.OrgRole, "org:") {
					return endpoint, fmt.Errorf("verify the canary session token: the organisation role still carries the `org:` wire prefix (%q); internal/identity/clerk/contract_test.go:80-96 believes the SDK strips it", claims.OrgRole)
				}
				if want := env.Get("GGG_CANARY_CLERK_ORG_SUBJECT"); want != "" && claims.OrgSubject != want {
					return endpoint, fmt.Errorf("verify the canary session token: the v2 organisation claim block resolved to %q, want %q", claims.OrgSubject, want)
				}
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/analytics", Module: "ggg/system/analytics-posthog", Target: "posthog",
		Keys: []liveCanaryKey{
			{Env: "POSTHOG_API_KEY", Required: true, Credential: true},
			{Env: "POSTHOG_HOST"},
		},
		Cleanup: liveCanaryLeavesImmutable,
		Leaves:  "one immutable `ggg_live_canary` event per run, attributed to the stable distinct id `ggg-live-canary` so every run groups under one person instead of creating a new one. analytics.Capturer has exactly one method, Capture (internal/analytics/analytics.go:11-13): there is no delete through this seam, and PostHog event deletion is a project-level operation the operator must do by hand. Point this at a throwaway project, not the product's.",
		Asserts: "POST {POSTHOG_HOST}/batch/ — the exact URL posthog-go uploads to (posthog.go:1581 in posthog-go@v1.22.0) — still accepts the batch envelope {api_key, batch:[...]} (message.go:82-89) and answers under 300, which is the whole of posthog-go's own success rule (posthog.go:1599-1603). That acceptance is genuinely all that is available: the SDK reports nothing about what it ingested, and internal/analytics/posthog/posthog_test.go:34 asserts a body substring for the same reason. Construction of the adapter's own Capturer is checked separately so a key/host pair the SDK rejects fails here rather than at boot.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			host := strings.TrimRight(strings.TrimSpace(env.Get("POSTHOG_HOST")), "/")
			if host == "" {
				// The manifest's own default
				// (registry/modules/system/analytics-posthog/module.json:210-218).
				host = "https://us.i.posthog.com"
			}
			endpoint := host + "/batch/"
			message, err := json.Marshal(map[string]any{
				"type":        "capture",
				"event":       "ggg_live_canary",
				"distinct_id": liveCanaryStableID,
				"timestamp":   time.Now().UTC().Format(time.RFC3339),
				"properties":  map[string]any{"$lib": "ggg-live-canary"},
			})
			if err != nil {
				return endpoint, err
			}
			body, err := json.Marshal(map[string]any{
				"api_key": env.Get("POSTHOG_API_KEY"),
				"batch":   []json.RawMessage{message},
			})
			if err != nil {
				return endpoint, err
			}
			if err := liveCanaryJSON(ctx, http.MethodPost, endpoint, map[string]string{
				"Content-Type": "application/json",
			}, body, nil); err != nil {
				return endpoint, fmt.Errorf("capture (POST /batch/): %w", err)
			}
			capturer, err := posthogadapter.New(env.Get("POSTHOG_API_KEY"), host)
			if err != nil {
				return endpoint, fmt.Errorf("construct the PostHog capturer: %w", err)
			}
			capturer.Close()
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/observability", Module: "ggg/system/observability-sentry", Target: "sentry",
		Keys:    []liveCanaryKey{{Env: "SENTRY_DSN", Required: true, Credential: true}},
		Cleanup: liveCanaryLeavesImmutable,
		Leaves:  "one immutable error event per run in the DSN's project, titled `ggg live canary`. observability.Reporter has only Capture and CaptureRequest (internal/observability/observability.go:13): there is no delete through this seam, and deleting a Sentry issue is a console act. Every run adds to the project's event quota, so the DSN must belong to a throwaway project — a canary pointed at the product's project pollutes the issue stream and the alert rules that watch it.",
		Asserts: "the ingestion contract sentry-go's transport depends on: the envelope endpoint the SDK derives from a DSN, {scheme}://{host}[:port][path]/api/{project}/envelope/ (sentry-go@v0.48.0 internal/protocol/dsn.go:212-224), still accepts a three-line envelope authenticated by `X-Sentry-Auth: Sentry sentry_version=7, ..., sentry_key=<public key>` (dsn.go:232-238) and still answers with a JSON object carrying a non-empty `id`. internal/observability/sentryadapter/sentry_test.go:36 asserts only a body substring against a local listener, so the path and the auth header have no coverage at all today. Adapter construction from the same DSN is checked too, which is what proves the DSN still parses.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			dsn := strings.TrimSpace(env.Get("SENTRY_DSN"))
			endpoint, publicKey, err := liveCanarySentryEnvelopeURL(dsn)
			if err != nil {
				return "", err
			}
			if _, err := sentryadapter.NewModule(ctx, nil, sentryadapter.Deps{DSN: dsn, Environment: liveCanaryStableID}); err != nil {
				return endpoint, fmt.Errorf("construct the Sentry reporter from the DSN: %w", err)
			}
			eventID := liveCanaryNonce() + liveCanaryNonce()
			now := time.Now().UTC()
			event, err := json.Marshal(map[string]any{
				"event_id":    eventID,
				"timestamp":   now.Format(time.RFC3339),
				"platform":    "go",
				"level":       "error",
				"environment": liveCanaryStableID,
				"exception": map[string]any{"values": []any{map[string]any{
					"type":  "liveCanary",
					"value": "ggg live canary",
				}}},
			})
			if err != nil {
				return endpoint, err
			}
			var envelope bytes.Buffer
			header, err := json.Marshal(map[string]any{"event_id": eventID, "sent_at": now.Format(time.RFC3339)})
			if err != nil {
				return endpoint, err
			}
			envelope.Write(header)
			envelope.WriteString("\n")
			item, err := json.Marshal(map[string]any{"type": "event", "content_type": "application/json", "length": len(event)})
			if err != nil {
				return endpoint, err
			}
			envelope.Write(item)
			envelope.WriteString("\n")
			envelope.Write(event)
			envelope.WriteString("\n")

			var out struct {
				ID string `json:"id"`
			}
			if err := liveCanaryJSON(ctx, http.MethodPost, endpoint, map[string]string{
				"Content-Type": "application/x-sentry-envelope",
				"X-Sentry-Auth": fmt.Sprintf("Sentry sentry_version=7, sentry_timestamp=%d, sentry_client=ggg-live-canary/1, sentry_key=%s",
					now.Unix(), publicKey),
			}, envelope.Bytes(), &out); err != nil {
				return endpoint, fmt.Errorf("ingest the envelope (POST the envelope endpoint): %w", err)
			}
			if out.ID == "" {
				return endpoint, fmt.Errorf("ingest the envelope: the response carried no `id`, so nothing confirms the event was accepted")
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/llm", Module: "ggg/system/llm-openai-compatible", Target: "openai",
		Keys: []liveCanaryKey{
			{Env: "LLM_API_KEY", Required: true, Credential: true},
			{Env: "LLM_MODEL", Required: true},
			{Env: "LLM_BASE_URL"},
		},
		Cleanup: liveCanaryCleans,
		Leaves:  "no addressable resource — but real money. One completion of at most 5 output tokens per run, which appears in the account's usage and billing records and cannot be removed. This is the only row whose cost is monetary rather than stored state, and the llm seam has no delete because there is nothing to delete; keep LLM_MODEL cheap.",
		Asserts: "the full request/response contract internal/llm/openai/openai_test.go:13-31 fakes, end to end through the adapter: POST {LLM_BASE_URL|https://api.openai.com/v1}/chat/completions with `Authorization: Bearer` and {model, messages, max_tokens, temperature} (openai.go:53-62) still answers with {model, choices[0].message.content, usage.prompt_tokens, usage.completion_tokens} (:38-47) — and the probe asserts all four fields arrive populated, so a provider that stopped returning usage, or returned an empty choices[], fails here.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			base := strings.TrimRight(strings.TrimSpace(env.Get("LLM_BASE_URL")), "/")
			if base == "" {
				base = "https://api.openai.com/v1"
			}
			endpoint := base + "/chat/completions"
			client, err := openai.New(base, env.Get("LLM_API_KEY"), env.Get("LLM_MODEL"))
			if err != nil {
				return endpoint, fmt.Errorf("construct the completion client: %w", err)
			}
			out, err := client.Chat(ctx, llm.ChatRequest{
				Messages:  []llm.Message{{Role: "user", Content: "Reply with the single word: canary."}},
				MaxTokens: 5,
			})
			if err != nil {
				return endpoint, fmt.Errorf("chat (POST /chat/completions): %w", err)
			}
			if out.Content == "" {
				return endpoint, fmt.Errorf("chat: choices[0].message.content was empty; openai.go:79-82 refuses an empty choices[] but not an empty content")
			}
			if out.Model == "" {
				return endpoint, fmt.Errorf("chat: the response carried no `model`; openai.go:39 reads it and internal/llm/openai/openai_test.go:31 believes it is always present")
			}
			if out.PromptTokens <= 0 {
				return endpoint, fmt.Errorf("chat: usage.prompt_tokens was %d; the usage block openai.go:43-46 reads has changed shape", out.PromptTokens)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/cache", Module: "ggg/system/cache-redis", Target: "upstash",
		Keys: []liveCanaryKey{
			{Env: "CACHE_REDIS_URL", Required: true},
			{Env: "CACHE_REDIS_TOKEN", Required: true, Credential: true},
		},
		Cleanup: liveCanaryCleans,
		Leaves:  "nothing. The probe Sets one key under the stable `ggg-live-canary` prefix with a 60-second TTL and Deletes it through cache.Store.Delete (internal/cache/cache.go:13, internal/cache/redis/redis.go:108-113), then proves the read misses. A failed run leaves one key that expires within a minute on its own.",
		Asserts: "the Upstash REST dialect this adapter speaks instead of RESP — which has NO coverage today (internal/cache/redis/redis_test.go:7 asserts a nil client only). POST {CACHE_REDIS_URL} with `Authorization: Bearer` and the body {\"command\":[...]} (redis.go:35-41) must still answer {\"result\":...} (:54-60) for SET/PX, GET and DEL, and a missing key must still come back as a null result rather than an error (:67-69), because that null is what the seam reports as a miss.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			endpoint := env.Get("CACHE_REDIS_URL")
			store, err := cacheredis.New(&cacheredis.RESTClient{Endpoint: endpoint, Token: env.Get("CACHE_REDIS_TOKEN")})
			if err != nil {
				return endpoint, fmt.Errorf("construct the cache store: %w", err)
			}
			key := liveCanaryStableID + ":" + liveCanaryNonce()
			want := []byte("ggg live canary")
			if err := store.Set(ctx, key, want, time.Minute); err != nil {
				return endpoint, fmt.Errorf("set (POST {\"command\":[\"SET\",...]}): %w", err)
			}
			defer func() { _ = store.Delete(context.WithoutCancel(ctx), key) }()
			got, found, err := store.Get(ctx, key)
			if err != nil {
				return endpoint, fmt.Errorf("get (POST {\"command\":[\"GET\",...]}): %w", err)
			}
			if !found || !bytes.Equal(got, want) {
				return endpoint, fmt.Errorf("get: found=%v value=%q, want true and %q", found, got, want)
			}
			if err := store.Delete(ctx, key); err != nil {
				return endpoint, fmt.Errorf("delete (POST {\"command\":[\"DEL\",...]}): %w", err)
			}
			if _, found, err := store.Get(ctx, key); err != nil {
				return endpoint, fmt.Errorf("get after delete: %w", err)
			} else if found {
				return endpoint, fmt.Errorf("get after delete: the key survived Delete")
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/rate-limit", Module: "ggg/system/rate-limit-redis", Target: "upstash",
		Keys: []liveCanaryKey{
			{Env: "RATE_LIMIT_REDIS_URL", Required: true},
			{Env: "RATE_LIMIT_REDIS_TOKEN", Required: true, Credential: true},
		},
		Cleanup: liveCanaryLeavesBounded,
		Leaves:  "one counter key named `ggg-live-canary`, incremented once per run. ratelimit.Limiter has exactly one method, Allow (internal/ratelimit/contract.go:18-20): there is no delete through this seam. The key is bounded twice over — it is stable rather than per-run, and the adapter sets EXPIRE in the same pipeline as the INCR (internal/ratelimit/redis/redis.go:28), so it disappears within the window on its own.",
		Asserts: "the SECOND, different Upstash REST body shape in this repository, which also has no coverage today (internal/ratelimit/redis/redis_test.go:7 asserts a nil client only): the rate limiter posts a pipeline ARRAY, [[\"INCR\",key],[\"EXPIRE\",key,seconds]] (redis.go:28-29), not the cache's {\"command\":[...]} object, and reads {\"result\":[...]} as a list whose first element is the count (:48-61). The probe asserts the decision comes back allowed with the configured limit and a remaining count inside it, which is what proves the list-shaped result still decodes.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			endpoint := env.Get("RATE_LIMIT_REDIS_URL")
			const limit = 100
			limiter, err := ratelimitredis.New(&ratelimitredis.RESTClient{Endpoint: endpoint, Token: env.Get("RATE_LIMIT_REDIS_TOKEN")}, limit)
			if err != nil {
				return endpoint, fmt.Errorf("construct the limiter: %w", err)
			}
			decision, err := limiter.Allow(ctx, liveCanaryStableID)
			if err != nil {
				return endpoint, fmt.Errorf("allow (POST the INCR/EXPIRE pipeline): %w", err)
			}
			if !decision.Allowed {
				return endpoint, fmt.Errorf("allow: the decision was denied on a %d/minute budget; the stable canary key has not expired, or the result list no longer decodes", limit)
			}
			if decision.Limit != limit {
				return endpoint, fmt.Errorf("allow: the decision reported limit %d, want %d", decision.Limit, limit)
			}
			if decision.Remaining < 0 || decision.Remaining >= limit {
				return endpoint, fmt.Errorf("allow: the decision reported %d remaining of %d, which is outside the budget; result[0] no longer carries the count (internal/ratelimit/redis/redis.go:57-60)", decision.Remaining, limit)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/search", Module: "ggg/system/search-typesense", Target: "typesense",
		Keys: []liveCanaryKey{
			{Env: "TYPESENSE_URL", Required: true},
			{Env: "TYPESENSE_API_KEY", Required: true, Credential: true},
			{Env: "GGG_CANARY_TYPESENSE_COLLECTION", Required: true, Harness: true},
		},
		Cleanup: liveCanaryCleans,
		Leaves:  "nothing. The probe Upserts one document and Deletes it through search.Index.Delete (internal/search/search.go:8, internal/search/typesense/typesense.go:73-89), then proves the query no longer returns it. The COLLECTION itself is operator-owned and is never created or dropped by the probe: GGG_CANARY_TYPESENSE_COLLECTION must already exist with the fields the adapter writes — id, tenant_id, collection, text and fields (typesense.go:70).",
		Asserts: "all four Typesense HTTP shapes the adapter depends on, none of which has any coverage today (internal/search/typesense/typesense_test.go:7 asserts an empty endpoint only): POST /collections/{c}/documents?action=upsert with `X-TYPESENSE-API-KEY` (typesense.go:52,:71); GET /collections/{c}/documents/search with query_by/per_page/filter_by=tenant_id:=... answering {hits:[{document:{id,tenant_id,fields},text_match}],next_page} (:99-128); GET /collections/{c}/documents/{id} answering a document carrying tenant_id, which is the read the adapter uses as its tenant-ownership check before deleting (:83-88); and DELETE of the same path (:89).",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			collection := env.Get("GGG_CANARY_TYPESENSE_COLLECTION")
			base := strings.TrimRight(env.Get("TYPESENSE_URL"), "/")
			endpoint := base + "/collections/" + collection
			index := typesense.New(base, env.Get("TYPESENSE_API_KEY"))
			id := liveCanaryStableID + "-" + liveCanaryNonce()
			document := search.Document{
				TenantID:   liveCanaryStableID,
				Collection: collection,
				ID:         id,
				Text:       "ggg live canary " + id,
				Fields:     map[string]string{"kind": liveCanaryStableID},
			}
			if err := index.Upsert(ctx, document); err != nil {
				return endpoint, fmt.Errorf("upsert (POST /documents?action=upsert): %w", err)
			}
			defer func() { _ = index.Delete(context.WithoutCancel(ctx), liveCanaryStableID, collection, id) }()
			result, err := index.Query(ctx, search.Query{
				TenantID:   liveCanaryStableID,
				Collection: collection,
				Text:       id,
				Limit:      5,
			})
			if err != nil {
				return endpoint, fmt.Errorf("query (GET /documents/search): %w", err)
			}
			if !liveCanaryHasHit(result, id) {
				return endpoint, fmt.Errorf("query: the upserted document %q was not among %d hit(s); the hits/document/tenant_id shape internal/search/typesense/typesense.go:115-137 decodes has changed, or the collection is not indexing `text`", id, len(result.Hits))
			}
			if err := index.Delete(ctx, liveCanaryStableID, collection, id); err != nil {
				return endpoint, fmt.Errorf("delete (GET then DELETE /documents/{id}): %w", err)
			}
			after, err := index.Query(ctx, search.Query{TenantID: liveCanaryStableID, Collection: collection, Text: id, Limit: 5})
			if err != nil {
				return endpoint, fmt.Errorf("query after delete: %w", err)
			}
			if liveCanaryHasHit(after, id) {
				return endpoint, fmt.Errorf("query after delete: %q survived Delete", id)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/realtime", Module: "ggg/system/realtime-ably", Target: "ably",
		Keys: []liveCanaryKey{
			{Env: "ABLY_ENDPOINT", Required: true},
			{Env: "ABLY_API_KEY", Required: true, Credential: true},
		},
		Cleanup: liveCanaryLeavesImmutable,
		Leaves:  "one message per run on the channel `ggg-live-canary`, plus one message against the account's monthly message quota. realtime.Broker has only Publish and Subscribe (internal/realtime/realtime.go:13-16): there is no delete, and if channel persistence is enabled on the account the message stays in history until Ably's retention expires it. Keep the canary channel out of any channel rule that persists.",
		Asserts: "that POST {ABLY_ENDPOINT}/channels/{topic}/messages with `Authorization: Basic <key>` is still accepted (internal/realtime/ably/ably.go:37-51) — and that IS all this row can assert, honestly. The seam's only read path is Subscribe, which this adapter refuses outright because it needs a websocket transport (ably.go:20-22), so there is no read-back and no payload assertion available; a 2xx is the entire observation. internal/realtime/ably/ably_test.go:8,:17 assert construction only, so even the acceptance is new coverage.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			base := strings.TrimRight(env.Get("ABLY_ENDPOINT"), "/")
			endpoint := base + "/channels/" + liveCanaryStableID + "/messages"
			broker := ably.New(base, env.Get("ABLY_API_KEY"), nil)
			payload, err := json.Marshal(map[string]any{"name": liveCanaryStableID, "data": "ggg live canary"})
			if err != nil {
				return endpoint, err
			}
			if err := broker.Publish(ctx, liveCanaryStableID, payload); err != nil {
				return endpoint, fmt.Errorf("publish (POST /channels/%s/messages): %w", liveCanaryStableID, err)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/telemetry", Module: "ggg/system/telemetry-otlp", Target: "otlp",
		Keys: []liveCanaryKey{
			{Env: "OTLP_ENDPOINT", Required: true},
			{Env: "OTLP_API_KEY", Credential: true},
		},
		Cleanup: liveCanaryLeavesImmutable,
		Leaves:  "one exported span named `ggg-live-canary` per run. telemetry.Provider hands out OpenTelemetry providers (internal/telemetry/telemetry.go:16) and there is no delete anywhere on that seam; the span lives in the collector's backend under its retention. A collector that fans out to a paid backend is billing for every run.",
		Asserts: "that the OTLP/HTTP trace endpoint still accepts what this adapter actually sends: POST {OTLP_ENDPOINT}/v1/traces with `Content-Type: application/x-protobuf` carrying a hand-marshalled ExportTraceServiceRequest (internal/telemetry/otlp/exporter.go:48-52,:60-74). The probe drives it through the real TracerProvider and asserts ForceFlush returns no error, so a rejected export is a failure rather than a swallowed batch. NOTE, and this is the finding rather than the assertion: the exporter sets NO authorization header (exporter.go:65,:85), so OTLP_API_KEY is declared, parsed into config and never sent — this row can therefore only run against an endpoint that accepts unauthenticated OTLP.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			base := strings.TrimRight(strings.TrimSpace(env.Get("OTLP_ENDPOINT")), "/")
			endpoint := base + "/v1/traces"
			provider, err := telemetryotlp.New(base)
			if err != nil {
				return endpoint, fmt.Errorf("construct the OTLP provider: %w", err)
			}
			defer func() { _ = provider.Shutdown(context.WithoutCancel(ctx)) }()
			if err := provider.Health(ctx); err != nil {
				return endpoint, fmt.Errorf("health (GET the endpoint): %w", err)
			}
			_, span := provider.Tracer.Tracer(liveCanaryStableID).Start(ctx, liveCanaryStableID)
			span.End()
			if err := provider.Tracer.ForceFlush(ctx); err != nil {
				return endpoint, fmt.Errorf("export the span (POST /v1/traces, application/x-protobuf): %w", err)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/audit-export", Module: "ggg/system/audit-export-otlp", Target: "otlp",
		Keys:    []liveCanaryKey{{Env: "OTLP_AUDIT_EXPORT_URL", Required: true, Credential: true}},
		Cleanup: liveCanaryLeavesImmutable,
		Leaves:  "one exported audit entry per run, with action `ggg-live-canary` and no real org or user. audit.Exporter has only Export (internal/audit/export.go:9-11) and there is no delete; the entry is off-box the moment it is accepted. The transactional Postgres audit row is untouched — the exporter runs after the ledger insert and can never replace it (internal/audit/export.go:12).",
		Asserts: "that POST {OTLP_AUDIT_EXPORT_URL} with `Content-Type: application/json` carrying a marshalled audit.Entry — {ID, OrgID, UserID, Action, Metadata} — is still accepted (internal/audit_export/otlp/otlp.go:27-44). Acceptance is all the seam exposes: Export returns only an error. internal/audit_export/otlp/otlp_test.go:8 asserts an empty endpoint refusal and nothing else, so this is the first check of the real sink. The URL itself is treated as a credential here even though the manifest does not mark it secret (registry/modules/system/audit-export-otlp/module.json:86-96): a collector URL with an embedded token is the normal case and a canary must not print it.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			endpoint := strings.TrimRight(env.Get("OTLP_AUDIT_EXPORT_URL"), "/")
			exporter := auditotlp.New(endpoint)
			if err := exporter.Export(ctx, audit.Entry{
				ID:       liveCanaryNonce(),
				OrgID:    liveCanaryStableID,
				UserID:   liveCanaryStableID,
				Action:   liveCanaryStableID,
				Metadata: map[string]any{"source": "live-canary"},
			}); err != nil {
				return endpoint, fmt.Errorf("export (POST the audit sink): %w", err)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/database", Module: "ggg/system/database-postgres", Target: "neon",
		Keys: []liveCanaryKey{
			{Env: "NEON_API_KEY", Required: true, Credential: true},
			{Env: "NEON_PROJECT_ID", Required: true},
		},
		Cleanup: liveCanaryCleans,
		Leaves:  "nothing. The probe calls the provisioner's Check, which is a read: GET /projects/{id} (internal/provision/neon/neon.go:356-365). Plan and Apply — which would create a project and a branch — are deliberately NOT called, because a canary must never provision a database. Nothing is written, so there is nothing to clean.",
		Asserts: "the Neon v2 control-plane shape the provisioner reads: GET {NEON_API_BASE_URL|https://api.neon.tech/v2}/projects/{id} with a bearer key still answers a body whose `project.id` echoes the request (neon.go:281-312,:356-365), which is what makes Check the authoritative observation the deploy/provision path trusts. The whole internal/provision/neon package has no live coverage otherwise. The probe does not exercise branch reads: a branch id only exists in prior provisioned state, and inventing one would assert nothing.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			project := env.Get("NEON_PROJECT_ID")
			endpoint := neon.DefaultBaseURL + "/projects/" + project
			key := env.Get("NEON_API_KEY")
			status, err := neon.NewProvisioner().Check(ctx, remote.ProviderRequest{
				Slot:        "ggg/database",
				Environment: "production",
				Adapter:     "ggg/system/database-postgres",
				Target:      "neon",
				Values:      map[string]string{"project_id": project},
				Secrets: remote.SecretValuesFunc(func(name string) (string, bool) {
					if name == neon.APIKeyEnv {
						return key, true
					}
					return "", false
				}),
			})
			if err != nil {
				return endpoint, fmt.Errorf("check (GET /projects/%s): %w", project, err)
			}
			if status.State != "ready" || !status.Healthy {
				return endpoint, fmt.Errorf("check: the project reported state %q healthy=%v (%s), want a ready, healthy project", status.State, status.Healthy, status.Message)
			}
			if status.ObservedStateHash == "" {
				return endpoint, fmt.Errorf("check: no observed state hash was produced, so the remote-plan staleness comparison internal/provision/neon/neon.go:258 feeds has nothing to compare")
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/notifications", Module: "ggg/system/notifications-knock", Target: "knock",
		Keys: []liveCanaryKey{
			{Env: "KNOCK_API_KEY", Required: true, Credential: true},
			{Env: "GGG_CANARY_KNOCK_WORKFLOW", Required: true, Harness: true},
			{Env: "GGG_CANARY_KNOCK_RECIPIENT", Required: true, Harness: true},
		},
		Cleanup: liveCanaryLeavesImmutable,
		Leaves:  "one real workflow run per canary run, delivered to GGG_CANARY_KNOCK_RECIPIENT through whatever channels the operator's canary workflow enables, plus the inline-identified tenant `ggg-live-canary` on first use. notifications.Notifier declares only Send and SendOrg (internal/notifications/notifications.go:6-9), so there is no cancel or delete through this seam, and the adapter sends no cancellation_key (internal/notifications/knock/knock.go:221-232): GGG_CANARY_KNOCK_RECIPIENT must be a throwaway recipient the operator owns, never a customer. Each run also counts against the account's notification quota.",
		Asserts: "POST https://api.knock.app/v1/workflows/{key}/trigger still accepts the exact body internal/notifications/knock/knock.go:221-232 sends ({recipients:[id], tenant, data{kind,title,body,url}}) under a bearer key, and still answers a JSON object carrying a non-empty `workflow_run_id` — the two halves internal/notifications/knock/knock_test.go:108 pins against an httptest fake. The probe drives the adapter's own Client.Trigger (knock.go:211), which is what production wiring installs as the delivery function (knock.go:351), so the workflow key is the message Kind here exactly as it is there, and a 2xx carrying no run id fails here exactly as it does there (knock.go:260-262).",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			workflow := env.Get("GGG_CANARY_KNOCK_WORKFLOW")
			endpoint := "https://api.knock.app/v1/workflows/" + workflow + "/trigger"
			client, err := knockadapter.NewClient(env.Get("KNOCK_API_KEY"), "")
			if err != nil {
				return endpoint, fmt.Errorf("construct the Knock client: %w", err)
			}
			if err := client.Trigger(ctx, notifications.Message{
				OrgID:  liveCanaryStableID,
				UserID: env.Get("GGG_CANARY_KNOCK_RECIPIENT"),
				Kind:   workflow,
				Title:  "ggg live canary",
				Body:   "ggg live canary " + time.Now().UTC().Format(time.RFC3339),
				URL:    "/app",
			}); err != nil {
				return endpoint, fmt.Errorf("trigger (POST /v1/workflows/{key}/trigger): %w", err)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/webhooks", Module: "ggg/system/webhooks-svix", Target: "svix",
		Keys: []liveCanaryKey{
			{Env: "SVIX_API_KEY", Required: true, Credential: true},
			{Env: "GGG_CANARY_SVIX_APP", Required: true, Harness: true},
		},
		Cleanup: liveCanaryLeavesImmutable,
		Leaves:  "one real Svix message per canary run on the GGG_CANARY_SVIX_APP consumer application, fanned out to every endpoint that application has subscribed, and retained for the account's payload-retention window. webhooks.Emitter declares only Emit (internal/webhooks/contract.go:5-7), so there is no delete through this seam and the adapter sends no eventId to collapse repeats (internal/webhooks/svix/svix.go:199-206): GGG_CANARY_SVIX_APP must be a throwaway application whose endpoints the operator owns, never a customer's.",
		Asserts: "POST https://api.svix.com/api/v1/app/{app_id}/msg/ still accepts the exact body internal/webhooks/svix/svix.go:199-206 sends ({eventType, payload{type,occurred_at,data}}) under a bearer key, and still answers a message object carrying a non-empty `id` — the two halves internal/webhooks/svix/svix_test.go:118 pins against an httptest fake. It also pins the identifier mapping the seam depends on: the organization id is the Svix application id (svix.go:185,:210), which is what keeps Svix identifiers out of this project's tables, and the event type is one the product actually emits (internal/webhooks/webhooks.go:22-27).",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			app := env.Get("GGG_CANARY_SVIX_APP")
			endpoint := "https://api.svix.com/api/v1/app/" + app + "/msg/"
			if len(webhooks.EventTypes) == 0 {
				return endpoint, fmt.Errorf("internal/webhooks/webhooks.go declares no event types, so there is no type the product emits to canary with")
			}
			client, err := svixadapter.NewClient(env.Get("SVIX_API_KEY"), "", time.Now)
			if err != nil {
				return endpoint, fmt.Errorf("construct the Svix client: %w", err)
			}
			if err := client.Send(ctx, app, webhooks.EventTypes[0], map[string]any{
				"id":     liveCanaryStableID,
				"source": "live-canary",
				"at":     time.Now().UTC().Format(time.RFC3339),
			}); err != nil {
				return endpoint, fmt.Errorf("create message (POST /api/v1/app/{app_id}/msg/): %w", err)
			}
			return endpoint, nil
		},
	},
	{
		Slot: "ggg/usage", Module: "ggg/system/usage-openmeter", Target: "openmeter",
		Keys: []liveCanaryKey{
			{Env: "OPENMETER_API_KEY", Required: true, Credential: true},
			{Env: "OPENMETER_URL", Required: false},
			{Env: "GGG_CANARY_OPENMETER_SUBJECT", Required: true, Harness: true},
		},
		Cleanup: liveCanaryLeavesBounded,
		Leaves:  "one usage event on the GGG_CANARY_OPENMETER_SUBJECT subject, and only ever one: the probe ingests with the stable external id `ggg-live-canary`, which the adapter carries as the CloudEvents id (internal/usage/openmeter/openmeter.go:200-206), and OpenMeter deduplicates on (source, id), so every later run collapses onto that same event instead of accumulating. usage.Recorder declares only Record (internal/usage/contract.go:5-7), so there is no delete through this seam; the subject must be a throwaway customer the operator owns, because a metered event on a real one reaches an invoice.",
		Asserts: "POST {OPENMETER_URL|https://openmeter.cloud}/api/v1/events still accepts the CloudEvents 1.0 JSON body internal/usage/openmeter/openmeter.go:195-215 sends ({specversion,type,id,time,source,subject,data{value,...metadata}}) under Content-Type: application/cloudevents+json and a bearer key — the shape internal/usage/openmeter/openmeter_test.go:123 pins against an httptest fake. Acceptance is all this endpoint offers: it answers 204 with no body, so the adapter treats the status as the whole result (openmeter.go:231-236) and the probe asserts exactly that and the deduplicating id, nothing more. An event whose type matches no meter is still accepted; OpenMeter surfaces those as ingestion warnings rather than refusals.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			base := env.Get("OPENMETER_URL")
			if base == "" {
				base = "https://openmeter.cloud"
			}
			endpoint := strings.TrimRight(base, "/") + "/api/v1/events"
			client, err := openmeteradapter.NewClient(env.Get("OPENMETER_API_KEY"), base, time.Now)
			if err != nil {
				return endpoint, fmt.Errorf("construct the OpenMeter client: %w", err)
			}
			if err := client.Ingest(ctx, env.Get("GGG_CANARY_OPENMETER_SUBJECT"), "ggg_live_canary", 1,
				liveCanaryStableID, map[string]any{"source": "live-canary"}); err != nil {
				return endpoint, fmt.Errorf("ingest (POST /api/v1/events): %w", err)
			}
			return endpoint, nil
		},
	},
}

// liveCanaryEnv is one row's resolved configuration plus the redaction set
// derived from it. Probes read values through it and never touch os.Getenv,
// so nothing can reach a credential the row did not declare.
type liveCanaryEnv struct {
	values map[string]string
	// secrets is every string that must never be rendered, longest first so
	// a full DSN is replaced before the public key inside it.
	secrets []liveCanarySecret
}

type liveCanarySecret struct {
	key, value string
}

func (e liveCanaryEnv) Get(key string) string { return e.values[key] }

// Redact removes every credential value this row carries from text. It is the
// only reason probes return errors instead of failing directly: one reporting
// path can be audited; one per probe cannot.
func (e liveCanaryEnv) Redact(text string) string {
	for _, secret := range e.secrets {
		text = strings.ReplaceAll(text, secret.value, liveCanaryRedaction(secret.key))
	}
	return text
}

// liveCanaryResolve reads a row's declared keys out of the process
// environment and names the required ones that are absent.
func liveCanaryResolve(row liveCanaryProvider, lookup func(string) string) (liveCanaryEnv, []string) {
	env := liveCanaryEnv{values: map[string]string{}}
	var missing []string
	for _, key := range row.Keys {
		value := strings.TrimSpace(lookup(key.Env))
		env.values[key.Env] = value
		if value == "" {
			if key.Required {
				missing = append(missing, key.Env)
			}
			continue
		}
		if !key.Credential {
			continue
		}
		env.secrets = append(env.secrets, liveCanarySecret{key: key.Env, value: value})
		// A credential embedded in a URL's userinfo — a Sentry DSN's public
		// key, a collector URL's token — must go too, because an endpoint
		// derived from the value would otherwise carry it.
		if parsed, err := url.Parse(value); err == nil && parsed.User != nil {
			if user := parsed.User.Username(); len(user) >= 4 {
				env.secrets = append(env.secrets, liveCanarySecret{key: key.Env, value: user})
			}
			if password, ok := parsed.User.Password(); ok && len(password) >= 4 {
				env.secrets = append(env.secrets, liveCanarySecret{key: key.Env, value: password})
			}
		}
	}
	sort.SliceStable(env.secrets, func(i, j int) bool {
		return len(env.secrets[i].value) > len(env.secrets[j].value)
	})
	return env, missing
}

// liveCanaryForbiddenSelector is safety rule 3: a selector that would aim a
// probe at a production tenant is a refusal, not a skip. An operator who set
// POLAR_SERVER=production asked for something this suite will not do, and
// skipping would look like the canary merely had no credentials.
func liveCanaryForbiddenSelector(row liveCanaryProvider, env liveCanaryEnv) error {
	for _, forbid := range row.Forbid {
		got := strings.TrimSpace(env.Get(forbid.Env))
		if got == "" {
			continue
		}
		for _, refuse := range forbid.Values {
			if strings.EqualFold(got, refuse) {
				return fmt.Errorf("%s selects %s=%s and this canary refuses to run against it: %s",
					row.Module, forbid.Env, refuse, forbid.Why)
			}
		}
	}
	return nil
}

// The suite. One row per managed adapter, each skipping itself with its own reason when its
// credentials are absent, each bounded by its own deadline, each reported
// through one redacting path.
func TestManagedTargetLiveCanaries(t *testing.T) {
	if os.Getenv(liveCanaryEnvVar) != "1" {
		t.Skipf("%s the managed-target canaries call the real maintained providers with live credentials (one network round trip per step, real remote state on every row that cannot clean up, and provider spend on the llm-openai-compatible row; each row is bounded at %s, so the suite is bounded at %s); CI's `live-canary` workflow owns it — set GGG_LIVE_CANARY=1 to run it here",
			gggcli.InapplicableSkipMarker, liveCanaryRowDeadline, time.Duration(len(liveCanaryProviders))*liveCanaryRowDeadline)
	}
	for _, row := range liveCanaryProviders {
		t.Run(row.Module, func(t *testing.T) {
			env, missing := liveCanaryResolve(row, os.Getenv)
			if len(missing) > 0 {
				t.Skipf("%s the %s canary (%s@%s, %s) needs live credentials and %s %s unset here (bounded at %s per run); CI's `live-canary` workflow owns it and maps every key from repository secrets — set %s to run it here",
					gggcli.InapplicableSkipMarker, row.Module, row.Slot, row.Target, row.Cleanup,
					strings.Join(missing, ", "), liveCanaryPlural(missing), liveCanaryRowDeadline,
					liveCanaryAssignments(missing))
			}
			if err := liveCanaryForbiddenSelector(row, env); err != nil {
				t.Fatalf("refused before any call: %s", env.Redact(err.Error()))
			}

			ctx, cancel := context.WithTimeout(context.Background(), liveCanaryRowDeadline)
			defer cancel()
			started := time.Now()
			endpoint, err := row.Probe(ctx, env)
			if err != nil {
				// The one reporting path, and the only place a probe's words
				// are rendered. The provider and the endpoint are named
				// because they come from the row and the probe's return, not
				// from the error text; the credential is scrubbed.
				t.Fatalf("%s canary against %s failed after %s: %s",
					row.Module, env.Redact(endpoint), time.Since(started).Round(time.Millisecond), env.Redact(err.Error()))
			}
			t.Logf("%s canary against %s passed in %s (%s)", row.Module, env.Redact(endpoint),
				time.Since(started).Round(time.Millisecond), row.Cleanup)
		})
	}
}

// Every module that publishes a `managed` service target must be canaried or
// explicitly excused, and every row must still describe the registry. This is
// what makes "adding a provider is a row, not a new file" true rather than
// aspirational: a new managed adapter fails here until it is one or the other.
func TestEveryManagedAdapterIsCanariedOrExcused(t *testing.T) {
	root := liveCanaryRepositoryRoot(t)
	managed := liveCanaryManagedAdapters(t, root)
	// The floor. A walk that found no managed adapter has broken, not the
	// registry: this repository ships eighteen.
	if len(managed) < 15 {
		t.Fatalf("only %d managed adapter(s) were found under registry/modules; the walk has collapsed, not the registry", len(managed))
	}

	canaried := map[string]liveCanaryProvider{}
	for _, row := range liveCanaryProviders {
		if previous, duplicate := canaried[row.Module]; duplicate {
			t.Fatalf("%s has two canary rows (targets %s and %s); one row per managed adapter", row.Module, previous.Target, row.Target)
		}
		canaried[row.Module] = row
	}

	var undeclared, both []string
	for id := range managed {
		_, isCanaried := canaried[id]
		_, isExcused := liveCanaryNothingToProbe[id]
		switch {
		case isCanaried && isExcused:
			both = append(both, id)
		case !isCanaried && !isExcused:
			undeclared = append(undeclared, id)
		}
	}
	sort.Strings(undeclared)
	sort.Strings(both)
	if len(undeclared) > 0 {
		t.Fatalf("these modules publish a managed service target and appear in neither liveCanaryProviders nor liveCanaryNothingToProbe: %v.\n"+
			"A managed reference target with no live canary and no stated reason is a wire shape this repository believes and has never checked. Add a row, or excuse it with the file:line that makes the excuse true.", undeclared)
	}
	if len(both) > 0 {
		t.Fatalf("these modules are both canaried and excused from canarying: %v.\nThe excuse is stale — delete the liveCanaryNothingToProbe entry.", both)
	}

	var dead []string
	for id := range canaried {
		if _, ok := managed[id]; !ok {
			dead = append(dead, id+" (liveCanaryProviders row, no managed target in the registry)")
		}
	}
	for id := range liveCanaryNothingToProbe {
		if _, ok := managed[id]; !ok {
			dead = append(dead, id+" (liveCanaryNothingToProbe entry, no managed target in the registry)")
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Fatalf("these canary declarations no longer describe the registry: %v.\nDelete each one; a declaration that outlives its subject is how the next one gets added without argument.", dead)
	}

	// Each row's target must be one the module actually publishes, and each
	// adapter-configuration key must still exist in the owning manifest — a
	// renamed key would otherwise make a canary skip forever while reporting
	// a clean line.
	for _, row := range liveCanaryProviders {
		adapter := managed[row.Module]
		if !adapter.targets[row.Target] {
			t.Fatalf("%s canaries target %q, which %s does not publish; its targets are %v",
				row.Module, row.Target, adapter.path, liveCanarySortedKeys(adapter.targets))
		}
		for _, key := range row.Keys {
			if key.Harness {
				continue
			}
			scope, declared := adapter.environment[key.Env]
			if !declared {
				t.Fatalf("the %s canary reads %s and %s declares no such environment key; the key was renamed and the canary would skip forever",
					row.Module, key.Env, adapter.path)
			}
			// An empty targets list applies to every target of the adapter.
			if len(scope) == 0 || scope[row.Module+"@"+row.Target] {
				continue
			}
			exception := row.Module + "@" + row.Target + ":" + key.Env
			if _, excused := liveCanaryManifestKeyScopeExceptions[exception]; !excused {
				t.Fatalf("%s declares %s but scopes it to %v, which excludes %s@%s, so the generated parser would not enforce it for the target this canary drives. Fix the manifest's targets list, or record the defect in liveCanaryManifestKeyScopeExceptions under %q",
					adapter.path, key.Env, liveCanarySortedKeys(scope), row.Module, row.Target, exception)
			}
		}
	}

	// And the exceptions go stale loudly: a fixed manifest fails here.
	var fixed []string
	for exception := range liveCanaryManifestKeyScopeExceptions {
		module, target, key, ok := liveCanarySplitException(exception)
		if !ok {
			t.Fatalf("liveCanaryManifestKeyScopeExceptions key %q is not `<module>@<target>:<KEY>`", exception)
		}
		adapter, known := managed[module]
		if !known {
			fixed = append(fixed, exception+" (no such managed adapter)")
			continue
		}
		scope, declared := adapter.environment[key]
		if !declared {
			fixed = append(fixed, exception+" (the manifest no longer declares the key)")
			continue
		}
		if len(scope) == 0 || scope[module+"@"+target] {
			fixed = append(fixed, exception+" (the manifest now scopes the key to this target)")
		}
	}
	sort.Strings(fixed)
	if len(fixed) > 0 {
		t.Fatalf("these liveCanaryManifestKeyScopeExceptions rows describe manifest defects that no longer exist: %v.\nDelete each one.", fixed)
	}
}

// Every row must state its safety claim in full. A row with no Leaves prose
// is a row whose operator cannot be told what the canary will cost them, and
// a row with no Asserts prose is a probe nobody can review for the "returned
// 200 and therefore nothing" failure this suite exists to avoid.
func TestEveryLiveCanaryRowStatesItsSafetyAndItsAssertion(t *testing.T) {
	for _, row := range liveCanaryProviders {
		t.Run(row.Module, func(t *testing.T) {
			if row.Slot == "" || row.Target == "" || row.Probe == nil {
				t.Fatalf("%s is not a complete row: slot=%q target=%q probe=%v", row.Module, row.Slot, row.Target, row.Probe != nil)
			}
			if len(row.Asserts) < 80 {
				t.Fatalf("%s states its assertion in %d character(s); say which wire shape the probe pins, in the terms its fake states it, or say plainly that acceptance is all that is available", row.Module, len(row.Asserts))
			}
			if len(row.Leaves) < 40 {
				t.Fatalf("%s states what it leaves behind in %d character(s); the operator paying for the account reads this", row.Module, len(row.Leaves))
			}
			var required, credentials int
			for _, key := range row.Keys {
				if key.Required {
					required++
				}
				if key.Credential {
					credentials++
				}
			}
			if required == 0 {
				t.Fatalf("%s declares no required key, so it can never skip for absent credentials and would run against nothing", row.Module)
			}
			if credentials == 0 {
				t.Fatalf("%s declares no credential key, so its failures would be redacted of nothing; a live canary that needs no credential is not one", row.Module)
			}
			// A row that cannot clean up must cite the seam declaration that
			// lacks the delete, so the claim is checkable rather than
			// asserted. A `.go:` citation is the repository's evidence
			// convention and the one thing a reviewer can follow.
			if row.Cleanup != liveCanaryCleans && !strings.Contains(row.Leaves, ".go:") {
				t.Fatalf("%s cannot clean up and its Leaves prose cites no file:line for the seam that lacks the delete; name the interface and where it is declared", row.Module)
			}
		})
	}
}

// The production refusal, driven. POLAR_SERVER=production must refuse before
// any call; sandbox and unset must not.
func TestLiveCanaryRefusesAProductionProviderSelector(t *testing.T) {
	var polarRow liveCanaryProvider
	for _, row := range liveCanaryProviders {
		if row.Module == "ggg/system/billing-polar" {
			polarRow = row
		}
	}
	if len(polarRow.Forbid) == 0 {
		t.Fatal("the billing-polar row declares no forbidden selector, so a canary could create checkout sessions and ingest immutable metered events in a production Polar tenant")
	}

	for _, testCase := range []struct {
		name, server string
		refuse       bool
	}{
		{name: "production refuses", server: "production", refuse: true},
		{name: "PRODUCTION refuses case-insensitively", server: "PRODUCTION", refuse: true},
		{name: "sandbox runs", server: "sandbox"},
		{name: "unset runs", server: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env, _ := liveCanaryResolve(polarRow, func(key string) string {
				switch key {
				case "POLAR_SERVER":
					return testCase.server
				case "POLAR_ACCESS_TOKEN":
					return "polar_oat_canary_unit_test_token"
				case "POLAR_PRODUCT_PRO":
					return "prod_canary"
				}
				return ""
			})
			err := liveCanaryForbiddenSelector(polarRow, env)
			if testCase.refuse {
				if err == nil {
					t.Fatalf("POLAR_SERVER=%q was accepted; the canary would create immutable metered events in a production tenant", testCase.server)
				}
				if !strings.Contains(err.Error(), "POLAR_SERVER") || !strings.Contains(err.Error(), "refuses") {
					t.Fatalf("the refusal does not name the selector and the refusal: %q", err)
				}
				if strings.Contains(err.Error(), "polar_oat_canary_unit_test_token") {
					t.Fatalf("the refusal leaked the access token: %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("POLAR_SERVER=%q was refused: %v", testCase.server, err)
			}
		})
	}
}

// Redaction, driven. Every credential value a row declares — and any userinfo
// embedded in one — must be gone from anything the suite renders, while the
// provider and the endpoint survive.
func TestLiveCanaryRedactsEveryCredentialValueFromFailures(t *testing.T) {
	t.Run("a declared credential never survives rendering", func(t *testing.T) {
		for _, row := range liveCanaryProviders {
			values := map[string]string{}
			var credentials []string
			for index, key := range row.Keys {
				value := fmt.Sprintf("canary-secret-%s-%d-vXwYz", strings.ToLower(key.Env), index)
				values[key.Env] = value
				if key.Credential {
					credentials = append(credentials, value)
				}
			}
			env, _ := liveCanaryResolve(row, func(key string) string { return values[key] })
			// The shape a provider error takes: the endpoint, the status, and
			// the upstream body, which is exactly where a credential gets
			// echoed back at you.
			raw := fmt.Sprintf("post https://provider.example/v1/thing: 401: {\"error\":\"invalid key %s\"}",
				strings.Join(credentials, " and "))
			got := env.Redact(raw)
			for _, credential := range credentials {
				if strings.Contains(got, credential) {
					t.Fatalf("%s: the rendered failure still carries a credential value: %q", row.Module, got)
				}
			}
			if !strings.Contains(got, "https://provider.example/v1/thing") || !strings.Contains(got, "401") {
				t.Fatalf("%s: redaction ate the endpoint or the status, which are what make a failure actionable: %q", row.Module, got)
			}
		}
	})

	t.Run("userinfo inside a credential goes too", func(t *testing.T) {
		var sentryRow liveCanaryProvider
		for _, row := range liveCanaryProviders {
			if row.Module == "ggg/system/observability-sentry" {
				sentryRow = row
			}
		}
		const publicKey = "abc123def456abc123def456abc123de"
		dsn := "https://" + publicKey + "@o4507.ingest.sentry.io/9"
		env, missing := liveCanaryResolve(sentryRow, func(key string) string {
			if key == "SENTRY_DSN" {
				return dsn
			}
			return ""
		})
		if len(missing) > 0 {
			t.Fatalf("the sentry row reported %v missing with a DSN set", missing)
		}
		endpoint, gotKey, err := liveCanarySentryEnvelopeURL(dsn)
		if err != nil {
			t.Fatalf("derive the envelope URL: %v", err)
		}
		if gotKey != publicKey {
			t.Fatalf("the envelope auth read the public key as %q, want %q", gotKey, publicKey)
		}
		// The endpoint is derived from the DSN and must be printable: it
		// carries the host and the project, never the key.
		if strings.Contains(endpoint, publicKey) {
			t.Fatalf("the derived envelope endpoint carries the DSN's public key: %q", endpoint)
		}
		if env.Redact(endpoint) != endpoint {
			t.Fatalf("the derived envelope endpoint was itself redacted, so a failure would name no endpoint at all: %q", env.Redact(endpoint))
		}
		rendered := env.Redact("ingest the envelope: 401 with " + dsn + " and key " + publicKey)
		if strings.Contains(rendered, publicKey) || strings.Contains(rendered, dsn) {
			t.Fatalf("the rendered failure still carries the DSN or its public key: %q", rendered)
		}
	})
}

// liveCanaryWorkflowPath is the only thing that sets GGG_LIVE_CANARY.
const liveCanaryWorkflowPath = ".github/workflows/live-canary.yml"

// liveCanaryPinnedWorkflowValues are the keys the workflow sets to a literal
// rather than mapping from a repository secret, with the reason. A literal is
// the stronger choice for a selector whose wrong value the suite refuses: a
// misconfigured repository secret cannot even present the refusal.
var liveCanaryPinnedWorkflowValues = map[string]string{
	"POLAR_SERVER": "pinned to `sandbox` because the probe creates a checkout session and ingests an immutable metered event; the suite refuses `production` and the pin means a wrong secret cannot reach the refusal",
}

// Every key a row declares must be mapped in the workflow that runs the
// suite. Without this, adding a row is half a change: the row exists, the CI
// job cannot supply it, and the canary reports a clean [inapplicable] line
// forever while checking nothing — which is precisely the green-by-skip
// failure the marker's accounting exists to make visible.
func TestEveryLiveCanaryKeyIsMappedInTheWorkflow(t *testing.T) {
	root := liveCanaryRepositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(liveCanaryWorkflowPath)))
	if err != nil {
		t.Fatalf("read %s: %v", liveCanaryWorkflowPath, err)
	}
	workflow := string(raw)

	// Parsed as `KEY: value` assignments rather than by substring, because
	// every key is also named in that file's prose and a substring match
	// would accept a comment as a mapping.
	assignments := map[string]string{}
	for line := range strings.SplitSeq(workflow, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		at := strings.Index(trimmed, ": ")
		if at <= 0 {
			continue
		}
		assignments[trimmed[:at]] = strings.TrimSpace(trimmed[at+2:])
	}
	if got := assignments["GGG_LIVE_CANARY"]; got != `"1"` {
		t.Fatalf("%s sets GGG_LIVE_CANARY to %q, want \"1\"; without it every row skips itself and the job checks nothing",
			liveCanaryWorkflowPath, got)
	}

	for _, row := range liveCanaryProviders {
		for _, key := range row.Keys {
			value, mapped := assignments[key.Env]
			if !mapped {
				t.Fatalf("the %s canary declares %s and %s never sets it, so the row would skip in CI forever while reporting a clean [inapplicable] line",
					row.Module, key.Env, liveCanaryWorkflowPath)
			}
			if reason, pinned := liveCanaryPinnedWorkflowValues[key.Env]; pinned {
				if strings.Contains(value, "secrets.") {
					t.Fatalf("%s maps %s from a repository secret, but it is declared pinned: %s", liveCanaryWorkflowPath, key.Env, reason)
				}
				continue
			}
			// Everything else must come from a repository secret. A literal
			// would be a credential committed to the tree.
			if !strings.HasPrefix(value, "${{ secrets.") {
				t.Fatalf("%s sets %s to %q rather than mapping it from a repository secret; a live credential must never be a literal in this tree (and if the literal is deliberate, record it in liveCanaryPinnedWorkflowValues with its reason)",
					liveCanaryWorkflowPath, key.Env, value)
			}
		}
	}

	var dead []string
	for key := range liveCanaryPinnedWorkflowValues {
		declared := false
		for _, row := range liveCanaryProviders {
			for _, candidate := range row.Keys {
				if candidate.Env == key {
					declared = true
				}
			}
		}
		if !declared {
			dead = append(dead, key)
		}
	}
	sort.Strings(dead)
	if len(dead) > 0 {
		t.Fatalf("these liveCanaryPinnedWorkflowValues rows name no key any row declares: %v.\nDelete each one.", dead)
	}
}

// liveCanaryRepositoryRoot resolves this repository's root from the test's own
// directory. The suite lives in its own package rather than internal/gggcli
// for a structural reason worth stating here: internal/gggcli may not name an
// adapter package at all (internal/modkit/cli_scan.go:111
// ValidateCoreCLIPackages), because the CLI must reach the SELECTED adapter
// through the generated per-slot accessors so an unselected one stays out of
// every derivative's build. A canary over EVERY managed adapter cannot use
// those accessors — they expose one adapter per slot per environment, and the
// whole point is to exercise the sixteen that are not selected in this
// process — so direct imports are the only way, and this package is where
// they may legitimately live.
func liveCanaryRepositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "gogogadget.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no gogogadget.json above %s, so the repository root is unresolvable", dir)
		}
		dir = parent
	}
}

// ---------------------------------------------------------------------------
// Probe helpers. Deliberately small: a probe's value is its assertions, and
// every line of transport here is a line that is not one.

// liveCanaryJSON performs one request and decodes a JSON response into out
// when out is non-nil. A non-2xx status is an error carrying the status and a
// bounded prefix of the body — the body is where a provider explains itself,
// and also where it echoes a bad credential, which is why every caller's
// error passes through Redact.
func liveCanaryJSON(ctx context.Context, method, endpoint string, headers map[string]string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 500))
		return fmt.Errorf("%d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode the response: %w", err)
	}
	return nil
}

// liveCanaryGet reads a URL that already carries its own authorization — a
// presigned S3 GET — and returns the body and status without treating a 404
// as an error, because a 404 is one of the assertions.
func liveCanaryGet(ctx context.Context, endpoint string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return raw, response.StatusCode, err
}

// liveCanarySentryEnvelopeURL derives the envelope endpoint and the auth
// public key from a DSN, exactly as sentry-go's transport does
// (sentry-go@v0.48.0 internal/protocol/dsn.go:212-224). The returned endpoint
// carries no userinfo, so it is safe to print; the key is returned separately
// and only ever reaches the X-Sentry-Auth header.
func liveCanarySentryEnvelopeURL(dsn string) (endpoint, publicKey string, err error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", "", fmt.Errorf("SENTRY_DSN is not a URL")
	}
	if parsed.User == nil || parsed.User.Username() == "" {
		return "", "", fmt.Errorf("SENTRY_DSN carries no public key in its userinfo, so no envelope can be authenticated")
	}
	project := strings.Trim(parsed.Path, "/")
	if project == "" {
		return "", "", fmt.Errorf("SENTRY_DSN carries no project id in its path")
	}
	prefix := ""
	if at := strings.LastIndex(project, "/"); at >= 0 {
		prefix, project = "/"+project[:at], project[at+1:]
	}
	return parsed.Scheme + "://" + parsed.Host + prefix + "/api/" + project + "/envelope/", parsed.User.Username(), nil
}

// liveCanaryNonce is 16 hex characters of randomness for the identifiers that
// may be per-run — a storage key, an event id. Identifiers that must be
// stable use liveCanaryStableID instead.
func liveCanaryNonce() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand does not fail on any platform this repository builds
		// for; a time-derived fallback keeps a probe from dying on transport.
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

func liveCanaryHasHit(result search.Result, id string) bool {
	for _, hit := range result.Hits {
		if hit.ID == id {
			return true
		}
	}
	return false
}

func liveCanaryPlural(missing []string) string {
	if len(missing) == 1 {
		return "is"
	}
	return "are"
}

// liveCanaryAssignments renders the exact export the operator needs, per the
// skip grammar: the reason must carry the env assignment that un-skips it.
func liveCanaryAssignments(missing []string) string {
	parts := make([]string, 0, len(missing)+1)
	parts = append(parts, "GGG_LIVE_CANARY=1")
	for _, key := range missing {
		parts = append(parts, key+"=...")
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// The registry walk behind TestEveryManagedAdapterIsCanariedOrExcused.

type liveCanaryAdapter struct {
	path        string
	targets     map[string]bool
	environment map[string]map[string]bool
}

// liveCanaryManagedAdapters reads every manifest and returns the modules that
// publish at least one `managed` service target, with their target ids and
// their environment declarations' target scoping.
func liveCanaryManagedAdapters(t *testing.T, root string) map[string]liveCanaryAdapter {
	t.Helper()
	out := map[string]liveCanaryAdapter{}
	pattern := filepath.Join(root, "registry", "modules", "*", "*", "module.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var document struct {
			Module struct {
				ID      string `json:"id"`
				Runtime struct {
					System *struct {
						Adapter *struct {
							Targets []struct {
								ID   string `json:"id"`
								Mode string `json:"mode"`
							} `json:"targets"`
						} `json:"adapter"`
					} `json:"system"`
				} `json:"runtime"`
				Environment []struct {
					Key     string   `json:"key"`
					Targets []string `json:"targets"`
				} `json:"environment"`
			} `json:"module"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		system := document.Module.Runtime.System
		if system == nil || system.Adapter == nil {
			continue
		}
		targets := map[string]bool{}
		managed := false
		for _, target := range system.Adapter.Targets {
			targets[target.ID] = true
			if target.Mode == "managed" {
				managed = true
			}
		}
		if !managed {
			continue
		}
		environment := map[string]map[string]bool{}
		for _, variable := range document.Module.Environment {
			scope := map[string]bool{}
			for _, target := range variable.Targets {
				scope[target] = true
			}
			environment[variable.Key] = scope
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			relative = path
		}
		out[document.Module.ID] = liveCanaryAdapter{
			path:        filepath.ToSlash(relative),
			targets:     targets,
			environment: environment,
		}
	}
	return out
}

func liveCanarySortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func liveCanarySplitException(exception string) (module, target, key string, ok bool) {
	at := strings.LastIndex(exception, ":")
	if at < 0 {
		return "", "", "", false
	}
	key = exception[at+1:]
	scope := exception[:at]
	sep := strings.LastIndex(scope, "@")
	if sep < 0 || key == "" {
		return "", "", "", false
	}
	return scope[:sep], scope[sep+1:], key, true
}
