# Task E review findings — fix round 1

Source: ReviewDocsRecast of 6788f83..57578ed. Spec compliance WARN, task quality WARN.
Verified correct by the reviewer: every count (297/18/36/42/288/174/10+1/37 pages), every command
and flag across all eight pages against `internal/gggcli/table.go`, the public promise in the
plan's order, scope (generated reference pages byte-identical), the link check, and the
honest-gaps discipline in roadmap.md. Fix the items below.

Note: the six contract gaps are now FIXED in code (commit dce9e7e) — panic recovery is installed,
`/readyz` consults `Runtime.Health` with critical/degraded semantics, `deploy apply --yes` works
noninteractively, `deploy plan` computes a real change set, the four read-only remote commands
render in human mode, and `make fuzz` runs both targets. So any sentence the review flagged as
"true only after the sibling's fix" is now true — but re-read those sections and make sure they
describe the shipped behaviour precisely, including the new `deploy plan` payload
(`plan_hash`, `observed_state_hash`, ordered `deploy://<id>/<resource>` changes, secret key names
only), the `remote_plan_stale` refusal, and the `/readyz` degraded response.

## Critical

- **C1 — `extending.md`'s "Add a plan" / "Add annual pricing" recipes name an API that does not
  exist.** `:547` and `:558` tell the reader to extend `billing.SetPolarProductIDs`; that symbol
  is gone from the entire tree (plan step 5 replaced global plan mutation with the injected
  immutable catalog, `internal/billing/catalog.go:21` `NewPlanCatalog`). `:549` claims surfaces
  render from `billing.Plans`; there is no exported `Plans` — `internal/billing/plans.go:28`
  declares unexported `defaultPlans`, reached through `DefaultPlanCatalog()` / `PlanByKey`
  (`plans.go:55`). `:543` tells the reader to add the `POLAR_PRODUCT_*` env record to
  `ggg/system/billing`'s manifest, but every Polar key is owned by `ggg/system/billing-polar`
  (`registry/modules/system/billing-polar/module.json:124-160`) — `ggg/system/billing` is the
  constructor-free seam, and the same page plus `README.md:205-207` state the adapter-owns-its-keys
  rule. Rewrite both recipes against the shipped catalog API and the correct manifest.

## Important

- **I1 — README's provider-slot default table claims a provenance the data does not have.**
  `README.md:108` says "Defaults are the `ggg/profile/minimal` seeds", but `:92` shows
  `mail-resend@resend` where minimal seeds `mail-smtp@smtp`, `:102` shows
  `analytics-posthog@posthog` where minimal seeds `analytics-noop@local` (both those values come
  from full/saas/web/api), and `:103` shows `observability-log@log`, which matches minimal only
  and contradicts both the other profiles (`full.json:554` seeds `observability-sentry@sentry`)
  and this repo's own committed production selection. Either state the profile each column
  belongs to or pick one profile and make every row match it.
- **I2 — the managed-key table omits the Neon key whose absence fails the boot.**
  `README.md:222` lists only `DATABASE_URL` for `ggg/database`/Neon. `NEON_API_KEY` is
  `required: true` (`registry/modules/system/database-postgres/module.json:214-219,244-253`) and
  the generated validator enforces it in production
  (`internal/config/config_registry_gen.go:208-210`). `NEON_PROJECT_ID` (optional) is also
  missing. A reader who supplies exactly the keys the table names gets the refusal the
  surrounding paragraph promises they will not get.
- **I3 — "one joined boot error naming every missing key" over-promises.**
  `README.md:210-211`, `content/docs/index.md:73-75` and `getting-started.md:121-123` assert a
  single joined error naming every missing key. The generated validator joins only what it
  enforces (`DATABASE_URL`, `NEON_API_KEY`, `RESEND_API_KEY`, the four `STORAGE_R2_*`, the four
  `CLERK_*`); Polar, PostHog, Sentry and LLM keys are read without a required check and their
  adapters refuse individually inside their constructors
  (`internal/billing/polar/module.go:24-26`, `internal/analytics/posthog/posthog.go:17-19`,
  `internal/observability/sentryadapter/sentry.go:54-56`, `internal/llm/openai/openai.go:23-25`),
  so boot fails on the first one. Keep the true half — nothing silently falls back to a local
  adapter — and state the joining accurately.
  Related manifest defect, fix it too: `billing-polar/module.json:129` still describes
  `POLAR_ACCESS_TOKEN` as "Empty means billing routes render 503 not-configured", which the
  constructor no longer does; that string is copied into the generated configuration reference
  and into `config_registry_gen.go:110`.

## Minor

- **M1** — `getting-started.md:54` glosses `full` as "Every module the catalog publishes". Its
  286 members exclude 11 catalog modules (incl. `ggg/element/divider`, `ggg/component/table-empty`
  and the nine environment-selected adapter modules), and the same table shows `saas` with 296 —
  larger than the profile described as complete. Fix the gloss.
- **M2** — `security.md`'s "Error pages include panic and validation detail only outside
  production" is now true again (recover is installed); confirm the sentence matches the restored
  chain position.
- **M3** — `deployment.md`'s `Runtime.Health` paragraph should now say what actually consumes it:
  `/readyz` (critical ⇒ 503, non-critical ⇒ 200 degraded) plus `ggg doctor --runtime`.
- **M4** — `modules.md:14-17` still shows unscoped kind examples (`element/button`,
  `page/projects`, …) on a page that elsewhere states ids are globally scoped.
- **M5** — `testing.md` cites both `./scripts/visual.sh` and `scripts/visual-run.sh` without
  relating them; one clause fixes the apparent contradiction (`visual.sh` execs
  `visual-run.sh compare`).
