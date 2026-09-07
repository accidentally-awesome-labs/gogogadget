# AE — stop advertising a profile shape that does not exist

Base `eb6c4197` (`v0.14.0`). Two decisions were handed down and both are implemented as given:
`ggg/profile/api` is deleted with a named-replacement refusal in its place, and `minimal` keeps its
name while its description and docs finally say what it is and why its floor is where it is.

---

## 1. `ggg/profile/api` is gone, and the name refuses instead of missing

### What was removed

| site | change |
|---|---|
| `registry/profiles/api.json` | deleted |
| `registry/profiles.json` | index entry removed (`registry build` rediscovers the directory, so the index is regenerated from the four remaining documents rather than hand-trimmed) |
| `content/docs/index.md`, `README.md` | the "choose a profile" list is now four names |
| `content/docs/getting-started.md` | the `api` table row is gone; a section states the deletion and its cause |
| `content/docs/architecture.md` | the resolution-step note cited `web` **and** `api` as the two profiles the un-expanded slot derivation broke; it now cites `web` and names the four slots it reaches transitively |
| `internal/modkit/resolve.go` | same correction in the comment that carries that history |
| `internal/modkit/shipped_profiles_test.go` | two comments referred to `api` as a shipped profile; they now say "the since-deleted `api`" and "the `web` closure" |
| `internal/gggcli/task8_handlers.go` | the guided prompt read `Profile (minimal, web, api, saas, full)` |

`registry/profiles/*.json` are registry documents, not module payloads, so no manifest `files` list
referenced the deleted document.

### The refusal, and where it lives

`internal/gggcli/new_project.go` gained `retiredProfiles`, a table from a retired scoped profile id
to the refusal an operator naming it should read, plus `retiredProfileRefusal`, which scopes the
operand through the existing `scopedProfileID` first so `--profile api` and
`--profile ggg/profile/api` are one answer.

`previewNew` consults it **only when `findProfile` misses**. That placement is load-bearing in two
directions:

- it is the site that resolves a profile id, so the refusal replaces the exact `unknown profile %q`
  it would otherwise have produced — nothing else in the command had to learn about it;
- it runs **after** the catalog is resolved, so `ggg new --registry github:gogogadget/gogogadget
  --ref v0.14.0` — a signed snapshot that still publishes `ggg/profile/api` — resolves that profile
  normally. An unconditional pre-check would have refused a profile the requested snapshot actually
  contains.

Exit code is 2 (usage), which is what the operand class already was. Precedent and voice are taken
from `ggg test unit`, retired in an earlier slice: a usage error naming the surviving mode, never a
silent alias, because an alias is how two names stay indistinguishable.

Measured through the built binary, both operand forms, human and `--json`:

```
$ ggg new /tmp/ae-api-refusal --module example.com/aeapi --profile ggg/profile/api \
    --registry directory:. --non-interactive
error: ggg/profile/api is gone; it resolved to the same closure as ggg/profile/web, module for
module, because the JSON API transport it existed to add is something ggg/system/server requires —
internal/web/routes.go dereferences the apiSurface that ggg/workflow/openapi-contract declares. An
API-only shape is not currently separable from the web surface, so two names described one thing.
Run `ggg new --profile ggg/profile/web`: it installs everything ggg/profile/api did
EXIT=2
```

`--profile api` prints the identical message and exit; `--json` the same; no destination directory
is created in any of the three.

### `add` and `update` reach the same name, so they got the same answer

`ggg new --profile` is not the only verb that takes a catalog id. An existing project reaches
`ggg/profile/api` through `ggg add` and `ggg update`, where the generic answer is the resolver's
`module "ggg/profile/api" is not in the catalog` — the same mute failure one layer over. The check
is in `Controller.Preview`'s `GraphMutation` branch, before `previewOperation`, so it holds for the
flag path, the TUI and any programmatic caller alike.

