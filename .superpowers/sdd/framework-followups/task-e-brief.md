### E. Recast README and the hand-owned docs around the framework workflows

Binding plan requirement (verbatim, task 10):

> Recast README and the hand-owned architecture/getting-started/extending/deployment/testing/
> security/roadmap pages around the framework workflows only after the commands exist. Preserve
> generated references as generated outputs. The public promise becomes: choose a profile, choose
> one adapter/target per required slot and environment, preview, apply, own the source, and keep
> updating without losing local edits.

The precondition is now met: the commands exist and are shipped. Tasks 1-10 plus this batch
delivered provider-aware schema v2, 19 provider slots with local/dev and managed/production
adapters, signed external registries, provider-neutral identity/billing, the typed `ggg` command
platform with a Charm TUI, `ggg new`/`init`/`create`/`setup`/`generate`/`services`/`dev`/`db`/
`check`/`test`/`build`, provider provisioning and deployment (`ggg provider`, `ggg deployment`,
`ggg deploy`, `ggg doctor --runtime`, `ggg db backup|restore|restore-drill`), targeted
`ggg update MODULES` / `--registry NAMESPACE --ref REF` with per-module snapshots, and the
external-registry template.

The docs still describe the pre-framework monolith: a SaaS boilerplate you clone and edit.

#### Scope — the pages this task owns

`README.md` plus the hand-owned pages named in the plan bullet: `content/docs/architecture.md`,
`getting-started.md`, `extending.md`, `deployment.md`, `testing.md`, `security.md`, `roadmap.md`.

Do NOT touch the generated references — `content/docs/configuration-reference.md`,
`module-reference.md`, `component-reference.md` are `ggg sync` outputs (see
`modkit.IsGeneratedOutputPath`); they stay generated and are linked, never restated.

Feature pages outside the plan's list (`api.md`, `billing.md`, `frontend.md`, `modules.md`, …)
are out of scope EXCEPT where they now state something false about a workflow this program
changed — in that case fix the false sentence and note it in your report rather than rewriting
the page.

#### What the recast must say

1. **The public promise, in this order**: choose a profile → choose one adapter/target per
   required slot per environment → preview → apply → own the source → keep updating without
   losing local edits. Every page's framing follows from that, not from "clone this repo".
2. **Getting started** is `ggg new` (or `ggg init` to adopt an existing directory), then
   `ggg setup`, `services up`, `db migrate`, `db seed`, `dev`. The zero-account path must stay
   documented and correct: `DEV_AUTH_BYPASS=true`, `/dev/login`, no Clerk/Polar/Resend account
   required, and `DEV_AUTH_BYPASS` boot-refused when `APP_ENV=production`.
3. **Architecture** documents the real layering: manifests own files and declarations; the
   registry resolves one graph; generated bootstrap wires typed capabilities; provider slots
   select adapters per environment; seams keep SDKs out of handlers.
4. **Extending** is the authoring path: `ggg create module|resource|page|workflow|job|migration|
   component|provider`, what each emits, the manifest fields that make it real, `ggg diff`,
   conflict resolution via `ggg resolve`, and the publisher path through the external-registry
   template (registry init/keygen/build/sign/verify/validate, rotation, `ggg registry add`).
5. **Deployment** documents `ggg deployment set`, `ggg deploy plan|apply|status|logs|rollback|
   secrets --environment`, the Docker and Fly references, stale-plan refusal, `--resume RUN_ID`,
   and the secret discipline: committed choices in `gogogadget.json`, CLI-managed dev/test values
   in mode-0600 `.ggg/env/<environment>.env`, never a secret in a plan, argv or state file, and
   no production secret written to disk.
6. **Testing** documents the test-layer decision rule, the gates (`make check`, `ggg registry
   validate`, e2e, visual through the pinned container, smoke, Docker, fuzz, race), and the
   per-module e2e ownership rule with the honest limits of the mechanical check (task B already
   rewrote the check's own section — keep it accurate, do not re-loosen it).
7. **Security** documents the middleware order, CSRF/CSP/SSRF rules, the fire-and-forget quartet,
   provider-neutral identity ids, restricted/rotating credentials, signature verification for
   external sources, and the refusals that fail closed.
8. **Roadmap** states what is actually done versus what is not, replacing any pre-framework
   list. Include the known follow-ups this batch recorded rather than pretending they are done.

#### Non-negotiables

- **Every command, flag, path and behaviour you write must be verified against the code**, not
  remembered. If a doc sentence and the code disagree, the code wins and you fix the sentence.
  Quote exact flag names from `internal/gggcli/table.go`.
- No aspirational text. If something is not implemented, do not document it as if it were; put
  it in roadmap.md with an honest status.
- Keep the docs' existing voice and structure conventions (see any current page) — this is a
  recast, not a new docs site. Internal links must resolve; `content/docs/index.md` must list
  every page it should.
- Any user-facing behaviour statement you change must match what `make check` proves. Do not
  change code in this task; if you find a code/doc mismatch that is a code bug, report it
  instead of fixing it here.

#### Acceptance

- README plus the seven named pages read as a framework, with the public promise stated in the
  order above, and no surviving sentence that describes the repo as a boilerplate you clone.
- A test or check that internal doc links resolve (extend the existing content tests if one
  exists — look in `internal/content` — rather than adding a second mechanism).
- `go test ./internal/content ./internal/web -count=1` passes (docs are embedded markdown; the
  content package parses and serves them).
- `go run ./cmd/ggg registry build && go run ./cmd/ggg sync --offline && go run ./cmd/ggg sync --check --offline` clean; the generated reference pages must be byte-identical afterwards.
- Do NOT run `make check`, e2e, visual, or any project-wide formatter/linter.
