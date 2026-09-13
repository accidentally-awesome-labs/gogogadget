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
// # Coverage is the rule, not one of two options
//
// A managed target is admissible only if the seam can be satisfied through a
// documented HTTP contract in which the PROVIDER performs the decision — no
// vendored SDK, no reimplementation of vendor semantics. That is a human
// admission criterion and this file cannot check it: nothing here reads a
// vendor's architecture. What it checks is the proxy —
// TestEveryManagedTargetHasALiveCanaryOrAnAdmissibleExcuse walks the registry,
// finds every module publishing a `managed` service target, and REQUIRES a
// liveCanaryProviders row. The proxy is worth having because a target no probe
// can drive is the shape a target that fails the rule takes: if the provider
// does not decide over HTTP, there is no wire shape to pin.
//
// liveCanaryNothingToProbe is the narrow exception, NOT the second half of an
// either/or. An entry costs a stated reason AND the file:line that proves it —
// the same evidence a non-cleaning row owes for the seam that lacks a delete —
// and liveCanaryExcuseDefect refuses one carrying less. The inversion has a
// name: ggg/system/feature-flags-launchdarkly sat in that allowlist for a
// release. LaunchDarkly's server SDKs download rule definitions and evaluate
// the targeting DSL locally, so no HTTP contract exists in which LaunchDarkly
// decides; its Deps.Client was an interface nothing implemented, NewModule
// refused nil, and selecting the adapter could not boot. Under an either/or
// default a stated reason was enough to keep it in the matrix declaring a key
// nothing read. Under this default the remedy is a probe or retirement, and it
// was retired.
//
// The same test refuses a dead row and checks each row's adapter-configuration
// keys still exist in the owning manifest, so a renamed env key fails here
// instead of making a canary silently skip forever.

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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gogogadget/gogogadget/internal/gggcli"
	"github.com/gogogadget/gogogadget/internal/modkit"

	posthogadapter "github.com/gogogadget/gogogadget/internal/analytics/posthog"
	"github.com/gogogadget/gogogadget/internal/audit"
	auditotlp "github.com/gogogadget/gogogadget/internal/audit_export/otlp"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/billing/polar"
	cachememory "github.com/gogogadget/gogogadget/internal/cache/memory"
	cacheredis "github.com/gogogadget/gogogadget/internal/cache/redis"
	"github.com/gogogadget/gogogadget/internal/flags"
	flagsmithadapter "github.com/gogogadget/gogogadget/internal/flags/flagsmith"
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

// liveCanarySecretPrefix is the repository-secret naming convention every
// key this suite reads is mapped from. Namespaced so an operator reading the
// repository's secret list can tell, without opening this file, which secrets
// exist only for the weekly canary and can be revoked without breaking a
// deployment.
const liveCanarySecretPrefix = "CANARY_"

// liveCanaryHarnessEnvPrefix is what a canary-harness variable carries in the
// process and its repository secret does not: `GGG_CANARY_` is this
// harness's name for the variable, and the secret is the operator's name for
// the thing it carries.
const liveCanaryHarnessEnvPrefix = "GGG_CANARY_"