`remove` and `diff` are excluded deliberately: their subject is the installed graph, and
"not installed" is already the right sentence for a name nothing publishes. Telling an operator
removing something to install `web` instead would be the install-side remedy worn by the wrong verb.
`TestAddAndUpdateNameTheDeletedAPIProfileButRemoveDoesNot` pins all three.

### Proof

`internal/gggcli/task8_test.go`:

- `TestNewRefusesTheDeletedAPIProfileByNamingItsReplacement` — both operand forms, over the
  `genesisTree` fixture catalog: exit 2, the message names the replacement command, the closure
  identity, and `ggg/system/server`, and does **not** contain `unknown profile`.
- `TestNewStillReportsANeverPublishedProfileAsUnknown` — `ggg/profile/nope` still reads as
  `unknown profile "ggg/profile/nope"`. The table must not become a blanket rewrite of every miss;
  a name that was never published is a typo, which is what the generic message says.
- `TestAddAndUpdateNameTheDeletedAPIProfileButRemoveDoesNot` — as above.

**Mutation-proven.** The closing clause of the refusal changed from
`Run \`ggg new --profile ggg/profile/web\`: it installs everything ggg/profile/api did` to
`That profile no longer exists`:

```
--- FAIL: TestNewRefusesTheDeletedAPIProfileByNamingItsReplacement/ggg/profile/api
    task8_test.go:81: the refusal is missing "ggg new --profile ggg/profile/web": …
--- FAIL: TestNewRefusesTheDeletedAPIProfileByNamingItsReplacement/api
```

Reverted; green. So the test fails on a refusal that explains but does not point.

`internal/modkit/shipped_profiles_test.go` additionally asserts the catalog does not publish `api`
again, naming `gggcli.retiredProfiles` as the thing that assumes it is gone — the two halves cannot
drift apart silently.

---

## 2. `minimal` keeps its name and states its floor

Not renamed: `--profile ggg/profile/minimal` keeps working for anyone using it, and a rename buys
nothing that a true description does not.

Manifest description, in full:

> The smallest closure that boots, not a small application: 178 modules. The floor is set by two
> things and neither is a taste decision. `ggg/system/server` owns `internal/web/server.go`, whose
> statically-declared capability fields name thirteen capabilities, so its requires pull
> `internal/{api,audit,cache,jobs,notify,ratelimit,realtime,search,telemetry,usage,webhooks}` into
> every closure that serves HTTP. And migration `0020_provider_neutral_ids` asserts a fixed
> fifteen-table shape before it renames anything, so no closure may omit the nine modules that
> create those tables; it is immutable and locked, so the way down is a follow-on migration, not an
> edit. Generating `server.go`'s capability struct and neutralising that assertion are what lower
> the floor.

`content/docs/getting-started.md` carries the same two reasons under **Why `minimal` is 178
modules**, with `internal/web/routes.go` dereferencing `ggg/workflow/openapi-contract`'s
`apiSurface` spelled out as the reason a JSON API is not optional either, and closes on what the
name means until the floor moves: *the least this source can be made to boot as*.

### Where the count went, and why it is now checked

**There is no generated profile reference, and there cannot be one from the current inputs.**
`content/docs/module-reference.md` is generated from `gogogadget.lock.json`, and the lock's
top-level keys are `dependencies, engine_contract, go_tools, modules, order, providers,
registry_commit, registries, runtime_orders, schema, snapshots` — no profiles. `ggg catalog --kind
profile` is `unknown kind "profile"`. So nothing generated can contradict the manifests, and
generating a profile table was not available without first putting profiles in the lock.

That leaves the hand-owned table in `content/docs/getting-started.md` as the profile reference — and
it is exactly what rotted: it said `minimal` was 150 modules and `web` 223 while they resolved 178
and 254, and it advertised a fifth profile whose closure was identical to `web`'s. An advertised
number nothing asserts is how that happens, so the number is now asserted from both ends.

`TestTheDocumentedProfileTableMatchesWhatTheProfilesResolveTo` (`internal/modkit`) plans every
shipped profile the way `ggg new --profile` would and requires, per profile:

- the table's **Members** column equals `len(profile.Members)`;
- the table's **Closure** column equals `len(plan.Resolved)`;
- the table's **Required provider slots** column equals `len(profile.RequiredProviderSlots)`;
- **the count in the profile's own `description`** equals `len(plan.Resolved)`.

and both directions of membership: every shipped profile has a row, and every row has a shipped
profile. `len(plan.Resolved)` is the same number a real `ggg new --registry directory:.` writes into
the destination's lock — verified below, four profiles, four exact matches. Cost 3.5 s, no
toolchain, no network, no Docker.

**Mutation-proven, both halves** (each needed a `registry build` + `registry sign` round, because a
payload edit otherwise fails resolution on the snapshot digest before the assertion is reached):

```
# table says 179 where minimal resolves 178
--- FAIL: TestTheDocumentedProfileTableMatchesWhatTheProfilesResolveTo
    ggg/profile/minimal resolves 178 modules; the documented table says 179

# an `api` row put back into the table
--- FAIL: TestTheDocumentedProfileTableMatchesWhatTheProfilesResolveTo
    the documented profile table advertises profile(s) this catalog does not publish: [api]
```

Both reverted. After the revert the snapshot digest returned to `7a4d351b…` byte for byte, so the
mutation rounds left nothing behind.

---

## 3. Every remaining description was checked against what it resolves

Measured, `ggg new --registry directory:. --non-interactive` per profile, over the final tree:

| profile | exit | members | closure |
|---|---|---|---|
| `ggg/profile/minimal` | **0** | 117 | 178 |
| `ggg/profile/web` | **0** | 190 | 254 |
| `ggg/profile/saas` | **0** | 296 | 289 |
| `ggg/profile/full` | **0** | 286 | 288 |

Three of the four descriptions were false or now-false, and all three were corrected:

- **`web`** said *"Minimal plus public content, internationalization, and the discovery surfaces."*
  Two problems. It is not simply "minimal plus": `minimal` installs one module `web` does not —
  `ggg/system/mail-smtp`, which `minimal` selects for production where `web` selects
  `mail-resend`, so `minimal ⊄ web` as closures. And `web` has silently absorbed what `api` named.
  Both are now stated, with the closure count.
- **`saas`** said *"API plus organizations, billing, …"* — it named a profile that no longer
  exists. It now says "Web plus …" (member-checked: `web`'s 190 members are all in `saas`'s 296),
  gives 289 as the largest closure the catalog resolves, and states the one-module gap against
  `full` precisely rather than by hand-wave.
- **`full`** said *"Every module the GoGoGadget catalog publishes."* It resolves **288 of 297**.
  The nine absent, enumerated: six managed adapters its provider defaults do not select
  (`mail-smtp`, `feature-flags-launchdarkly`, `notifications-knock`, `search-typesense`,
  `usage-openmeter`, `webhooks-svix`), one deploy module its `default_deployment` does not choose
  (`deploy-fly`), and two UI modules its member list omits (`ggg/component/table-empty`,
  `ggg/element/divider`). The description now says all of that, including that the name overstates
  it.

The nesting claim is stated once, with its exception: `minimal ⊂ web ⊂ saas`, and the single
exception is a provider default (`mail-smtp`) rather than a member.

### `full`'s name: recorded, not renamed

Same decision class as `api`, so it is recorded here for a deliberate call rather than taken in
passing.

`full` cannot be made literally true by renaming alone, and it cannot be made true by growing the
member list either. Two of the nine absent modules are a member-list omission and could be added;
the other seven are structural — **an adapter is a per-environment selection, and one deploy module
is chosen out of the published set, so no profile can ever resolve all 297.** The honest options
are therefore:

1. **Leave it and describe it** (taken here). The name overstates; the description and the docs both
   say by how much and why.
2. **Add `ggg/component/table-empty` and `ggg/element/divider` to `full`'s members.** Closes the
   only gap that is a genuine omission, makes `full ⊃ saas`, and costs a closure change (288 → 290)
   plus a release round. Does not make the name true.
