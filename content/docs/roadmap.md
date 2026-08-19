---
title: Roadmap
description: Researched feature gaps vs. production SaaS norms — what exists, what's tiered, what's deliberately delegated.
section: Guides
weight: 27
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
| OpenAPI spec + API expansion | Only 3 endpoints (projects list/create, ai/chat) — `internal/web/routes.go`; no spec; offset pagination only; no idempotency keys on POST; no per-token rate limits | Author `openapi.yaml`, then grow endpoints against it |
| Org-level data export | Only projects CSV exists — `internal/jobs/export_csv.go` | Full-org JSON/CSV bundle via the same job + storage pattern |
| Dunning depth | Single payment_failed email — `internal/billing/webhook.go` | Add a retry-schedule comms sequence |
| Email digest implementation | `email.digest` job kind is a registered no-op — `internal/jobs/jobs.go` | Implement daily/weekly rollup rendering |
| Changelog page; in-app help; breadcrumbs; keyboard shortcuts/command palette; skeleton screens beyond Clerk slots | All absent — `internal/web/templates/` | Add incrementally; skeletons first |
| SEO hardening: canonical tags, JSON-LD, RSS autodiscovery `<link>`, sitemap `<lastmod>` | Absent — `internal/web/templates/layouts.templ` `headMeta`, `internal/web/handlers_content.go` | Extend head meta + sitemap renderer |
| Moderator/support role tier | Only global `users.is_admin` + org roles; no read-only admin — `internal/web/auth.go` `requireAdmin` | Add a read-only admin tier |
| Server-side theme/locale preference per user | localStorage + cookie only | Persist on the user mirror row |

## Tier 3 — platform/delegated

| Gap | Decision |
|---|---|
| SSO/SAML/SCIM, passkeys, session/device management | Deliberately delegated to Clerk — document, don't build |
| Status page | Recommend external (e.g. BetterStack/UptimeRobot) reading `/healthz`; don't self-host a status page on the same failure domain |
| Backups/PITR | Neon/managed-Postgres concern; document a restore drill in [Deployment](/docs/deployment) |
| Multi-node rate limiting | Documented upgrade trigger exists (Upstash swap note in `internal/web/middleware.go` `rateLimit`) |
| Custom domains per org, per-seat billing, coupons | Polar/Clerk configuration, not code |