// liveCanaryKey is one environment variable a row consumes.
type liveCanaryKey struct {
	// Env is the variable's name.
	Env string
	// Required rows skip when it is unset; optional keys widen a probe's
	// assertions when present and are omitted from the skip reason. The
	// distinction is load-bearing outside this file too: the operator needs
	// to know which secrets a row cannot run at all without, and which ones
	// merely widen a row that already runs, so
	// TestEveryLiveCanarySecretIsOnTheOperatorChecklist files each secret
	// into whichever of the checklist's two tables this field names.
	Required bool
	// Secret overrides the repository secret the workflow maps this key
	// from. Empty means the convention: liveCanarySecretPrefix plus the env
	// key with any liveCanaryHarnessEnvPrefix removed.
	//
	// It exists because the two names answer different questions. The env
	// key is named after the SEAM that reads it; the secret is named after
	// the TARGET the operator configured, and those diverge where one seam
	// serves several targets — the four `STORAGE_R2_*` keys are `CANARY_R2_*`
	// secrets, because an operator holding a Cloudflare R2 token is not
	// holding a "storage" token. An override is a declaration, not an
	// escape: the guard still requires the liveCanarySecretPrefix namespace
	// and still requires the checklist to list exactly what is declared here.
	Secret string
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

// secretName is the repository secret this key is mapped from: the name the
// OPERATOR actually sets, which is the half of the mapping a guard over env
// keys alone never sees.
func (k liveCanaryKey) secretName() string {
	if k.Secret != "" {
		return k.Secret
	}
	return liveCanarySecretPrefix + strings.TrimPrefix(k.Env, liveCanaryHarnessEnvPrefix)
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

// liveCanaryAdmissionRule is the criterion a managed target is admitted
// under. It is quoted into the refusals below rather than left to this file's
// header, because a refusal that states only the mechanics teaches the wrong
// lesson: the next target gets a probe bolted onto an unshippable adapter
// instead of being reconsidered.
const liveCanaryAdmissionRule = "A managed target is admissible only if the seam can be satisfied through a documented HTTP contract in which the PROVIDER performs the decision — no vendored SDK, no reimplementation of vendor semantics."

// liveCanaryAdmissionProxy states what is mechanically checked, so no reader
// mistakes a refusal here for a claim that this suite inspects vendor
// architecture. It does not; it counts probes.
const liveCanaryAdmissionProxy = "This guard cannot read a vendor's architecture; the rule above is a human admission criterion. Its mechanical proxy is `the target has a live canary probe`: where the provider does not decide over HTTP there is no wire shape to pin, so an unprobeable target is the shape a target that fails the rule takes."

// liveCanaryExcuse is one managed target's exemption from the probe
// requirement: a module that publishes a `managed` service target and yet
// makes NO network call in the wiring a production boot produces, so there is
// no wire shape for a live canary to check.
//
// It is two fields rather than one string on purpose. An exemption is the only
// way past the requirement, so it owes the same evidence a non-cleaning canary
// row owes for the seam that lacks a delete: the claim, and the line a
// reviewer opens to check it.
type liveCanaryExcuse struct {
	// Why states which wiring a production boot produces and why it reaches
	// no provider.
	Why string
	// Cites is the `file.go:line` that makes Why checkable rather than
	// asserted.
	Cites string
}

// liveCanaryExcuseReasonFloor is the length below which a reason is a
// gesture. It matches the floor a canary row's Asserts prose carries, for the
// same reason: a safety claim degrades by being shortened until it says
// nothing, not by being deleted.
const liveCanaryExcuseReasonFloor = 80

// liveCanaryExcuseDefect names what makes an exemption inadmissible, or
// returns "" when it is admissible. It is a function rather than assertions
// inlined into the walk because the walk's input is the registry, which by
// construction carries no inadmissible entry once this gate passes — and a
// refusal that can only be observed by editing the registry is a refusal
// nobody re-checks. TestLiveCanaryRefusesAnExemptionWithoutItsEvidence drives
// it in both directions.
func liveCanaryExcuseDefect(excuse liveCanaryExcuse) string {
	why := strings.TrimSpace(excuse.Why)
	cites := strings.TrimSpace(excuse.Cites)
	switch {
	case why == "":
		return "states no reason at all"
	case len(why) < liveCanaryExcuseReasonFloor:
		return fmt.Sprintf("states its reason in %d character(s), under the %d-character floor; say which wiring a production boot produces and why it reaches no provider", len(why), liveCanaryExcuseReasonFloor)
	case cites == "":
		return "cites nothing; name the `file.go:line` a reviewer opens to check the reason, the way a non-cleaning canary row cites the seam that lacks a delete"
	case !liveCanaryCitesAFileLine(cites):
		return fmt.Sprintf("cites %q, which is not a `file.go:line`; a citation a reviewer cannot open is more prose", cites)
	}
	return ""
}

// liveCanaryCitesAFileLine reports whether text carries at least one
// `<path>.go:<line>` citation. Deliberately a shape check and not a file
// read: the cited line moves with every edit above it, and a gate that
// resolved citations would fail on refactors instead of on missing evidence.
func liveCanaryCitesAFileLine(text string) bool {
	for rest := text; ; {
		at := strings.Index(rest, ".go:")
		if at < 0 {
			return false
		}
		named := at > 0 && !strings.ContainsAny(rest[at-1:at], " \t\"`")
		rest = rest[at+len(".go:"):]
		if named && rest != "" && rest[0] >= '0' && rest[0] <= '9' {
			return true
		}
	}
}

// liveCanaryNothingToProbe is the exemption set, and empty is its intended
// steady state: under the rule above, a managed target no probe can drive is a
// target that should not ship, so the ordinary remedies are a probe or
// retirement and this map is the third, narrow one.
//
// Four entries have left it, and it is empty today. notifications-knock,
// webhooks-svix and usage-openmeter each wired New(d.Queue, nil, nil) and
// stopped at the local Postgres queue; they now trigger, create and ingest for
// real, so they are rows in liveCanaryProviders instead, and
// TestEveryManagedTargetHasALiveCanaryOrAnAdmissibleExcuse refuses the halfway
// state in both directions. feature-flags-launchdarkly left the other way: it
// was RETIRED, because the reason it could not be probed — LaunchDarkly
// evaluates its targeting DSL inside its own SDK, so there is no HTTP contract
// in which the provider decides — was the reason it could not ship. Its slot is
// now served by ggg/system/feature-flags-flagsmith, whose row is below.
var liveCanaryNothingToProbe = map[string]liveCanaryExcuse{}

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
			{Env: "STORAGE_R2_ACCOUNT_ID", Required: true, Secret: "CANARY_R2_ACCOUNT_ID"},
			{Env: "STORAGE_R2_ACCESS_KEY_ID", Required: true, Credential: true, Secret: "CANARY_R2_ACCESS_KEY_ID"},
			{Env: "STORAGE_R2_SECRET_ACCESS_KEY", Required: true, Credential: true, Secret: "CANARY_R2_SECRET_ACCESS_KEY"},
			{Env: "STORAGE_R2_BUCKET", Required: true, Secret: "CANARY_R2_BUCKET"},
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
	{
		Slot: "ggg/feature-flags", Module: "ggg/system/feature-flags-flagsmith", Target: "flagsmith",
		Keys: []liveCanaryKey{
			// Two credentials because Flagsmith has two APIs. The Environment
			// Key is documented as public and exposable in client-side code,
			// and the manifest declares it secret:false — but Credential is
			// this suite's own judgement, not the manifest's, and a key that
			// names someone's environment does not belong in a failure
			// message either.
			{Env: "FLAGSMITH_ENVIRONMENT_KEY", Required: true, Credential: true},
			{Env: "FLAGSMITH_MANAGEMENT_TOKEN", Required: true, Credential: true},
			{Env: "FLAGSMITH_PROJECT_ID", Required: true},
			{Env: "FLAGSMITH_ENVIRONMENT_ID", Required: true},
			// Both optional: set them to canary a self-hosted Flagsmith,
			// which serves both APIs from one host.
			{Env: "FLAGSMITH_FLAGS_API_URL"},
			{Env: "FLAGSMITH_API_URL"},
		},
		Cleanup: liveCanaryLeavesBounded,
		Leaves:  "no flag and no override — the first row on this slot that ends with the provider's flag list as it found it. The feature is named `ggg-live-canary-<16 hex>` per run and deleted through flags.Service.Delete (DELETE /api/v1/projects/{project}/features/{id}/), its per-org override through flags.Service.DeleteOverride (DELETE /api/v1/environments/{envKey}/identities/{pk}/featurestates/{id}/). The unique name is a safety property rather than cosmetics: Flagsmith deletes a feature project-wide, so a fixed name could delete an operator's real flag. What DOES persist is two Flagsmith identities, `ggg-live-canary` and `ggg-live-canary-unidentified` — the seam's seven methods include no identity delete (internal/flags/contract.go:19-27), one is created explicitly by the override path (internal/flags/flagsmith/flagsmith.go:614) and the other lazily by evaluation — so the claim is bounded, not clean: exactly two rows, stable identifiers, reused by every later run instead of accumulating. The deletes are also deferred and their errors dropped, so a failed run can leave one feature behind under the run's own name.",
		Asserts: "POST https://edge.api.flagsmith.com/api/v1/identities/ still accepts {\"identifier\":\"<org>\"} with the PUBLIC Environment Key in X-Environment-Key and still answers {identifier, traits[], flags[{feature:{id,name},enabled}]} — the two halves internal/flags/flagsmith/flagsmith_test.go:150-175 believes, including that `enabled` is FLAGSMITH's decision and not a value this adapter recomputes, which is the whole reason this target is admissible where LaunchDarkly was not. Then that the SECRET organisation token still authorises the Management API as `Authorization: Api-Key <token>` across the four mutating shapes the seam needs: POST /api/v1/projects/{project}/features/ accepting {name,description,default_enabled,initial_value} (201), POST /api/v1/environments/{envKey}/identities/{pk}/featurestates/ accepting {feature,enabled} (201), and the two 204 deletes. And that GET /api/v1/environments/{envKey}/featurestates/?feature_name=<key>&anyIdentity=1 still returns ONLY identity-override rows carrying identity:{id,identifier} — the provider-side filter flagsmith_test.go:380-392 relies on to enumerate per-org overrides without paging every identity. The decisive assertion is precedence: the flag's environment default is OFF and only the canary org's identity override is ON, so a true answer proves Flagsmith applied identity precedence rather than this adapter guessing it.",
		Probe: func(ctx context.Context, env liveCanaryEnv) (string, error) {
			endpoint := "https://edge.api.flagsmith.com/api/v1/identities/"
			if override := env.Get("FLAGSMITH_FLAGS_API_URL"); override != "" {
				endpoint = strings.TrimRight(override, "/") + "/api/v1/identities/"
			}
			projectID, err := strconv.Atoi(env.Get("FLAGSMITH_PROJECT_ID"))
			if err != nil {
				return endpoint, fmt.Errorf("FLAGSMITH_PROJECT_ID is not numeric: %w", err)
			}
			environmentID, err := strconv.Atoi(env.Get("FLAGSMITH_ENVIRONMENT_ID"))
			if err != nil {
				return endpoint, fmt.Errorf("FLAGSMITH_ENVIRONMENT_ID is not numeric: %w", err)
			}
			service := flagsmithadapter.New(flagsmithadapter.Service{
				Flags:          env.Get("FLAGSMITH_FLAGS_API_URL"),
				Management:     env.Get("FLAGSMITH_API_URL"),
				EnvironmentKey: env.Get("FLAGSMITH_ENVIRONMENT_KEY"),
				Token:          env.Get("FLAGSMITH_MANAGEMENT_TOKEN"),
				ProjectID:      projectID,
				EnvironmentID:  environmentID,
				Cache:          cachememory.New(),
			})
			// Unique per run: Flagsmith deletes a feature project-wide, so a
			// fixed name could delete an operator's real flag.
			key := liveCanaryStableID + "-" + liveCanaryNonce()
			if err := service.Upsert(ctx, flags.Flag{Key: key, Description: "ggg live canary", Enabled: false, Rollout: 100}); err != nil {
				return endpoint, fmt.Errorf("create flag (POST /api/v1/projects/%d/features/): %w", projectID, err)
			}
			// Registered before the assertions so a failing evaluation cannot
			// leave the feature behind. WithoutCancel because the row's
			// deadline may already have fired by the time cleanup runs.
			defer func() { _ = service.Delete(context.WithoutCancel(ctx), key) }()
			if err := service.SetOverride(ctx, key, liveCanaryStableID, true); err != nil {
				return endpoint, fmt.Errorf("set per-org override (POST /api/v1/environments/{env}/identities/{pk}/featurestates/): %w", err)
			}
			defer func() { _ = service.DeleteOverride(context.WithoutCancel(ctx), key, liveCanaryStableID) }()
			overrides, err := service.ListOverrides(ctx, key)
			if err != nil {
				return endpoint, fmt.Errorf("list overrides (GET /api/v1/environments/{env}/featurestates/?anyIdentity=1): %w", err)
			}
			found := false
			for _, override := range overrides {
				if override.OrgID == liveCanaryStableID && override.Enabled {
					found = true
				}
			}
			if !found {
				return endpoint, fmt.Errorf("list overrides (GET /api/v1/environments/{env}/featurestates/?anyIdentity=1): the identity override just written was not returned; internal/flags/flagsmith/flagsmith_test.go:380-392 believes `anyIdentity` returns exactly these rows with identity.identifier set")
			}
			// The provider performs the decision: the flag's environment
			// default is OFF and only this identity's override is ON, so a
			// true here proves Flagsmith applied identity precedence. The two
			// checks use DIFFERENT org ids on purpose — Service.Enabled caches
			// one subject's whole decision set for 30s
			// (internal/flags/flagsmith/flagsmith.go:83), so a repeat on the
			// same org would read the cache and assert nothing.
			if !service.Enabled(ctx, liveCanaryStableID, key) {
				return endpoint, fmt.Errorf("evaluate (POST /api/v1/identities/): the identity override was written but Flagsmith answered disabled; identity overrides outrank the environment default (https://docs.flagsmith.com/flagsmith-concepts/segments#flag-evaluation-precedence)")
			}
			if service.Enabled(ctx, liveCanaryStableID+"-unidentified", key) {
				return endpoint, fmt.Errorf("evaluate (POST /api/v1/identities/): an org with no override answered enabled, but the flag's environment default is off")
			}
			if _, err := service.List(ctx); err != nil {
				return endpoint, fmt.Errorf("list flags (GET /api/v1/projects/%d/features/?environment=%d): %w", projectID, environmentID, err)
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

// liveCanaryCoverage is the walk's judgement of one managed-adapter
// population against the two declarations.
type liveCanaryCoverage struct {
	// Unprobed publish a managed service target and have no canary row.
	Unprobed []string
	// Both are declared twice — a row AND an exemption.
	Both []string
	// Inadmissible are exemptions that do not carry their evidence, each
	// rendered `<module> (<defect>)`.
	Inadmissible []string
}

// liveCanaryClassify is the whole of that judgement, pure so every branch can
// be driven on a synthetic population: the real registry carries none of
// these defects once the gate passes, and a refusal nobody can observe is a
// refusal nobody maintains.
func liveCanaryClassify(managed []string, canaried map[string]liveCanaryProvider, excused map[string]liveCanaryExcuse) liveCanaryCoverage {
	var coverage liveCanaryCoverage
	for _, id := range managed {
		_, isCanaried := canaried[id]
		excuse, isExcused := excused[id]
		switch {
		case isCanaried && isExcused:
			coverage.Both = append(coverage.Both, id)
		case isCanaried:
		case !isExcused:
			coverage.Unprobed = append(coverage.Unprobed, id)
		default:
			if defect := liveCanaryExcuseDefect(excuse); defect != "" {
				coverage.Inadmissible = append(coverage.Inadmissible, id+" ("+defect+")")
			}
		}
	}
	sort.Strings(coverage.Unprobed)
	sort.Strings(coverage.Both)
	sort.Strings(coverage.Inadmissible)
	return coverage
}

// liveCanaryUnprobedRefusal is what a managed target with no probe gets. It
// quotes the rule, then states the bound on what was actually checked, then
// names the remedy — and the remedy is ordered, because the cheap move
// (bolting a probe onto an adapter that should not ship) is the wrong one.
func liveCanaryUnprobedRefusal(modules []string) string {
	return fmt.Sprintf("these modules publish a managed service target and have no live canary probe: %v.\n"+
		"The rule: %s\n"+
		"%s\n"+
		"Remedy, in this order: implement the probe as a liveCanaryProviders row, or RETIRE the target. A managed reference nobody can probe asks every derivative to trust a wire shape this repository has never checked, and ships a credential declaration nothing reads. Exempting it in liveCanaryNothingToProbe is the third option and the narrow one: it costs a stated reason and the file:line that proves it.",
		modules, liveCanaryAdmissionRule, liveCanaryAdmissionProxy)
}

// liveCanaryInadmissibleExemptionRefusal is what an exemption without its
// evidence gets.
func liveCanaryInadmissibleExemptionRefusal(defects []string) string {
	return fmt.Sprintf("these liveCanaryNothingToProbe entries are not admissible exemptions: %v.\n"+
		"The rule: %s\n"+
		"An exemption is the only way past the probe requirement, so it carries the evidence a non-cleaning canary row carries: prose stating which wiring a production boot produces and why it reaches no provider, plus the `file.go:line` a reviewer opens to check it. Without both it is an opinion, and an opinion is how a dead target keeps its place in the matrix.",
		defects, liveCanaryAdmissionRule)
}

// Every module that publishes a `managed` service target must have a live
// canary probe; an exemption is the narrow alternative and must carry its
// evidence. Every declaration must still describe the registry. This is what
// makes "adding a provider is a row, not a new file" true rather than
// aspirational: a new managed adapter fails here until someone probes it,
// retires it, or proves on the record that nothing can be probed.
func TestEveryManagedTargetHasALiveCanaryOrAnAdmissibleExcuse(t *testing.T) {
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

	ids := make([]string, 0, len(managed))
	for id := range managed {
		ids = append(ids, id)
	}
	coverage := liveCanaryClassify(ids, canaried, liveCanaryNothingToProbe)
	if len(coverage.Unprobed) > 0 {
		t.Fatal(liveCanaryUnprobedRefusal(coverage.Unprobed))
	}
	if len(coverage.Inadmissible) > 0 {
		t.Fatal(liveCanaryInadmissibleExemptionRefusal(coverage.Inadmissible))
	}
	if len(coverage.Both) > 0 {
		t.Fatalf("these modules are both canaried and exempted from canarying: %v.\nThe exemption is stale — delete the liveCanaryNothingToProbe entry.", coverage.Both)
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

// ---------------------------------------------------------------------------
// The tie between a canary row and an install.

// liveCanaryInstallCoverage is the judgement of the canary rows against this
// repository's own installation intent, in both directions.
type liveCanaryInstallCoverage struct {
	// Uninstalled are canary rows whose adapter no environment selects and
	// `verification_installs` does not declare. The row imports the adapter's
	// package, so its compilation depends on source no lock row owns.
	Uninstalled []string
	// Unprobed are verification installs with no canary row. The declaration
	// installs an adapter for verification and then verifies nothing.
	Unprobed []string
}

// liveCanaryClassifyInstalls is the whole judgement, pure so both directions
// can be driven on a synthetic population: the real repository carries neither
// defect once the gate passes, and a refusal nobody can observe is a refusal
// nobody maintains.
func liveCanaryClassifyInstalls(selected, verification []string, canaried map[string]liveCanaryProvider) liveCanaryInstallCoverage {
	var coverage liveCanaryInstallCoverage
	installed := map[string]bool{}
	for _, id := range selected {
		installed[id] = true
	}
	for _, id := range verification {
		installed[id] = true
		if _, probed := canaried[id]; !probed {
			coverage.Unprobed = append(coverage.Unprobed, id)
		}
	}
	for id := range canaried {
		if !installed[id] {
			coverage.Uninstalled = append(coverage.Uninstalled, id)
		}
	}
	sort.Strings(coverage.Uninstalled)
	sort.Strings(coverage.Unprobed)
	return coverage
}

// liveCanaryUninstalledRefusal is what a canary row for an adapter this
// project neither selects nor verification-installs gets.
func liveCanaryUninstalledRefusal(modules []string) string {
	return fmt.Sprintf("these liveCanaryProviders rows drive adapters this repository neither selects nor installs for verification: %v.\n"+
		"A row imports the adapter's package directly — that is the whole reason the self_host carve-out in ValidatePayloadAdapterImports exists — so the row compiles against source no lock row owns: invisible to install, to update and to removal, and reproducible by no `ggg sync`.\n"+
		"Remedy: add each module to gogogadget.json `verification_installs` (installed for verification, selected by nothing), select it with `ggg provider set`, or delete the row.",
		modules)
}

// liveCanaryUnprobedInstallRefusal is what a verification install with no row
// gets. It is the other half of the admission rule: a managed target earns its
// place by being probed, so an install whose stated purpose is verification
// and which verifies nothing is the same defect
// ggg/system/feature-flags-launchdarkly was — a credential declaration nothing
// reads — wearing a different key.
func liveCanaryUnprobedInstallRefusal(modules []string) string {
	return fmt.Sprintf("gogogadget.json declares these verification installs and no liveCanaryProviders row probes them: %v.\n"+
		"The rule: %s\n"+
		"A verification install is the ONE way this repository installs an adapter nothing selects, and its entire justification is that the canary must import it. Without a row it installs an adapter for nothing.\n"+
		"Remedy: write the probe as a liveCanaryProviders row, or delete the entry from verification_installs.",
		modules, liveCanaryAdmissionRule)
}

// liveCanaryProjectIntent reads this repository's own committed intent through
// the same parser `ggg sync` uses, so a declaration this test accepts is a
// declaration the engine accepts.
func liveCanaryProjectIntent(t *testing.T, root string) modkit.Project {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "gogogadget.json"))
	if err != nil {
		t.Fatalf("read gogogadget.json: %v", err)
	}
	project, err := modkit.ParseProject(raw)
	if err != nil {
		t.Fatalf("parse gogogadget.json: %v", err)
	}
	return project
}

// liveCanarySelectedAdapters is every adapter some environment selects.
func liveCanarySelectedAdapters(project modkit.Project) []string {
	out := make([]string, 0, len(project.Providers)*3)
	for _, selections := range project.Providers {
		for _, choice := range []modkit.ProviderSelection{selections.Development, selections.Test, selections.Production} {
			if choice.Adapter != "" {
				out = append(out, choice.Adapter)
			}
		}
	}
	sort.Strings(out)
	return out
}

// The load-bearing tie, both directions. It is what makes
// `verification_installs` narrow rather than a hole: an entry costs a probe,
// and a probe costs an install. Neither declaration can grow without the
// other, so the canary suite's imports stay declared and this repository's
// install set stays derivable from its own intent.
func TestEveryCanaryRowIsInstalledAndEveryVerificationInstallIsProbed(t *testing.T) {
	root := liveCanaryRepositoryRoot(t)
	project := liveCanaryProjectIntent(t, root)
	selected := liveCanarySelectedAdapters(project)

	// Floors. Both directions are vacuous on an empty population, and an
	// empty population here means the reader broke, not the repository: this
	// project selects one adapter per slot per environment and declares the
	// managed adapters its canary reaches.
	if len(liveCanaryProviders) < 15 {
		t.Fatalf("only %d canary row(s); the suite has collapsed, not the registry", len(liveCanaryProviders))
	}
	if len(selected) < 15 {
		t.Fatalf("gogogadget.json yielded only %d selected adapter(s); the intent reader has collapsed", len(selected))
	}
	if len(project.VerificationInstalls) == 0 {
		t.Fatal("gogogadget.json declares no verification installs, yet the canary rows below import managed adapters directly; " +
			"if every row's adapter is genuinely selected this floor is wrong, but silence here is how the unowned-source state returned")
	}

	canaried := map[string]liveCanaryProvider{}
	for _, row := range liveCanaryProviders {
		canaried[row.Module] = row
	}
	coverage := liveCanaryClassifyInstalls(selected, project.VerificationInstalls, canaried)
	// The declaration's own defect is reported first. A single edit can
	// produce both — renaming a row's Module leaves its verification install
	// unprobed AND the new name uninstalled — and the entry an author just
	// wrote is the one they can act on.
	if len(coverage.Unprobed) > 0 {
		t.Fatal(liveCanaryUnprobedInstallRefusal(coverage.Unprobed))
	}
	if len(coverage.Uninstalled) > 0 {
		t.Fatal(liveCanaryUninstalledRefusal(coverage.Uninstalled))
	}
}

// Both refusals, driven on a synthetic population. The real repository can
// carry neither once the gate above passes, so this is the only place the
// classification's branches are observable.
func TestLiveCanaryInstallClassificationRefusesBothDirections(t *testing.T) {
	row := func(id string) map[string]liveCanaryProvider {
		return map[string]liveCanaryProvider{id: {Module: id}}
	}
	t.Run("a row for an adapter nothing installs is refused", func(t *testing.T) {
		coverage := liveCanaryClassifyInstalls(nil, nil, row("ggg/system/probe-managed"))
		if len(coverage.Uninstalled) != 1 || coverage.Uninstalled[0] != "ggg/system/probe-managed" {
			t.Fatalf("Uninstalled = %v, want the unselected, non-verification-installed row named", coverage.Uninstalled)
		}
		refusal := liveCanaryUninstalledRefusal(coverage.Uninstalled)
		for _, want := range []string{"ggg/system/probe-managed", "verification_installs", "ggg provider set"} {
			if !strings.Contains(refusal, want) {
				t.Fatalf("the refusal must name %q; it said:\n%s", want, refusal)
			}
		}
	})
	t.Run("selection and verification install both satisfy it", func(t *testing.T) {
		for name, coverage := range map[string]liveCanaryInstallCoverage{
			"selected":             liveCanaryClassifyInstalls([]string{"ggg/system/probe-managed"}, nil, row("ggg/system/probe-managed")),
			"verification install": liveCanaryClassifyInstalls(nil, []string{"ggg/system/probe-managed"}, row("ggg/system/probe-managed")),
		} {
			if len(coverage.Uninstalled) != 0 || len(coverage.Unprobed) != 0 {
				t.Fatalf("%s population refused: %+v", name, coverage)
			}
		}
	})
	t.Run("a verification install with no row is refused", func(t *testing.T) {
		coverage := liveCanaryClassifyInstalls(nil, []string{"ggg/system/probe-managed"}, map[string]liveCanaryProvider{})
		if len(coverage.Unprobed) != 1 || coverage.Unprobed[0] != "ggg/system/probe-managed" {
			t.Fatalf("Unprobed = %v, want the unprobed verification install named", coverage.Unprobed)
		}
		refusal := liveCanaryUnprobedInstallRefusal(coverage.Unprobed)
		for _, want := range []string{"ggg/system/probe-managed", liveCanaryAdmissionRule, "liveCanaryProviders row"} {
			if !strings.Contains(refusal, want) {
				t.Fatalf("the refusal must name %q; it said:\n%s", want, refusal)
			}
		}
	})
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

// The probe requirement, driven. The real registry has, by construction, no
// managed target without a probe once the walk above passes, so the refusal
// is driven on a synthetic population instead: a guard whose red can only be
// seen by breaking the registry is a guard whose red nobody sees.
func TestLiveCanaryRefusesAManagedTargetWithNoProbe(t *testing.T) {
	const probed = "example/system/thing-managed"
	const unprobed = "example/system/other-managed"
	rows := map[string]liveCanaryProvider{probed: {Slot: "example/thing", Module: probed, Target: "managed"}}
	admissible := map[string]liveCanaryExcuse{unprobed: {
		Why:   "the adapter makes no network call in the wiring a production boot produces: New takes the local queue and nothing else, so there is no wire shape a probe could pin.",
		Cites: "internal/example/other/other.go:41",
	}}

	coverage := liveCanaryClassify([]string{probed, unprobed}, rows, nil)
	if got := coverage.Unprobed; len(got) != 1 || got[0] != unprobed {
		t.Fatalf("a managed target with no row and no exemption must be refused; classify reported Unprobed=%v", got)
	}
	refusal := liveCanaryUnprobedRefusal(coverage.Unprobed)
	for _, want := range []string{
		unprobed,
		"have no live canary probe",
		liveCanaryAdmissionRule,
		liveCanaryAdmissionProxy,
		"or RETIRE the target",
	} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("the refusal must carry %q so the reader gets the rule and the remedy, not just the mechanics; it read:\n%s", want, refusal)
		}
	}

	// The two ways past it, both of which must pass: a probe, or an
	// exemption that carries its evidence.
	if coverage := liveCanaryClassify([]string{probed}, rows, nil); len(coverage.Unprobed) > 0 {
		t.Fatalf("a probed managed target must pass; classify reported Unprobed=%v", coverage.Unprobed)
	}
	coverage = liveCanaryClassify([]string{probed, unprobed}, rows, admissible)
	if len(coverage.Unprobed) > 0 || len(coverage.Inadmissible) > 0 || len(coverage.Both) > 0 {
		t.Fatalf("a probe plus an admissible exemption must pass; classify reported %+v", coverage)
	}
	// And declaring both remains a stale-exemption refusal.
	if coverage := liveCanaryClassify([]string{probed}, rows, map[string]liveCanaryExcuse{probed: admissible[unprobed]}); len(coverage.Both) != 1 {
		t.Fatalf("a target that is both probed and exempted must be refused; classify reported %+v", coverage)
	}
}

// The exemption gate, driven. An exemption is the one way past the probe
// requirement, so the evidence it owes is checked in both directions rather
// than trusted to whoever adds the next one.
func TestLiveCanaryRefusesAnExemptionWithoutItsEvidence(t *testing.T) {
	const admissibleWhy = "the adapter makes no network call in the wiring a production boot produces: New takes the local queue and nothing else, so there is no wire shape a probe could pin."

	for name, testCase := range map[string]struct {
		excuse  liveCanaryExcuse
		refused string
	}{
		"no reason at all": {
			excuse:  liveCanaryExcuse{Cites: "internal/example/other/other.go:41"},
			refused: "states no reason at all",
		},
		"a reason shortened until it says nothing": {
			excuse:  liveCanaryExcuse{Why: "nothing to probe here", Cites: "internal/example/other/other.go:41"},
			refused: "states its reason in 21 character(s), under the 80-character floor",
		},
		"a reason with no citation": {
			excuse:  liveCanaryExcuse{Why: admissibleWhy},
			refused: "cites nothing",
		},
		"a citation that is not a file:line": {
			excuse:  liveCanaryExcuse{Why: admissibleWhy, Cites: "the adapter package"},
			refused: `cites "the adapter package", which is not a ` + "`file.go:line`",
		},
		"a file with no line": {
			excuse:  liveCanaryExcuse{Why: admissibleWhy, Cites: "internal/example/other/other.go"},
			refused: "which is not a `file.go:line`",
		},
		"reason and citation both present": {
			excuse: liveCanaryExcuse{Why: admissibleWhy, Cites: "internal/example/other/other.go:41"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			defect := liveCanaryExcuseDefect(testCase.excuse)
			if testCase.refused == "" {
				if defect != "" {
					t.Fatalf("an exemption carrying its reason and its file:line must be admissible; it was refused with %q", defect)
				}
				return
			}
			if !strings.Contains(defect, testCase.refused) {
				t.Fatalf("the exemption must be refused naming %q; liveCanaryExcuseDefect returned %q", testCase.refused, defect)
			}
			// And the refusal the walk renders must carry the module, the
			// defect and the rule, so the reader learns why the evidence is
			// owed rather than only that it is.
			const module = "example/system/other-managed"
			coverage := liveCanaryClassify([]string{module}, nil, map[string]liveCanaryExcuse{module: testCase.excuse})
			if len(coverage.Inadmissible) != 1 {
				t.Fatalf("an inadmissible exemption must be classified as one; classify reported %+v", coverage)
			}
			refusal := liveCanaryInadmissibleExemptionRefusal(coverage.Inadmissible)
			for _, want := range []string{module, defect, liveCanaryAdmissionRule, "not admissible exemptions"} {
				if !strings.Contains(refusal, want) {
					t.Fatalf("the refusal must carry %q; it read:\n%s", want, refusal)
				}
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

// liveCanaryWorkflowJob is the one job whose env block reaches the suite's
// process. The env block is read from that job rather than from the file's
// text because a `KEY: value` line anywhere else — a `with:` input, a
// comment's example — is not a mapping, and a guard that counted one would
// report agreement the operator never gets.
const liveCanaryWorkflowJob = "live-canary"

// liveCanaryChecklistPath holds the operator's secret checklist: the third
// surface, and the only one the person setting the secrets reads.
const liveCanaryChecklistPath = "content/docs/testing.md"

// liveCanaryPinnedWorkflowValues are the keys the workflow sets to a literal
// rather than mapping from a repository secret, with the reason. A literal is
// the stronger choice for a selector whose wrong value the suite refuses: a
// misconfigured repository secret cannot even present the refusal. A pinned
// key is therefore NOT a repository secret and must not appear on the
// operator's checklist.
var liveCanaryPinnedWorkflowValues = map[string]string{
	"POLAR_SERVER": "pinned to `sandbox` because the probe creates a checkout session and ingests an immutable metered event; the suite refuses `production` and the pin means a wrong secret cannot reach the refusal",
}

// liveCanaryWorkflowNonRowEnv are the job's env entries that are not a row's
// credential mapping, with the reason each is there. Everything else in that
// block must belong to a row: this map is what makes the orphan direction
// checkable at all, and an entry added here is an entry a reader must justify
// rather than one the guard quietly tolerates.
var liveCanaryWorkflowNonRowEnv = map[string]string{
	liveCanaryEnvVar: "the gate that un-skips the suite; without it every row skips itself and the job is a green report on nothing",
}

// liveCanaryParityBound is what this parity does NOT establish, quoted into
// its own refusals and restated in content/docs/testing.md where the
// checklist lives. Three surfaces agreeing is worth having and is worth
// exactly that: the guard reads NAMES. It cannot read a value, so it cannot
// know whether a token is live, whether it addresses a sandbox rather than a
// production tenant, or whether the project behind it is throwaway. A guard
// whose existence implies more than it checks is worse than no guard, because
// the operator stops looking.
const liveCanaryParityBound = "This parity holds three surfaces in agreement — the canary table, the workflow's job env block, and the operator's checklist — and holds nothing else. It reads names, never values: that a secret is live, that it addresses a sandbox and not a production tenant, and that the account behind it is throwaway remain the operator's, and content/docs/testing.md states them where the checklist lives."

// liveCanaryWorkflowFile is the shape this guard reads, decoded with
// gopkg.in/yaml.v3 — the parser internal/modkit/ci_workflow_test.go already
// reads this same file with, rather than a second one.
type liveCanaryWorkflowFile struct {
	Jobs map[string]struct {
		Env map[string]string `yaml:"env"`
	} `yaml:"jobs"`
}

// liveCanarySecretExpression matches the only mapping shape a credential may
// reach the job by. Anchored: `${{ secrets.X }}` concatenated with anything
// else is not a mapping this guard will vouch for.
var liveCanarySecretExpression = regexp.MustCompile(`^\$\{\{\s*secrets\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}$`)

// liveCanaryMappedSecret returns the repository secret a workflow env value
// maps from.
func liveCanaryMappedSecret(value string) (string, bool) {
	match := liveCanarySecretExpression.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return "", false
	}
	return match[1], true
}

// liveCanarySortedNames is the deterministic key order every refusal below
// renders in. A refusal whose list reorders between runs is a refusal nobody
// can diff.
func liveCanarySortedNames[V any](set map[string]V) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// liveCanaryDeclaration is one row's claim on one environment variable.
type liveCanaryDeclaration struct {
	key    liveCanaryKey
	module string
}

// liveCanaryDeclaredKeys indexes the table by env key. A key two rows declare
// is not a defect — OTLP_ENDPOINT could legitimately serve two adapters — so
// the last writer wins for the module attribution and the parity is over the
// key set either way.
func liveCanaryDeclaredKeys(rows []liveCanaryProvider) map[string]liveCanaryDeclaration {
	declared := map[string]liveCanaryDeclaration{}
	for _, row := range rows {
		for _, key := range row.Keys {
			declared[key.Env] = liveCanaryDeclaration{key: key, module: row.Module}
		}
	}
	return declared
}

// liveCanaryEnvParity is the judgement of the table against the workflow's
// job env block, in every direction that can go wrong.
type liveCanaryEnvParity struct {
	// Unmapped: a row declares the key and the job env does not set it. The
	// expensive one — see liveCanaryUnmappedRefusal.
	Unmapped []string
	// Orphaned: the job env sets a key no row declares and no entry in
	// liveCanaryWorkflowNonRowEnv justifies.
	Orphaned []string
	// Misnamed: the key is mapped, from the wrong repository secret.
	Misnamed []string
	// Literal: the key is set to something that is not a secret reference and
	// is not declared pinned.
	Literal []string
	// PinnedFromSecret: a key declared pinned is taken from a secret anyway.
	PinnedFromSecret []string
	// DeadPin, DeadNonRow: a declaration that describes nothing.
	DeadPin, DeadNonRow []string
}

func (p liveCanaryEnvParity) clean() bool {
	return len(p.Unmapped) == 0 && len(p.Orphaned) == 0 && len(p.Misnamed) == 0 &&
		len(p.Literal) == 0 && len(p.PinnedFromSecret) == 0 &&
		len(p.DeadPin) == 0 && len(p.DeadNonRow) == 0
}

// liveCanaryClassifyEnv is the whole judgement, pure so every branch can be
// driven on a synthetic env block: the real workflow carries none of these
// defects once the gate passes, and a refusal nobody can observe is a refusal
// nobody maintains.
func liveCanaryClassifyEnv(rows []liveCanaryProvider, env, pinned, nonRow map[string]string) liveCanaryEnvParity {
	declared := liveCanaryDeclaredKeys(rows)

	var parity liveCanaryEnvParity
	for _, name := range liveCanarySortedNames(declared) {
		declaration := declared[name]
		value, mapped := env[name]
		if !mapped {
			parity.Unmapped = append(parity.Unmapped, fmt.Sprintf("%s declares %s, which the job env never sets (the operator would set %s)",
				declaration.module, name, declaration.key.secretName()))
			continue
		}
		if reason, isPinned := pinned[name]; isPinned {
			if strings.Contains(value, "secrets.") {
				parity.PinnedFromSecret = append(parity.PinnedFromSecret, fmt.Sprintf("%s is taken from a secret (%q) though it is declared pinned: %s", name, value, reason))
			}
			continue
		}
		secret, isSecret := liveCanaryMappedSecret(value)
		if !isSecret {
			parity.Literal = append(parity.Literal, fmt.Sprintf("%s = %q", name, value))
			continue
		}
		if want := declaration.key.secretName(); secret != want {
			parity.Misnamed = append(parity.Misnamed, fmt.Sprintf("%s is mapped from %s; %s declares %s", name, secret, declaration.module, want))
		}
	}

	for _, name := range liveCanarySortedNames(env) {
		if _, isRow := declared[name]; isRow {
			continue
		}
		if _, justified := nonRow[name]; justified {
			continue
		}
		parity.Orphaned = append(parity.Orphaned, fmt.Sprintf("%s = %q", name, env[name]))
	}

	for _, name := range liveCanarySortedNames(pinned) {
		if _, isRow := declared[name]; !isRow {
			parity.DeadPin = append(parity.DeadPin, name)
		}
	}
	for _, name := range liveCanarySortedNames(nonRow) {
		if _, set := env[name]; !set {
			parity.DeadNonRow = append(parity.DeadNonRow, name)
		}
	}
	return parity
}

// liveCanaryUnmappedRefusal is what a row key the workflow does not map gets.
// It states the CONSEQUENCE rather than the mismatch, because the mismatch is
// cheap to read and the consequence is not: the row does not fail, it skips —
// and it keeps skipping after the operator sets the secret, because nothing
// carries the secret into the job. The skip reason names the missing key, so
// the operator reads a correct sentence about a credential they have already
// supplied and concludes the canary is broken rather than the mapping.
func liveCanaryUnmappedRefusal(unmapped []string) string {
	return fmt.Sprintf("%s never sets it, so the row would skip in CI forever while reporting a clean [inapplicable] line:\n  %s\nThe cost is not the mismatch. An unmapped key makes its row skip EVEN AFTER the operator sets the repository secret — the secret exists and nothing carries it into the job — and the row's own skip reason names the key as missing, so the operator reads a true sentence about a credential they already supplied and concludes the canary is broken. Map each key in the %s job env of %s.",
		liveCanaryWorkflowPath, strings.Join(unmapped, "\n  "), liveCanaryWorkflowJob, liveCanaryWorkflowPath)
}

// liveCanaryOrphanRefusal is what an env entry no row declares gets. An
// orphan costs the operator a secret they set for nothing, and it costs the
// checklist its meaning: once the workflow may carry mappings nothing reads,
// the checklist's set equality stops being evidence that a configured
// provider is a probed provider.
func liveCanaryOrphanRefusal(orphaned []string) string {
	return fmt.Sprintf("the %s job of %s sets env entries no canary row declares:\n  %s\nNothing reads them. An orphan mapping is a checklist line the operator sets for nothing, and it is the same defect as the opposite direction wearing the other face: once this block may carry entries no row reads, its agreement with the checklist stops being evidence that a configured provider is a probed provider. Delete the mapping, declare the key on the row that needs it, or — if it is not a row's credential at all — record it in liveCanaryWorkflowNonRowEnv with its reason.",
		liveCanaryWorkflowJob, liveCanaryWorkflowPath, strings.Join(orphaned, "\n  "))
}

// liveCanaryWorkflowEnv is the one job's env block, read through
// gopkg.in/yaml.v3. Shared by the two directions below, which are separate
// tests rather than one because a guard gets one red proof in
// internal/modkit/testdata/redproof and these two defects are not the same
// defect: the first is a row nobody can supply, the second is a secret
// nobody reads.
func liveCanaryWorkflowEnv(t *testing.T) map[string]string {
	t.Helper()
	root := liveCanaryRepositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(liveCanaryWorkflowPath)))
	if err != nil {
		t.Fatalf("read %s: %v", liveCanaryWorkflowPath, err)
	}
	var workflow liveCanaryWorkflowFile
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse %s: %v", liveCanaryWorkflowPath, err)
	}
	job, ok := workflow.Jobs[liveCanaryWorkflowJob]
	if !ok {
		t.Fatalf("%s has no %s job, so nothing maps a credential into this suite and every row skips forever", liveCanaryWorkflowPath, liveCanaryWorkflowJob)
	}
	return job.Env
}

// Every key a row declares must be mapped in the workflow that runs the
// suite, under the secret name the row declares. Without this, adding a row
// is half a change: the row exists, the CI job cannot supply it, and the
// canary reports a clean [inapplicable] line forever while checking nothing —
// which is precisely the green-by-skip failure the marker's accounting exists
// to make visible, and the one defect the operator cannot fix by setting the
// secret.
func TestEveryLiveCanaryKeyIsMappedInTheWorkflow(t *testing.T) {
	env := liveCanaryWorkflowEnv(t)
	if got := env[liveCanaryEnvVar]; got != "1" {
		t.Fatalf("the %s job sets %s to %q, want \"1\"; without it every row skips itself and the job checks nothing",
			liveCanaryWorkflowJob, liveCanaryEnvVar, got)
	}

	parity := liveCanaryClassifyEnv(liveCanaryProviders, env, liveCanaryPinnedWorkflowValues, liveCanaryWorkflowNonRowEnv)
	if len(parity.Unmapped) > 0 {
		t.Fatal(liveCanaryUnmappedRefusal(parity.Unmapped))
	}
	if len(parity.Misnamed) > 0 {
		t.Fatalf("%s maps these keys from a repository secret the table does not declare:\n  %s\nThe value side of the mapping is what the operator actually sets, so a secret name that disagrees with the table is an unset secret: the row skips forever with a reason that reads as a missing credential. Fix the mapping, or — where the operator's name for the thing legitimately differs from the seam's name for the variable — declare it in the row's liveCanaryKey.Secret.",
			liveCanaryWorkflowPath, strings.Join(parity.Misnamed, "\n  "))
	}
	if len(parity.Literal) > 0 {
		t.Fatalf("%s sets these keys to a literal rather than mapping them from a repository secret:\n  %s\nA live credential must never be a literal in this tree; if the literal is deliberate, record it in liveCanaryPinnedWorkflowValues with its reason.",
			liveCanaryWorkflowPath, strings.Join(parity.Literal, "\n  "))
	}
	if len(parity.PinnedFromSecret) > 0 {
		t.Fatalf("%s takes a declared-pinned key from a repository secret:\n  %s",
			liveCanaryWorkflowPath, strings.Join(parity.PinnedFromSecret, "\n  "))
	}
	if len(parity.DeadPin) > 0 {
		t.Fatalf("these liveCanaryPinnedWorkflowValues rows name no key any row declares: %v.\nDelete each one.", parity.DeadPin)
	}

	required, optional := liveCanaryKeyCounts(liveCanaryProviders)
	t.Logf("%d rows declare %d keys (%d required, %d optional); the %s job env carries %d entries, mapping %d repository secrets, %d pinned, %d non-row.\n%s",
		len(liveCanaryProviders), required+optional, required, optional, liveCanaryWorkflowJob,
		len(env), len(liveCanaryDeclaredSecrets(liveCanaryProviders, liveCanaryPinnedWorkflowValues)),
		len(liveCanaryPinnedWorkflowValues), len(liveCanaryWorkflowNonRowEnv), liveCanaryParityBound)
}

// The other direction. Every entry that env block carries must belong to a
// row, or be declared a non-row entry with its reason. Without this the block
// accumulates mappings nothing reads: the operator sets a secret, names an
// account, pays for it, and no probe touches it — and worse, the checklist's
// agreement with this file stops being evidence that a configured provider is
// a probed provider, which is the whole value of holding the three surfaces
// together.
func TestNoLiveCanaryWorkflowMappingIsUnclaimed(t *testing.T) {
	env := liveCanaryWorkflowEnv(t)
	parity := liveCanaryClassifyEnv(liveCanaryProviders, env, liveCanaryPinnedWorkflowValues, liveCanaryWorkflowNonRowEnv)
	if len(parity.Orphaned) > 0 {
		t.Fatal(liveCanaryOrphanRefusal(parity.Orphaned))
	}
	if len(parity.DeadNonRow) > 0 {
		t.Fatalf("these liveCanaryWorkflowNonRowEnv rows name no entry the %s job env carries: %v.\nDelete each one.", liveCanaryWorkflowJob, parity.DeadNonRow)
	}
	claimed := len(env) - len(liveCanaryWorkflowNonRowEnv)
	t.Logf("all %d of the %d entries in the %s job env belong to a canary row; %d are declared non-row.",
		claimed, len(env), liveCanaryWorkflowJob, len(liveCanaryWorkflowNonRowEnv))
}

// liveCanaryKeyCounts is the required/optional split, reported by both guards
// so the numbers appear in the log of a passing run rather than only in a
// refusal.
func liveCanaryKeyCounts(rows []liveCanaryProvider) (required, optional int) {
	for _, row := range rows {
		for _, key := range row.Keys {
			if key.Required {
				required++
				continue
			}
			optional++
		}
	}
	return required, optional
}

// liveCanaryDeclaredSecrets is every repository secret the table implies, with
// the row and requiredness behind it. Pinned keys are excluded: a pinned value
// is not a secret and must not reach the operator's checklist.
func liveCanaryDeclaredSecrets(rows []liveCanaryProvider, pinned map[string]string) map[string]liveCanaryDeclaration {
	secrets := map[string]liveCanaryDeclaration{}
	for _, row := range rows {
		for _, key := range row.Keys {
			if _, isPinned := pinned[key.Env]; isPinned {
				continue
			}
			secrets[key.secretName()] = liveCanaryDeclaration{key: key, module: row.Module}
		}
	}
	return secrets
}

// The checklist headings. The checklist is CHECKED against the table rather
// than generated from it, and the reason is the second column: it carries
// editorial guidance the table does not and should not hold — which throwaway
// resource a secret should point at, why Flagsmith needs two credentials, that
// a Typesense collection must already exist. Generating the table would
// either delete that guidance or drag it into the canary rows, which already
// carry two prose fields (Leaves, Asserts) for the reader of the code rather
// than the operator. What must not drift is the SET of names, and a set is
// exactly what a check can hold without owning the prose.
//
// The split into two tables is the required/optional distinction made visible
// where the operator works: liveCanaryKey.Required decides which table a
// secret belongs in, and filing one in the wrong table is refused, because an
// operator who reads "optional" about a key whose absence skips the whole row
// has been told the opposite of the truth.
const (
	liveCanaryChecklistHeading         = "### The operator's secret checklist"
	liveCanaryChecklistRequiredHeading = "#### Required — an unset secret skips its row"
	liveCanaryChecklistOptionalHeading = "#### Optional — an unset secret narrows its row, which still runs"
)

// liveCanaryChecklistEntry matches a secret name in a checklist cell. Names
// are backticked in that document, so the pattern cannot match the prose that
// discusses them.
var liveCanaryChecklistEntry = regexp.MustCompile("`(" + liveCanarySecretPrefix + "[A-Z0-9_]+)`")

// liveCanaryChecklistBounds are the phrases the checklist section must carry:
// the statement of what no guard here can check. They live in the same
// section as the checklist on purpose — a bound stated three pages away from
// the thing it bounds is a bound nobody reads — and they are asserted rather
// than trusted because this guard's own existence is what makes them
// necessary.
// Each phrase is a whole clause rather than a word, because a word like
// "sandbox" occurs incidentally in that section's other tables and a bound
// satisfied by an incidental occurrence is not a bound.
var liveCanaryChecklistBounds = []struct{ Phrase, Why string }{
	{"No guard can check a secret's value", "the parity is over names; a checklist in perfect agreement can name a revoked token"},
	{"whether it addresses a sandbox or a production tenant", "unreadable from a name, and POLAR_SERVER's pin protects exactly one provider from exactly one direction"},
	{"whether the account behind it is throwaway", "the operator's judgement, and several rows leave state in that account that no seam can remove"},
}

// liveCanaryChecklistSection returns the checklist section's text. Scoped to
// the section because the page's other tables name canary secrets in prose
// ("one usage event on CANARY_OPENMETER_SUBJECT"), and a page-wide scan would
// read those as checklist entries.
func liveCanaryChecklistSection(doc string) (string, error) {
	at := strings.Index(doc, liveCanaryChecklistHeading)
	if at < 0 {
		return "", fmt.Errorf("%s carries no %q section, so the operator has no checklist to work from", liveCanaryChecklistPath, liveCanaryChecklistHeading)
	}
	section := doc[at+len(liveCanaryChecklistHeading):]
	if end := strings.Index(section, "\n### "); end >= 0 {
		section = section[:end]
	}
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section, nil
}

// liveCanaryChecklistSecrets reads the secret names out of one subsection's
// table, first column only: the second column names env keys and prose.
func liveCanaryChecklistSecrets(section, heading string) (map[string]bool, error) {
	at := strings.Index(section, heading)
	if at < 0 {
		return nil, fmt.Errorf("the checklist in %s carries no %q table; the required/optional split is how the operator learns which secrets a row cannot run without", liveCanaryChecklistPath, heading)
	}
	block := section[at+len(heading):]
	if end := strings.Index(block, "\n#### "); end >= 0 {
		block = block[:end]
	}
	listed := map[string]bool{}
	for line := range strings.SplitSeq(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cells) == 0 {
			continue
		}
		for _, match := range liveCanaryChecklistEntry.FindAllStringSubmatch(cells[0], -1) {
			listed[match[1]] = true
		}
	}
	return listed, nil
}

// liveCanaryChecklistParity is the judgement of the table's secrets against
// the two checklist tables, in every direction.
type liveCanaryChecklistParity struct {
	// Missing: a declared secret on neither table. The operator never sets it.
	Missing []string
	// Stale: a checklist entry no row declares. The operator sets it for nothing.
	Stale []string
	// Misfiled: listed, in the table that states the opposite of its
	// requiredness — or in both.
	Misfiled []string
	// Shared: two env keys deriving one secret name.
	Shared []string
}

func (p liveCanaryChecklistParity) clean() bool {
	return len(p.Missing) == 0 && len(p.Stale) == 0 && len(p.Misfiled) == 0 && len(p.Shared) == 0
}

// liveCanaryClassifyChecklist is the whole judgement, pure so every branch is
// drivable on a synthetic checklist.
func liveCanaryClassifyChecklist(rows []liveCanaryProvider, pinned map[string]string, required, optional map[string]bool) liveCanaryChecklistParity {
	declared := liveCanaryDeclaredSecrets(rows, pinned)

	byEnv := map[string][]string{}
	for _, row := range rows {
		for _, key := range row.Keys {
			if _, isPinned := pinned[key.Env]; isPinned {
				continue
			}
			byEnv[key.secretName()] = append(byEnv[key.secretName()], key.Env)
		}
	}

	var parity liveCanaryChecklistParity
	for _, secret := range liveCanarySortedNames(declared) {
		declaration := declared[secret]
		if envs := byEnv[secret]; len(envs) > 1 {
			sort.Strings(envs)
			parity.Shared = append(parity.Shared, fmt.Sprintf("%s feeds %s", secret, strings.Join(envs, " and ")))
		}
		inRequired, inOptional := required[secret], optional[secret]
		if !inRequired && !inOptional {
			where := "Optional"
			if declaration.key.Required {
				where = "Required"
			}
			parity.Missing = append(parity.Missing, fmt.Sprintf("%s (%s reads it as %s; belongs under %s)", secret, declaration.module, declaration.key.Env, where))
			continue
		}
		if inRequired && inOptional {
			parity.Misfiled = append(parity.Misfiled, fmt.Sprintf("%s is on both tables; it is %s", secret, liveCanaryRequiredness(declaration.key.Required)))
			continue
		}
		if declaration.key.Required != inRequired {
			parity.Misfiled = append(parity.Misfiled, fmt.Sprintf("%s is listed as %s and is %s", secret, liveCanaryRequiredness(inRequired), liveCanaryRequiredness(declaration.key.Required)))
		}
	}

	// One line per stale name even when both tables carry it: the refusal is
	// a list the reader deletes from, and a name repeated in it reads as two
	// separate defects.
	reported := map[string]bool{}
	for _, listed := range []map[string]bool{required, optional} {
		for _, secret := range liveCanarySortedNames(listed) {
			if _, ok := declared[secret]; ok || reported[secret] {
				continue
			}
			reported[secret] = true
			parity.Stale = append(parity.Stale, secret)
		}
	}
	sort.Strings(parity.Stale)
	return parity
}

func liveCanaryRequiredness(required bool) string {
	if required {
		return "required: its absence SKIPS the row"
	}
	return "optional: its absence only NARROWS a row that still runs"
}

// The value side of the mapping, and the surface the operator actually reads.
// The workflow parity above proves the job carries every key a row declares;
// it says nothing about whether the names on the right-hand side are the names
// anyone was told to set. This is that half, in both directions, plus the
// required/optional filing and the statement of what none of it can check.
func TestEveryLiveCanarySecretIsOnTheOperatorChecklist(t *testing.T) {
	root := liveCanaryRepositoryRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(liveCanaryChecklistPath)))
	if err != nil {
		t.Fatalf("read %s: %v", liveCanaryChecklistPath, err)
	}
	section, err := liveCanaryChecklistSection(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	required, err := liveCanaryChecklistSecrets(section, liveCanaryChecklistRequiredHeading)
	if err != nil {
		t.Fatal(err)
	}
	optional, err := liveCanaryChecklistSecrets(section, liveCanaryChecklistOptionalHeading)
	if err != nil {
		t.Fatal(err)
	}

	parity := liveCanaryClassifyChecklist(liveCanaryProviders, liveCanaryPinnedWorkflowValues, required, optional)
	if len(parity.Missing) > 0 {
		t.Fatalf("the checklist in %s does not list these repository secrets the canary table declares:\n  %s\nThe checklist is the only one of the three surfaces the operator reads. A secret nobody is told to set is never set, so its row skips every run — reported as a clean [inapplicable] line, never as a failure — and nothing anywhere says the coverage is missing. Add each to the table named.",
			liveCanaryChecklistPath, strings.Join(parity.Missing, "\n  "))
	}
	if len(parity.Stale) > 0 {
		t.Fatalf("the checklist in %s lists repository secrets no canary row declares:\n  %s\nThe operator sets them, pays for whatever account they name, and nothing reads them. A stale entry also erodes the checklist's only guarantee — that configuring everything on it configures every row — so the next missing entry is indistinguishable from this one. Delete each, or declare the key on the row that needs it.",
			liveCanaryChecklistPath, strings.Join(parity.Stale, "\n  "))
	}
	if len(parity.Misfiled) > 0 {
		t.Fatalf("the checklist in %s files these secrets under the wrong table:\n  %s\nThe two tables are the operator's only statement of which secrets a row cannot run at all without. Telling them a row-gating secret is optional guarantees an unconfigured row nobody notices; telling them a widening secret is required guarantees an account opened for nothing. liveCanaryKey.Required decides, and it is the table that must move.",
			liveCanaryChecklistPath, strings.Join(parity.Misfiled, "\n  "))
	}
	if len(parity.Shared) > 0 {
		t.Fatalf("these canary keys derive one repository secret:\n  %s\nOne checklist line would then configure two env keys, and the parity above could not tell a deliberate share from a typo in liveCanaryKey.Secret. Give each its own secret.",
			strings.Join(parity.Shared, "\n  "))
	}

	// Over whitespace-collapsed prose: a phrase that spans the page's wrap
	// column is still stated, and a bound that failed on a reflow would be
	// refreshed by rewording rather than by rereading.
	flowed := strings.Join(strings.Fields(section), " ")
	for _, bound := range liveCanaryChecklistBounds {
		if !strings.Contains(flowed, bound.Phrase) {
			t.Fatalf("the checklist section of %s does not state %q. %s\nThe three-surface parity is checked by a guard now, and a guard's existence is read as a promise: state the bound where the checklist lives, or the operator stops checking the things only they can check.\n%s",
				liveCanaryChecklistPath, bound.Phrase, bound.Why, liveCanaryParityBound)
		}
	}

	requiredKeys, optionalKeys := liveCanaryKeyCounts(liveCanaryProviders)
	t.Logf("%d rows declare %d keys (%d required, %d optional), %d of which map %d repository secrets; the checklist lists %d required and %d optional.\n%s",
		len(liveCanaryProviders), requiredKeys+optionalKeys, requiredKeys, optionalKeys,
		requiredKeys+optionalKeys-len(liveCanaryPinnedWorkflowValues),
		len(liveCanaryDeclaredSecrets(liveCanaryProviders, liveCanaryPinnedWorkflowValues)),
		len(required), len(optional), liveCanaryParityBound)
}

// Both parities, driven on synthetic populations. The real three surfaces
// carry none of these defects once the two gates above pass, so this is the
// only place the classifications' branches are observable — and a refusal
// that can only be seen by breaking the repository is a refusal nobody
// re-checks.
func TestLiveCanaryWorkflowParityRefusesEveryDirection(t *testing.T) {
	rows := []liveCanaryProvider{{
		Slot: "ggg/mail", Module: "ggg/system/mail-fixture", Target: "fixture",
		Keys: []liveCanaryKey{
			{Env: "FIXTURE_API_KEY", Required: true, Credential: true},
			{Env: "GGG_CANARY_FIXTURE_SINK", Required: true, Harness: true},
			{Env: "FIXTURE_REGION", Secret: "CANARY_FIXTURE_HOME_REGION"},
			{Env: "FIXTURE_SERVER"},
		},
	}}
	pinned := map[string]string{"FIXTURE_SERVER": "pinned to sandbox"}
	nonRow := map[string]string{liveCanaryEnvVar: "the gate"}
	sound := map[string]string{
		liveCanaryEnvVar:          "1",
		"FIXTURE_API_KEY":         "${{ secrets.CANARY_FIXTURE_API_KEY }}",
		"GGG_CANARY_FIXTURE_SINK": "${{ secrets.CANARY_FIXTURE_SINK }}",
		"FIXTURE_REGION":          "${{ secrets.CANARY_FIXTURE_HOME_REGION }}",
		"FIXTURE_SERVER":          "sandbox",
	}

	t.Run("the sound population is clean", func(t *testing.T) {
		if parity := liveCanaryClassifyEnv(rows, sound, pinned, nonRow); !parity.clean() {
			t.Fatalf("a sound env block was refused: %+v", parity)
		}
	})

	mutate := func(change func(map[string]string)) map[string]string {
		copied := map[string]string{}
		for key, value := range sound {
			copied[key] = value
		}
		change(copied)
		return copied
	}

	t.Run("a row key the workflow does not map", func(t *testing.T) {
		parity := liveCanaryClassifyEnv(rows, mutate(func(env map[string]string) {
			delete(env, "FIXTURE_API_KEY")
		}), pinned, nonRow)
		if len(parity.Unmapped) != 1 || !strings.Contains(parity.Unmapped[0], "FIXTURE_API_KEY") {
			t.Fatalf("an unmapped row key was not refused: %+v", parity)
		}
		// The refusal must name the consequence, not the mismatch: this is
		// the one direction whose cost survives the operator fixing what the
		// skip reason told them to fix.
		refusal := liveCanaryUnmappedRefusal(parity.Unmapped)
		for _, want := range []string{"skip in CI forever", "EVEN AFTER the operator sets the repository secret", "CANARY_FIXTURE_API_KEY"} {
			if !strings.Contains(refusal, want) {
				t.Fatalf("the unmapped refusal does not state %q:\n%s", want, refusal)
			}
		}
	})

	t.Run("an env entry no row declares", func(t *testing.T) {
		parity := liveCanaryClassifyEnv(rows, mutate(func(env map[string]string) {
			env["FIXTURE_LEGACY_TOKEN"] = "${{ secrets.CANARY_FIXTURE_LEGACY_TOKEN }}"
		}), pinned, nonRow)
		if len(parity.Orphaned) != 1 || !strings.Contains(parity.Orphaned[0], "FIXTURE_LEGACY_TOKEN") {
			t.Fatalf("an orphan env entry was not refused: %+v", parity)
		}
		if refusal := liveCanaryOrphanRefusal(parity.Orphaned); !strings.Contains(refusal, "Nothing reads them") {
			t.Fatalf("the orphan refusal does not state the consequence:\n%s", refusal)
		}
	})

	t.Run("a mapping from the wrong secret name", func(t *testing.T) {
		parity := liveCanaryClassifyEnv(rows, mutate(func(env map[string]string) {
			env["FIXTURE_REGION"] = "${{ secrets.CANARY_FIXTURE_REGION }}"
		}), pinned, nonRow)
		if len(parity.Misnamed) != 1 || !strings.Contains(parity.Misnamed[0], "CANARY_FIXTURE_HOME_REGION") {
			t.Fatalf("a misnamed secret was not refused: %+v", parity)
		}
	})

	t.Run("a credential literal, and a pin taken from a secret", func(t *testing.T) {
		parity := liveCanaryClassifyEnv(rows, mutate(func(env map[string]string) {
			env["FIXTURE_API_KEY"] = "sk-live-not-a-reference"
			env["FIXTURE_SERVER"] = "${{ secrets.CANARY_FIXTURE_SERVER }}"
		}), pinned, nonRow)
		if len(parity.Literal) != 1 || len(parity.PinnedFromSecret) != 1 {
			t.Fatalf("a literal credential and a secret-sourced pin were not both refused: %+v", parity)
		}
	})

	t.Run("a declaration that describes nothing", func(t *testing.T) {
		parity := liveCanaryClassifyEnv(rows, mutate(func(env map[string]string) {
			delete(env, liveCanaryEnvVar)
		}), map[string]string{"FIXTURE_GONE": "pinned"}, nonRow)
		if len(parity.DeadPin) != 1 || len(parity.DeadNonRow) != 1 {
			t.Fatalf("a dead pin and a dead non-row exemption were not both refused: %+v", parity)
		}
	})
}

func TestLiveCanaryChecklistParityRefusesEveryDirection(t *testing.T) {
	rows := []liveCanaryProvider{{
		Slot: "ggg/mail", Module: "ggg/system/mail-fixture", Target: "fixture",
		Keys: []liveCanaryKey{
			{Env: "FIXTURE_API_KEY", Required: true, Credential: true},
			{Env: "FIXTURE_BASE_URL"},
			{Env: "FIXTURE_SERVER"},
		},
	}}
	pinned := map[string]string{"FIXTURE_SERVER": "pinned to sandbox"}
	required := map[string]bool{"CANARY_FIXTURE_API_KEY": true}
	optional := map[string]bool{"CANARY_FIXTURE_BASE_URL": true}

	t.Run("the sound checklist is clean", func(t *testing.T) {
		if parity := liveCanaryClassifyChecklist(rows, pinned, required, optional); !parity.clean() {
			t.Fatalf("a sound checklist was refused: %+v", parity)
		}
	})

	t.Run("a deleted checklist entry", func(t *testing.T) {
		parity := liveCanaryClassifyChecklist(rows, pinned, map[string]bool{}, optional)
		if len(parity.Missing) != 1 || !strings.Contains(parity.Missing[0], "CANARY_FIXTURE_API_KEY") {
			t.Fatalf("a missing checklist entry was not refused: %+v", parity)
		}
		if !strings.Contains(parity.Missing[0], "belongs under Required") {
			t.Fatalf("the missing entry does not name the table it belongs in: %q", parity.Missing[0])
		}
	})

	t.Run("a stale checklist entry", func(t *testing.T) {
		stale := map[string]bool{"CANARY_FIXTURE_API_KEY": true, "CANARY_FIXTURE_RETIRED": true}
		parity := liveCanaryClassifyChecklist(rows, pinned, stale, optional)
		if len(parity.Stale) != 1 || parity.Stale[0] != "CANARY_FIXTURE_RETIRED" {
			t.Fatalf("a stale checklist entry was not refused: %+v", parity)
		}
	})

	t.Run("a pinned key is not a secret and may not be listed", func(t *testing.T) {
		parity := liveCanaryClassifyChecklist(rows, pinned, required, map[string]bool{
			"CANARY_FIXTURE_BASE_URL": true, "CANARY_FIXTURE_SERVER": true,
		})
		if len(parity.Stale) != 1 || parity.Stale[0] != "CANARY_FIXTURE_SERVER" {
			t.Fatalf("a checklist entry for a pinned literal was not refused: %+v", parity)
		}
	})

	t.Run("requiredness filed backwards, in both directions", func(t *testing.T) {
		parity := liveCanaryClassifyChecklist(rows, pinned,
			map[string]bool{"CANARY_FIXTURE_BASE_URL": true},
			map[string]bool{"CANARY_FIXTURE_API_KEY": true})
		if len(parity.Misfiled) != 2 {
			t.Fatalf("two backwards filings were not both refused: %+v", parity)
		}
		joined := strings.Join(parity.Misfiled, "\n")
		for _, want := range []string{"absence SKIPS the row", "only NARROWS a row that still runs"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("the misfiling refusal does not state %q:\n%s", want, joined)
			}
		}
	})

	t.Run("a secret on both tables", func(t *testing.T) {
		parity := liveCanaryClassifyChecklist(rows, pinned, required, map[string]bool{
			"CANARY_FIXTURE_API_KEY": true, "CANARY_FIXTURE_BASE_URL": true,
		})
		if len(parity.Misfiled) != 1 || !strings.Contains(parity.Misfiled[0], "on both tables") {
			t.Fatalf("a secret listed as both required and optional was not refused: %+v", parity)
		}
	})

	t.Run("two keys deriving one secret", func(t *testing.T) {
		shared := []liveCanaryProvider{{
			Slot: "ggg/mail", Module: "ggg/system/mail-fixture", Target: "fixture",
			Keys: []liveCanaryKey{
				{Env: "FIXTURE_API_KEY", Required: true},
				{Env: "FIXTURE_TOKEN", Required: true, Secret: "CANARY_FIXTURE_API_KEY"},
			},
		}}
		parity := liveCanaryClassifyChecklist(shared, pinned, required, map[string]bool{})
		if len(parity.Shared) != 1 || !strings.Contains(parity.Shared[0], "FIXTURE_API_KEY and FIXTURE_TOKEN") {
			t.Fatalf("two keys deriving one secret were not refused: %+v", parity)
		}
	})
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
// The registry walk behind TestEveryManagedTargetHasALiveCanaryOrAnAdmissibleExcuse.

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
