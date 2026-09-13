# Task BF — Retire `feature-flags-launchdarkly`, ship `feature-flags-flagsmith`

Slice owner: `FlagsmithTarget`. Companion slice: `AdmissionRule` (canary, `AGENTS.md`,
the rule's own doc section). Branch `main`.

The decision this slice executes: **a managed target is admissible only if the seam can
be satisfied through a documented HTTP contract in which the provider performs the
decision** — no vendored SDK, no reimplementation of vendor semantics.
`ggg/system/feature-flags-launchdarkly` failed that rule and could not boot;
`ggg/system/feature-flags-flagsmith` satisfies it and is now the slot's managed
reference target.

---

## 1. The seven-method mapping

The seam is `internal/flags/contract.go:19-27`. Every method below is implemented in
`internal/flags/flagsmith/flagsmith.go` and asserted in `internal/flags/flagsmith/flagsmith_test.go`.
**No method returns `flags.ErrReadOnly`** — that was the entire reason for choosing this
vendor, and `TestServiceSatisfiesTheWholeSeamWithoutErrReadOnly` asserts it directly by
pointing a configured adapter at a dead port and requiring every mutation to fail with a
*transport* error rather than a read-only sentinel.

Two APIs, two credentials, documented as such at
<https://docs.flagsmith.com/integrating-with-flagsmith/flagsmith-api-overview>:
the Flags API authenticates with the **public, non-secret Environment Key** in
`X-Environment-Key` ("Open by design. The Environment Key can be exposed in client-side
code"), and the Management API authenticates with a **secret organisation API token** sent
as `Authorization: Api-Key <token>`
(<https://docs.flagsmith.com/integrating-with-flagsmith/flagsmith-api-overview/admin-api/authentication>:
"The token should be prefixed with `Api-Key`").

| # | Seam method | API | Request | Doc URL |
|---|---|---|---|---|
| 1 | `Enabled(ctx, orgID, key) bool` | Flags | `POST /api/v1/identities/` + `X-Environment-Key`, body `{"identifier":"<orgID>"}`. Doc: "Lazily creates an identity… Receives the flags for that identity, including any segment or identity-specific overrides." With an empty orgID instead: `GET /api/v1/flags/` (environment defaults). | <https://docs.flagsmith.com/integrating-with-flagsmith/flagsmith-api-overview/flags-api/code-examples>, <https://docs.flagsmith.com/integrating-with-flagsmith/flagsmith-api-overview/flags-api> |
| 2 | `List(ctx) ([]Flag, error)` | Management | `GET /api/v1/projects/{project_pk}/features/?environment=<envID>`. The `environment` query is what returns `environment_feature_state` per feature — the published operation description says "Pass `environment=<id>` to also get each feature's live state for that environment in `environment_feature_state`" — which is what makes `List` one call rather than one per flag. | `list_project_features`, <https://api.flagsmith.com/api/v1/docs/> (spec: <https://api.flagsmith.com/api/v1/swagger.json>) |
| 3a | `Upsert` — create | Management | `POST /api/v1/projects/{project_pk}/features/` body `{name, description, default_enabled, initial_value}` → `201` | `create_feature` ("Creates a new feature flag in the specified project"), <https://api.flagsmith.com/api/v1/docs/> |
| 3b | `Upsert` — description | Management | `PUT /api/v1/projects/{project_pk}/features/{id}/` body `{description}` → `200` | `update_feature`, <https://api.flagsmith.com/api/v1/docs/> |
| 3c | `Upsert` — enabled | Management | `PUT /api/v1/environments/{environment_api_key}/featurestates/{id}/` body `{feature, enabled}` → `200` | `update_environment_feature_state`, <https://api.flagsmith.com/api/v1/docs/> |
| 4 | `Delete(ctx, key)` | Management | `DELETE /api/v1/projects/{project_pk}/features/{id}/` → `204` | `api_v1_projects_features_destroy`, <https://api.flagsmith.com/api/v1/docs/> |
| 5 | `ListOverrides(ctx, key)` | Management | `GET /api/v1/environments/{environment_api_key}/featurestates/?feature_name=<key>&anyIdentity=1`. `anyIdentity` is the provider's own filter for precisely this question: "Pass any value to get results that have an identity override. Do not pass for default behaviour." Each row carries `identity:{id,identifier}`. | `api_v1_environments_featurestates_list`, <https://api.flagsmith.com/api/v1/docs/> |
| 6a | `SetOverride` — resolve identity | Management | `GET /api/v1/environments/{environment_api_key}/identities/?q="<orgID>"`, then `POST /api/v1/environments/{environment_api_key}/identities/` body `{identifier}` → `201` when absent | `api_v1_environments_identities_list` / `_create`, <https://api.flagsmith.com/api/v1/docs/>; `q` semantics from source, see §2.3 |
| 6b | `SetOverride` — write | Management | `POST /api/v1/environments/{environment_api_key}/identities/{identity_pk}/featurestates/` body `{feature, enabled}` → `201`, or `PUT …/featurestates/{id}/` → `200` when one exists | `api_v1_environments_identities_featurestates_create` / `_update`, <https://api.flagsmith.com/api/v1/docs/> |
| 7 | `DeleteOverride(ctx, key, orgID)` | Management | `DELETE /api/v1/environments/{environment_api_key}/identities/{identity_pk}/featurestates/{id}/` → `204` | `api_v1_environments_identities_featurestates_destroy`, <https://api.flagsmith.com/api/v1/docs/> |

The `3a/3b/3c` split is **forced by the provider's schemas, not chosen**. On create
(`ListFeature`) `name`, `description`, `default_enabled` and `initial_value` are all
writable; on update (`UpdateFeature`) `name`, `default_enabled` and `initial_value` are
`readOnly: true`. So an existing feature's on/off state is reachable only through its
environment feature state, and `Upsert` issues `3b`/`3c` only when the respective value
actually differs (`TestUpsertLeavesAnUnchangedFeatureAlone` proves the no-op case issues
neither PUT).

### 1.1 Quoted assertions

**Evaluation** (`TestEnabledIdentifiesTheOrganisationAndReturnsTheProvidersDecision`):

```
assert.Equal(t, "POST /api/v1/identities/", captured.line())
assert.Equal(t, "ser.env_key_test", captured.environmentKey)   // X-Environment-Key
assert.Equal(t, "application/json", captured.contentType)
assert.Empty(t, captured.authorization)                        // the SECRET token must not travel here
...
assert.Equal(t, map[string]any{"identifier": "org_1"}, body)
```

**List** (`TestListReadsTheProjectFeaturesInEnvironmentContext`):

```
assert.Equal(t, "GET /api/v1/projects/7/features/?environment=42", captured.line())
assert.Equal(t, "Api-Key tok_management_test", captured.authorization)
assert.Empty(t, captured.environmentKey)                       // public key must not authorise a mutating API
assert.Empty(t, captured.body)
```

**Mutation — identity override create** (`TestSetOverrideCreatesAnIdentityOverrideForAKnownOrganisation`):

```
assert.Equal(t, `GET /api/v1/environments/ser.env_key_test/identities/?q=%22org_1%22`, identity.line())
assert.Equal(t, "POST /api/v1/environments/ser.env_key_test/identities/31/featurestates/", created.line())
assert.Equal(t, "Api-Key tok_management_test", created.authorization)
...
assert.Equal(t, map[string]any{"feature": float64(11), "enabled": true}, body)
```

**Mutation — feature create** (`TestUpsertCreatesAnAbsentFeature`):

```
assert.Equal(t, "POST /api/v1/projects/7/features/", created.line())
assert.Equal(t, map[string]any{
    "name": "beta-dashboard", "description": "New dashboard",
    "default_enabled": true, "initial_value": "",
}, body)
```

The `fake` handler is route-table driven and records every unrouted path; each test ends
with `f.requireAllRouted()`, so a request to a path Flagsmith does not publish fails the
assertion instead of silently returning 200.

---

## 2. The override mapping, its justification, and its limits

### 2.1 Identity, not segment

The seam's unit of targeting is an organisation id: `Enabled(ctx, orgID, key)` and
`Override{OrgID, Enabled}`. This adapter **identifies the org to Flagsmith** and therefore
maps a per-org override to a Flagsmith **identity override**.

Justification from the docs, in order of weight:

1. **Precedence is an exact match.** Flagsmith: "Identity overrides always take precedence
   over segment overrides. Simply put, the order of precedence when evaluating a flag is:
   1. Identity overrides 2. Segment overrides 3. Default value for the current environment"
   (<https://docs.flagsmith.com/flagsmith-concepts/segments#flag-evaluation-precedence>).
   The seam's fixed order (`internal/flags/flags.go:99-102`) is: per-org override wins over
   everything, else `enabled && rollout`. Identity overrides are the only Flagsmith object
   that sits above everything else, which is what "wins over everything" requires.
2. **Cardinality is exact.** One org ↔ one identity ↔ at most one feature state per feature.
   `ListOverrides` is then a single provider-side query (`anyIdentity`), not a reconstruction.
3. **A segment would be the wrong object.** Segments are "defined by a set of rules that
   match identity traits" (<https://docs.flagsmith.com/flagsmith-concepts/segments>), so
   "org X specifically" means one segment per org carrying an equality rule — against a
   documented ceiling of **500 segments per project / 2000 segment overrides per environment**
   (<https://docs.flagsmith.com/flagsmith-concepts/segments/segment-rule-operators#limits>) —
   and Flagsmith would still rank it *below* an identity override, so it could not express
   "wins over everything" anyway.

### 2.2 What the mapping cannot express

1. **`Flag.Rollout`.** Flagsmith has no rollout field on a feature; a gradual rollout there
   is a **segment with a `% Split` condition plus a segment override**
   (<https://docs.flagsmith.com/flagsmith-concepts/segments/segment-rule-operators>,
   <https://docs.flagsmith.com/managing-flags/rollout/rollout-by-percentage>). Confirmed
   mechanically against the spec: on `UpdateFeature` the only writable fields are
   `description`, `tags`, `is_archived`, `metadata`, `is_server_key_only`, and on
   `FeatureStateSerializerBasic` / `FeatureStateSerializerCreate` the writable set is
   `{feature, enabled}` with `feature_state_value` marked `readOnly`. There is no field to
   put a percentage in.

   So the adapter **refuses** rather than accepting and dropping it:
   `flagsmith.ErrRolloutUnsupported`, raised by `Upsert` for any rollout other than 100,
   before any provider call (`TestUpsertRefusesAPartialRolloutByNameRatherThanDroppingIt`
   asserts `assert.Empty(t, f.lines())` — a rejected rollout cannot leave a half-written
   feature). `List` reports `Rollout: 100` for every flag, so the accepted domain and the
   reported domain are the same set and the round trip is exact.

   This is deliberately **not** `flags.ErrReadOnly`: it is one field's value range, not a
   read-only seam. The `PERCENTAGE_SPLIT` object graph *is* reachable through the Management
   API (`POST /api/v1/projects/{project_pk}/segments/` with a
   `{operator:"PERCENTAGE_SPLIT", value:"50"}` condition, then
   `POST /api/v1/environments/{environment_api_key}/features/{feature_pk}/create-segment-override/`)
   and was deliberately left out of this slice — see §6 for the evidence that settled it.

2. **Identity deletion.** The seam has no identity concept, so identities this adapter
   creates (`SetOverride` → `ensureIdentity`, and the Flags API's documented lazy create on
   evaluation) are never removed. `DeleteOverride` removes the *override*, not the identity.
   This is why the live-canary row is classified `liveCanaryLeavesBounded` rather than
   `liveCanaryCleans` — `AdmissionRule` made that correction and was right.

3. **Enumerating overrides is bounded by pagination, not by a full scan.** `ListOverrides`
   relies on `anyIdentity`, so it reads only identity-override rows for one feature rather
   than paging every identity. `paginate` caps the follow loop at `pageLimit = 50` pages and
   returns a named error past it, rather than looping forever on a provider that always
   returns `next`.

4. **Evaluation is a network call.** Flagsmith performs the decision, which is the whole
   point, so decisions are cached for a fixed `decisionTTL = 30s` through the declared
   `cache.store` capability — one `POST /api/v1/identities/` answers every key for one org
   (`TestEnabledCachesOneProviderCallForEveryKeyOfOneOrganisation`). `cache.Store` has no
   prefix delete, so a mutation cannot invalidate a per-identity entry: a changed flag or
   override becomes visible **within 30s**, where the Postgres adapter's admin mutations
   invalidate immediately. This bound is documented in the package comment, in
   `content/docs/feature-flags.md`, and asserted by `Health` refusing a cache-less adapter.

### 2.3 One undocumented-in-spec detail, verified in source

`GET /api/v1/environments/{environment_api_key}/identities/` accepts a `q` filter that the
published OpenAPI document does not list (its parameters are only `page`, `page_size`). It
is in the deployed implementation, where a **double-quoted** query is an exact identifier
match:

```python
# api/environments/identities/views.py — IdentityViewSet.get_queryset
search_query = self.request.query_params.get("q")
if search_query:
    if search_query.startswith('"') and search_query.endswith('"'):
        # Quoted searches should do an exact match just like Google
        queryset = queryset.filter(identifier__exact=search_query.replace('"', ""))
```

<https://raw.githubusercontent.com/Flagsmith/flagsmith/main/api/environments/identities/views.py>

Because that is implementation rather than published contract, the adapter sends
`?q="<orgID>"` **and re-checks the identifier in Go**, so a provider that ever degraded the
quoted form to fuzzy still could not override the wrong organisation
(`findIdentity`, and `TestSetOverrideCreatesAnIdentityOverrideForAKnownOrganisation`
asserts the quoted query line).

---

## 3. Missing-credential boot refusal

Per the plan's rule, an explicitly selected managed adapter with missing credentials
returns **one joined boot error** and never falls back locally.
`TestNewModuleRefusesEveryMissingCredentialInOneError` runs `NewModule` against an empty
host and asserts the single returned error names all four required keys:

```
flagsmith: FLAGSMITH_ENVIRONMENT_KEY is required (public Environment Key, Flags API)
FLAGSMITH_MANAGEMENT_TOKEN is required (secret organisation API token, Management API)
FLAGSMITH_PROJECT_ID is required (numeric Management API id)
FLAGSMITH_ENVIRONMENT_ID is required (numeric Management API id)
```

(one `errors.Join`, newline-separated, wrapped once as `flagsmith: %w`). Two further
assertions keep it a refusal rather than a degradation: no `*Module` is returned, and
`TestNewModuleRefusesAMalformedNumericCoordinate` distinguishes a blank coordinate from a
malformed one — `FLAGSMITH_PROJECT_ID must be a positive integer, got "seven"` and
`FLAGSMITH_ENVIRONMENT_ID must be a positive integer, got "0"` arrive in the same joined
error, because they are different operator mistakes and a boot should name both.

`Service.ready()` is the same check for a hand-built `Service`, and
`TestReadyNamesEveryMissingCoordinateForAHandBuiltService` asserts it returns
`errors.Join` with exactly four wrapped causes rather than the first failure.

**Health** follows the seam's siblings (`internal/flags/postgres/postgres.go:83`,
`internal/search/typesense/typesense.go:141`): configuration and transport, no provider
round trip — `TestHealthMatchesTheSeamSiblingsAndMakesNoProviderCall` points the adapter at
an httptest server whose handler calls `t.Fatal`, so a health check that spent a request
would fail the test. It additionally refuses a cache-less adapter (see §2.2.4) and returns
`ctx.Err()` on a cancelled context.

---

## 4. Self-hosted target: declared **no**, and why

Flagsmith is open source and self-hostable, and I still publish **one** target, `flagsmith`,
`mode: "managed"`, `environments: ["production"]`.

Reason, from Flagsmith's own Docker guide
(<https://docs.flagsmith.com/deployment-self-hosting/hosting-guides/docker>): the deployment
is two containers — the combined dashboard/API plus **"PostgreSQL Database … The REST API
stores all its data within a PostgreSQL database"** — and then, before anything is usable,
**"As a first step, you will need to create a new account at http://localhost:8000/signup"**.
An Environment Key, a numeric project id and a numeric environment id are all minted by that
signup-and-create flow. A `local_service` block whose `health` probe passed while no
`FLAGSMITH_ENVIRONMENT_KEY` could exist would ship a target that looks selectable and cannot
boot the adapter — which is exactly the defect `v0.26.0` was about. The slot already has a
zero-account path that needs no signup at all (`feature-flags-postgres@postgres`, selected
for development, test *and* production), so a second local option would buy nothing and cost
a lie.

An operator who *does* run their own Flagsmith is served by the same target: both
`FLAGSMITH_API_URL` and `FLAGSMITH_FLAGS_API_URL` are endpoint overrides (SaaS splits the two
APIs across `api.flagsmith.com` and `edge.api.flagsmith.com`; a self-hosted deployment serves
both from one host), and `TestEndpointOverridesSelectASelfHostedDeployment` asserts both are
honoured and trailing-slash normalised. This is stated in the manifest input labels and in
`content/docs/feature-flags.md`.

---

## 5. Removal sweep

Deleted outright:

- `internal/flags/launchdarkly/launchdarkly.go`, `internal/flags/launchdarkly/launchdarkly_test.go`
- `registry/modules/system/feature-flags-launchdarkly/module.json` (the whole directory)

Rewired:

| Path | Change |
|---|---|
| `registry/systems.json:34` | index entry swapped to `feature-flags-flagsmith` (sort position unchanged) |
| `registry/profiles/full.json:221` | member swapped; `:10` description's managed-adapter list now names `feature-flags-flagsmith`. Counts hold — one module out, one in, so 288 of 297 is still true |
| `registry/profiles/saas.json:225` | member swapped |
| `internal/web/page_admin_flags.go:21` | `LAUNCHDARKLY_CONSOLE` → `FLAGSMITH_CONSOLE` |
| `internal/web/workflow_admin_flags.go:195` | same |
| `README.md:104` | matrix "Other targets" → `feature-flags-flagsmith@flagsmith` |
| `content/docs/getting-started.md:228` | managed-adapters-not-selected list |
| `content/docs/roadmap.md:20` | managed references row: LaunchDarkly → Flagsmith |
| `content/docs/testing.md:680-687` | the "no zero-account server" list drops LaunchDarkly; Flagsmith is called out as the one managed target that *does* publish a self-hostable server and still has no tier-1 container, with the reason |
| `content/docs/testing.md:716-729` | the rule's founding-incident narrative made unambiguously past tense, naming its replacement |
| `content/docs/feature-flags.md` | new "Managed target" section; the intro no longer claims "no external service" |

**Not touched, by agreement with `AdmissionRule`** (their manifests, their bumps):
`internal/canary/**`, `internal/modkit/remove.go`, `internal/modkit/testdata/redproof/**`,
`AGENTS.md`, `.github/workflows/live-canary.yml`,
`registry/modules/system/{modkit,project-docs,ci-github}/module.json`.

### 5.1 Proof nothing dangles

```
$ grep -rn 'LAUNCHDARKLY_' . --exclude-dir=.git --exclude-dir=tmp --exclude-dir=.superpowers
(no matches)
$ grep -rn 'flags/launchdarkly' . --exclude-dir=.git --exclude-dir=tmp --exclude-dir=.superpowers
(no matches)
$ grep -rn '@launchdarkly' . --exclude-dir=.git --exclude-dir=tmp --exclude-dir=.superpowers
(no matches)
```

Zero environment keys, zero Go package references, zero `adapter@target` strings. The module
**id** survives in exactly three populations, none of them live:

1. **Deliberate retirement history**, because the retirement *is* the rule's rationale:
   `AGENTS.md:334`, `content/docs/testing.md:717`,
   `internal/canary/live_canary_selfhost_test.go:89` and `:343`,
   `internal/modkit/testdata/redproof/own-canary-target-unprobed.patch` (prose),
   `internal/flags/flagsmith/flagsmith_test.go` (one comment naming what this vendor
   replaced), and `registry/modules/system/feature-flags-flagsmith/module.json:10`
   (the new module's description states what it replaces). `content/docs/testing.md:717`
   was rewritten to read "**retired, and no longer in this catalog**" and to state that
   every surviving mention is history, so a future search cannot mistake it for live.
2. **Generated outputs**, stale by design and regenerated by the release owner:
   `gogogadget.lock.json` (lock tombstone + the module's recorded payloads) and
   `registry.snapshot.json` (signed payload index). Per the slice constraints I did **not**
   run `ggg registry build` / `sign` / `sync`, so these stay stale until the release order
   runs. The same applies to the generated `content/docs/configuration-reference.md` and
   `.env.example`, which will gain the four required `FLAGSMITH_*` keys on the next `sync`
   (LaunchDarkly's keys were both `required: false`, so they never appeared there).
3. **`.superpowers/sdd/**` task reports**, including this one.

### 5.2 Profile / selection decision

**No shipped profile ever *selected* LaunchDarkly**, so nothing had to be repointed: it was a
profile *member* (installed, compiled, unselected), and `ggg/feature-flags` resolved to
`feature-flags-postgres@postgres` for development, test **and** production in
`gogogadget.json`, `registry/profiles/full.json`, `saas.json`, `web.json` and `minimal.json`.

I kept those selections exactly as they were, and that is a decision rather than an omission.
The repo's convention for *product-infrastructure* slots is already explicit: `ggg/search`,
`ggg/notifications`, `ggg/webhooks` and `ggg/usage` all default production to their Postgres
adapter while publishing an unselected managed adapter beside it — `full.json:10` documents
precisely this ("Six are the managed adapters this profile's provider defaults do not
select"). Feature flags belong to that family. Defaulting production to Flagsmith would make
a paid third-party account a prerequisite for `ggg new --profile full` in production, for a
capability the framework already serves transactionally.

What changed is the thing that was actually broken: the unselected managed member is now one
that **can** boot when an operator runs
`ggg provider set ggg/feature-flags production feature-flags-flagsmith@flagsmith`.
Previously that command produced a project that could not start.

### 5.3 `flags.ErrReadOnly` — kept, deliberately

After this slice no in-tree adapter returns `flags.ErrReadOnly`; it survives at
`internal/flags/contract.go:8` plus my two negative assertions. I kept it rather than
deleting it: it is part of the `ggg/system/feature-flags` seam's published contract (the plan
specifies "Unsupported mutations return `flags.ErrReadOnly`; admin renders read-only state
plus provider-console link"), and the seam is consumed by third-party adapter modules under
step 4/10 of the plan. Removing it is a seam `contract` bump and a breaking change for an
external adapter, to delete four lines. Flagging it here rather than doing it silently.

---

## 6. Scope decision I want on the record: percentage-split segments

I came close to implementing `Flag.Rollout` properly via feature-specific segments
(`POST /api/v1/projects/{project_pk}/segments/` with a `PERCENTAGE_SPLIT` condition +
`create-segment-override/`), and the mapping would have been genuinely exact: `% Split`
"deterministically assigns a bucket to each identity solely based on its ID", and this
adapter's identity *is* the org, so it buckets per-org exactly as
`fnv32(orgID + "|" + key) % 100 < N` does.

What settled it against, with evidence: **the admin rollout control cannot reach any
non-Postgres adapter today.** `internal/web/workflow_admin_flags.go:36-48` starts with
`s.q.GetFeatureFlag(ctx, key)` and `http.NotFound`s on error — the Postgres table, not the
seam. `handleAdminFlagToggle:21` and `handleAdminFlagDelete:129` do the same. Under a
Flagsmith selection every one of those paths 404s before the seam is consulted. Building a
three-object orchestration (segment + feature-segment + segment feature state), written blind
against a spec with no live instance and no integration test, to serve a control that cannot
call it, would have been the `v0.26.0` failure mode in new clothes.

**Two follow-ups this leaves**, neither in this slice's ownership:

1. `internal/web`'s admin flag handlers read `s.q` (Postgres) for existence checks while
   writing through `flags.Service`. Confirmed independently by `AdmissionRule`: **five of
   the six admin mutations** gate on the Postgres row before the seam —
   `workflow_admin_flags.go` toggle `:21`, rollout `:40`, create `:110`, delete `:129`,
   override-set `:148` — each `http.NotFound` when `s.q.GetFeatureFlag` misses. So under a
   Flagsmith selection the read path renders fine (`List` goes to the provider) and every
   one of those controls 404s. Fixing it is a design decision, not a patch: it needs the
   seam to say which mutations the provider owns and which the Postgres ledger owns.
2. Once (1) is settled, `Upsert` can grow the percentage-split segment path and
   `ErrRolloutUnsupported` can go. The endpoints are verified and named above.

**This is the admission rule's first dividend, and it is worth stating plainly.** The dead
target was not merely useless — it was *camouflage*. Because selecting
`feature-flags-launchdarkly` could not boot, nobody ever ran the admin flag surface against
a non-Postgres adapter, so a seam the product code does not honour looked healthy for a
release. Requiring a managed target to be probeable-or-retired is what made the defect
surface in the slice that adds a working adapter, rather than in someone's production.

---

## 7. Revision bumps applied in this commit

| Module | Revision | Why |
|---|---|---|
| `ggg/system/feature-flags-flagsmith` | — → **1** | new module |
| `ggg/system/feature-flags-launchdarkly` | **deleted** | manifest and payloads removed |
| `ggg/page/admin-flags` | 4 → **5** | `internal/web/page_admin_flags.go` re-hashed |
| `ggg/workflow/admin-flags` | 4 → **5** | `internal/web/workflow_admin_flags.go` re-hashed |
| `ggg/system/content-assets` | 44 → **45** | `content/docs/{feature-flags,getting-started,roadmap,testing}.md` all re-hashed |
| `ggg/profile/full` | 2 → **3** | member list + description |
| `ggg/profile/saas` | 2 → **3** | member list |

`registry/systems.json` is an index and carries no revision. `ggg/system/feature-flags`
(the seam) has no payload change and is correctly **not** bumped.

Handed to `AdmissionRule` over `hub` rather than edited here, because they own the manifests
and had already bumped them: `ggg/system/project-docs` (revision 23, already above the
published 22) needs both payload digests refreshed —
`AGENTS.md` → `3fffec6a8a23cb3abcf292e05b749cceff657fb484f8d1b0c11b544892c0e51e` (their edit)
and `README.md` → `2e322048d54c9bd6ed5cbbb13c1b25b1d719042e89eedc3e7f0dd9aced27ee2d` (mine).
They also own `ggg/system/modkit` (89) and `ggg/system/ci-github` (9).

---

## 8. Verification

All quoted verbatim.

```
$ go build ./...
(no output)

$ go vet ./internal/flags/... ./internal/web/...
(no output)

$ gofmt -l internal/flags internal/web
(no output)

$ go test ./internal/flags/... -count=1
ok  	github.com/gogogadget/gogogadget/internal/flags	3.753s
ok  	github.com/gogogadget/gogogadget/internal/flags/flagsmith	0.369s
ok  	github.com/gogogadget/gogogadget/internal/flags/postgres	0.323s

$ go test ./internal/modkit -run 'TestPublishedSchemaInstancesValidate|TestEveryModuleChangedSinceTheLastReleaseCarriesAHigherRevision|TestPublishedSchemaAndValidatorAgreeOnEnumTargetInputs' -count=1
ok  	github.com/gogogadget/gogogadget/internal/modkit	0.972s
```

The 30 cases in `internal/flags/flagsmith`, verbose:

```
--- PASS: TestEnabledIdentifiesTheOrganisationAndReturnsTheProvidersDecision (0.00s)
--- PASS: TestEnabledIsFalseForADisabledFlagAndForAnUnknownKey (0.00s)
--- PASS: TestEnabledWithNoOrganisationReadsTheEnvironmentDefaults (0.00s)
--- PASS: TestEnabledIsFalseAndLoggedWhenTheProviderFails (0.00s)
--- PASS: TestEnabledCachesOneProviderCallForEveryKeyOfOneOrganisation (0.00s)
--- PASS: TestListReadsTheProjectFeaturesInEnvironmentContext (0.00s)
--- PASS: TestListFollowsPaginationAndSurfacesProviderErrors (0.00s)
--- PASS: TestUpsertCreatesAnAbsentFeature (0.00s)
--- PASS: TestUpsertUpdatesDescriptionAndEnvironmentStateOfAnExistingFeature (0.00s)
--- PASS: TestUpsertLeavesAnUnchangedFeatureAlone (0.00s)
--- PASS: TestUpsertRefusesAPartialRolloutByNameRatherThanDroppingIt (0.00s)
--- PASS: TestDeleteRemovesTheFeatureFromTheProject (0.00s)
--- PASS: TestDeleteIsIdempotentForAnAbsentKey (0.00s)
--- PASS: TestListOverridesAsksTheProviderForIdentityOverridesOnly (0.00s)
--- PASS: TestListOverridesReturnsEmptyRatherThanNilForAFlagWithNone (0.00s)
--- PASS: TestSetOverrideCreatesAnIdentityOverrideForAKnownOrganisation (0.00s)
--- PASS: TestSetOverrideUpdatesAnExistingIdentityOverride (0.00s)
--- PASS: TestSetOverrideCreatesTheIdentityWhenTheOrganisationHasNeverBeenSeen (0.00s)
--- PASS: TestSetOverrideRefusesAnUnknownFlagAndAnEmptyOrganisation (0.00s)
--- PASS: TestDeleteOverrideRemovesTheIdentityOverride (0.00s)
--- PASS: TestDeleteOverrideIsIdempotentWhenThereIsNothingToRemove (0.00s)
--- PASS: TestServiceSatisfiesTheWholeSeamWithoutErrReadOnly (0.00s)
--- PASS: TestNewModuleRefusesEveryMissingCredentialInOneError (0.00s)
--- PASS: TestNewModuleRefusesAMalformedNumericCoordinate (0.00s)
--- PASS: TestNewModuleBuildsTheAdapterFromItsOwnDeclaredEnvironment (0.00s)
--- PASS: TestNewModuleRefusesAMissingCacheCapability (0.00s)
--- PASS: TestEndpointOverridesSelectASelfHostedDeployment (0.00s)
--- PASS: TestHealthMatchesTheSeamSiblingsAndMakesNoProviderCall (0.00s)
--- PASS: TestModuleErrorsNameTheAdapterNotTheCredential (0.00s)
--- PASS: TestReadyNamesEveryMissingCoordinateForAHandBuiltService (0.00s)
```

After landing (the two gates that necessarily stayed red until the manifest declared the
new package, plus the canary suite that imports it):

```
$ go test ./internal/modkit -run 'TestAgentsProjectPlaneEqualsTheUnownedResidue|TestEveryGoPackageDeclaresTestsOrSaysWhyNot' -count=1
ok  	github.com/gogogadget/gogogadget/internal/modkit	0.431s

$ go test ./internal/canary/ -count=1
ok  	github.com/gogogadget/gogogadget/internal/canary	0.519s

$ go build ./... && go vet ./...
(no output)
```

### 8.1 Known-red, expected, owned by the release order

`go run ./cmd/ggg catalog --json` cannot resolve, and says so precisely:

```
"message": "resolve registry ggg: registry snapshot payload
            \"registry/modules/page/admin-flags/module.json\" digest mismatch"
```

That is `registry.snapshot.json` pinning pre-edit manifest digests. Per the slice
constraints I did not run `ggg registry build` / `sign` / `sync`, did not push and did not
tag; `gogogadget.lock.json` and `registry.snapshot.json` therefore still carry the
LaunchDarkly module and stale digests, and the full `make check` / `ggg registry validate`
integration pass belongs to the release owner. The schema and release-baseline gates that
*can* run against the tree both pass (§8).

---

## 9. Live canary handoff

Sent to `AdmissionRule` over `hub` once the suite was green; landed by them as
`80697384 "Canary the flags slot against the provider that decides"`, with three corrections
I accepted:

- `liveCanaryLeavesBounded`, not `liveCanaryCleans` — correct, per §2.2.2: the probe mints
  two Flagsmith identities the seam cannot delete, and its deletes are deferred with errors
  dropped, so removal is attempted rather than verified.
- the nonce replaced with the file's existing `liveCanaryNonce()` (`<16 hex>`, matching the
  docs row).
- both cleanup defers wrapped in `context.WithoutCancel`, because the row's 30s deadline can
  fire before cleanup runs.

The row's keys are the adapter's own four required keys plus the two optional endpoint
overrides, every one scoped in the manifest to exactly
`ggg/system/feature-flags-flagsmith@flagsmith`, so their key-scope walk needed no exception
entry. `liveCanaryNothingToProbe` is now **empty for the first time**.