3. **Rename to something the shape supports** — `catalog`, `everything-selectable`. Breaks
   `--profile ggg/profile/full` for existing users, which is the exact cost the brief refused to pay
   for `minimal`, and here there is no correctness gain to pay it for.

Recommendation: (2) as a small follow-on so `saas ⊂ full` holds, and keep the name. The gap that
made the name a lie is now advertised rather than hidden, which was the actual defect.

---

## Gates

| gate | result |
|---|---|
| `go test -race ./internal/modkit ./internal/gggcli` | ok (194.2 s / 16.4 s) |
| `make check` | `tests: 1986 passed, 0 skipped, 0 inapplicable, 0 failed across 91 packages` |
| `bin/ggg registry validate` | exit 0 — every example/fixture/external closure installed, compiled, restored byte for byte (1967 tree entries each) |
| `make e2e` | `468 passed (5.7m)`, 202 skipped, 1 flaky retried green: `a11y-states.spec.ts:423 date-picker calendar open` (dark), unrelated to this slice |
| `make visual` | not run — no template, token or component touched |
| release order | `registry build` → `registry sign --dir . --key-file ~/.config/ggg/core-registry.key` → `sync --offline` → `sync --check --offline` = 0; snapshot `830d9c1e…`, registry `6e246c15…` |
| `TestCommittedSnapshotVerifiesUnderThePinnedCoreKey` | PASS |
| four-profile `ggg new` sweep | 0 / 0 / 0 / 0 |

`make check` was 1980 tests at `eb6c4197` and is 1986 now: seven added leaves (one documented-table
gate, three refusal tests across six subtests) less the `api` subtest of
`TestEveryShippedProfileResolvesIntoACoherentProject` that went with the profile.

The first `sync --offline` of a round leaves the lock one write behind and `sync --check` reports
exit 4 with `1 pending change(s), 0 generated drift(s)`; the second round is clean. Ran to green as
instructed, both times.

Revisions, each read fresh off disk immediately before writing: `ggg/system/modkit` 58→59
(`new_project.go`, `controller.go`, `task8_handlers.go`, `task8_test.go`, `resolve.go`,
`shipped_profiles_test.go`), `ggg/system/content-assets` 29→30 (`getting-started.md`, `index.md`,
`architecture.md`), `ggg/system/project-docs` 10→11 (`README.md`), and profiles `minimal` 2→3,
`web` 2→3, `saas` 1→2, `full` 1→2. A second payload round landed after the first signing; rather
than inflate `modkit` to 60 for one slice, the lock was restored to its committed state so
`ValidateManifestRevisions` compared 59 against the recorded 58 and the single bump covered the
whole slice.

No container was started. The operator's Homebrew Postgres on 5432 was never touched; the test
stack on 15432 is as it was found. One worktree was added at `eb6c4197` to measure `saas` and `full`
closures against the pristine tree and was removed (`git worktree list` shows one entry). Nothing
pushed, nothing tagged.

## Non-goals held

`ggg/system/server` was not split, `internal/web/server.go` was not touched, no profile was added,
and no module's `requires` changed. The only Go changes are the refusal table, its two call sites,
one prompt string, and three comments carrying history that named a profile which no longer exists.

## Follow-ups this slice found and did not take

1. **Generate the profile reference.** It is unreachable today because the lock records no
   profiles. Recording the selected profile id and its resolved closure in the lock would let
   `emitModuleReference` publish a profile table that cannot rot, and would retire the
   documented-table gate added here in favour of a generated output.
2. **`full`'s member list** — options above; (2) recommended.
3. **The floor itself**, which is what makes `minimal` 178 and made `api` indistinguishable from
   `web`: generate `server.go`'s capability struct from the resolved graph, and land a follow-on
   migration that neutralizes `0020_provider_neutral_ids`'s fifteen-table assertion. Both are
   already on the program's list; this slice only made their cost legible.
