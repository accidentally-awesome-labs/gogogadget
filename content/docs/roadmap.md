---
title: Roadmap
description: Researched feature gaps vs. production SaaS norms — what exists, what's tiered, what's deliberately delegated.
section: Guides
weight: 28
---

This page is a point-in-time audit (2026-08-17) of what a production SaaS
needs that GoGoGadget lacks, tiered by priority. Sources: a full repo
inventory plus published SaaS operational norms — audit-retention practices,
GDPR 30-day export/deletion windows, status-page ticket deflection, and OTel
vendor neutrality. Every claim of absence cites the repo path as evidence.

## Tier 1 — shipped in this release

| Capability | What landed |
|---|---|
| Admin audit-log viewer | `/admin/audit` |
| Docs search | `/docs/search?q=` — sidebar box, ranked results with snippets |
| Flags admin | Create/delete + per-org overrides at `/admin/flags/{key}`; evaluator cache invalidated on mutation |
| Metrics | `GET /metrics` — stdlib Prometheus exposition (HTTP counters, latency, runtime, DB pool); bearer-gated via `METRICS_TOKEN`, unregistered in prod without one |
| Audit retention | `AUDIT_RETENTION_DAYS` janitor (0 = forever, the default); per-category windows remain future work |
| Schedules admin | `/admin/schedules` — create/toggle/delete + run-now over the scheduler table; only `jobs.SchedulableKinds` offered |
| Webhook secret rotation | Rotate from settings; previous secret keeps verifying for a 24h grace window (deliveries dual-sign), janitor clears it |
| Impersonation reason capture | Interstitial requires a 10–280 char reason; stored on the session row and in both audit entries |
| OpenAPI spec | `GET /api/v1/openapi.yaml` (3.1, embedded); route⇄spec parity and payload-shape tests block drift |
| API expansion | Cursor pagination (`next_cursor`), `Idempotency-Key` on POST with 24h replay, per-token rate limits (`API_RATE_LIMIT_RPM`) |
| Email digest | Per-user cadence (`off`/`daily`/`weekly`) rolled up from in-app notifications; worker-rendered, window-stamped |
| Server-side theme + locale | Stored on the user row, mirrored to cookies; dark paints server-side (no flash), digests speak the user's language |
| Support role tier | `users.admin_role` (`support`/`admin`); read-only staff, method-based write guard, audited grants, last-admin lockout |
| SEO hardening | Self-referential canonicals (collapsing the `?lang=` duplicates), reciprocal hreflang, JSON-LD, sitemap `lastmod`, RSS autodiscovery — `/docs/seo` |
| Org-level data export | `org:admin` JSON bundle via the job → storage → notification path; secrets excluded by construction, capped and marked when truncated |
| Dunning sequence | Day 0 / +3 / +7 comms scheduled at failure, each re-checking the subscription so a recovered customer is never chased — `/docs/billing` |
| Loading feedback | Delayed navigation progress bar + content dim (no flicker on fast swaps), `.skeleton` for load-triggered fragments — `/docs/frontend` |
| Admin jobs viewer + dead-letter requeue | `/admin/jobs` |
| Announcement banner + admin CRUD | `/admin/announcements`, app-shell banner |
| Notification preferences | `/app/settings/notifications` |
| GDPR self-serve: data export (JSON) + account deletion | `/app/settings/account` |
| Dedicated 403 page + `MAINTENANCE_MODE` 503 page | Error pages for both codes |
| Mobile app navigation | Hamburger drawer in `Topbar` |
| Testing | Seam contract suites, zero-test package coverage, `-race` + fuzz + smoke + docker CI jobs |

## Tier 2 — recommended next

| Gap | Evidence of absence | One-line approach |
|---|---|---|
| Changelog page; in-app help; breadcrumbs; keyboard shortcuts/command palette | Absent — `internal/web/templates/` | Add incrementally |

## Tier 3 — platform/delegated

| Gap | Decision |
|---|---|
| SSO/SAML/SCIM, passkeys, session/device management | Deliberately delegated to Clerk — document, don't build |
| Status page | Recommend external (e.g. BetterStack/UptimeRobot) reading `/healthz`; don't self-host a status page on the same failure domain |
| Backups/PITR | Neon/managed-Postgres concern; document a restore drill in [Deployment](/docs/deployment) |
| Multi-node rate limiting | Documented upgrade trigger exists (Upstash swap note in `internal/web/middleware.go` `rateLimit`) |
| Custom domains per org, per-seat billing, coupons | Polar/Clerk configuration, not code |
