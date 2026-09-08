# task-am — the fifteen conditional requirements the published contract did not state

Base `ff54e4dc` (`v0.17.0`). That release fixed one Critical: the published external contract
`registry/schema/module.schema.json` was weaker than the Go validator for `runtime.ui[].signature`,
so a third-party manifest could validate clean and be refused by `ggg`. The gate that landed with
the fix then measured **fifteen more instances of the same class** — conditional requirements the
tool enforces and the schema stated none of — and recorded them as its own slice
(`task-al-report.md` §4, "Recorded, not fixed"). This is that slice.

Fourteen are now stated in the published schema and asserted through both engines. One needs a
keyword JSON Schema 2020-12 does not have; it is recorded beside the signature residual rather than
approximated, because a keyword that states a weaker rule than the tool reads as a guarantee.

---

## 1. The fourteen, expressed

Every rule below is stated with the keyword that says the actual rule, not a keyword that says
something adjacent. Each `$comment` in the schema names the refusal the tool raises.

| requirement | keyword | condition |
|---|---|---|
| `AssetContribution.engine` / `.integrity` | `dependentRequired` both ways | each requires the other: an injected engine whose bytes nothing pins, or a checksum the shell would ignore |
| `LocalServiceEnv.value` / `.from_key` | `oneOf` with `minLength` in each branch | exactly one — both matching means two `oneOf` branches match, neither means none does |
| `LocalServiceHealth.path` | `if kind const http` / `then` | an http probe fetches a path; a tcp probe dials the port |
| `NavigationContribution.route_id` / `.href` | `oneOf` | exactly one target |
| `NavigationContribution.group` | `if area const footer` / `then` / `else not` | required for footer entries, **refused** everywhere else — the validator's rule is `(area == footer) != (group != "")`, not "required when area is set" |
| `NamespaceClaims.jobs` ↔ `RuntimeContributions.jobs` | two `Manifest.allOf` entries, `if`/`then` each way | each half requires the other |
| `NamespaceClaims.cli` | `Manifest.allOf`, `if runtime.cli minItems 1` / `then` | a contributed command name is collision-checked |
| `NamespaceClaims.packages` | `Manifest.allOf`, `if environment contains derivation` / `then` | a derivation names a package the module must claim |
| `RuntimeContributions.system` + `SystemContribution.adapter` | `Manifest.allOf`, `if environment contains non-empty targets` / `then` nested | a target-narrowed env key needs the adapter whose targets it names |
| `ServiceTarget.provisioner` | `if automation enum provision,configure` / `then` (with `minLength`) | `manual` targets run a checklist and name none |

Three details that decide whether the keyword is right rather than merely present:

- **`minItems: 1` on every array discriminator, and on what the `then` requires.** `claims.jobs: []`
  decodes to an empty slice and the validator accepts it, so conditioning on mere *presence* would
  have made the schema refuse a manifest `ggg` accepts — the opposite divergence, and just as wrong.
- **`minLength: 1` inside the `then`/branch, never on the property.** `provisioner: ""` on a
  `manual` target is accepted by the validator, so a blanket `minLength` would have over-tightened;
  inside `then` it says exactly "for provision/configure, present and non-empty".
- **The two-halves and derivation rules live on `Manifest.allOf`,** not on `NamespaceClaims` or
  `RuntimeContributions`, because they span two sibling objects and neither definition can see the
  other.

No pattern was added, so `TestPublishedSchemaPatternsArePortableAndStrict` (Go RE2 portability) is
unaffected, and the document remains a valid 2020-12 schema — the compiler in
`publishedSchemaCompiler` validates it on every compile.

### What is *not* stated, and said so

Two of the ten rules are stated in the part JSON Schema can express and the rest is written down:

- **Jobs.** The presence of both halves is expressed; the validator additionally requires the two
  **sets** to be equal (every claimed kind declared and back), which needs a comparison of values
  across two arrays.
- **Env targets.** The presence of the adapter is expressed; the validator additionally checks each
  `<adapter>@<target>` string against the declared target ids.

Both limits are in the `$comment` next to the rule. A relaxation is sound — everything the schema
refuses, the tool refuses — but it is not silent.

## 2. The residual: `EnvironmentVariable.secret`

`validateAdapterEnvironment` requires an env declaration's `secret` flag (and `type`) to **equal**
the flag on the adapter target input whose `env_key` names that record's `key`. That is a join
between two sibling arrays on a matched value. JSON Schema 2020-12 has no keyword for it — the same
class of limit as RE2's missing backreference in `UIContribution.signature` — so it is recorded in
that property's `$comment`, listed in `publishedSchemaResiduals`, and **asserted**, not described:

`TestPublishedSchemaAndValidatorAgreeOnConditionalRequirements` drops `secret` from
`database-postgres`'s `NEON_API_KEY` declaration and requires the validator to refuse it and the
schema to accept it. If a future keyword expressed the rule, that assertion fails and the `$comment`
has to go. Symmetrically, the gate fails if the record names something the validator no longer
enforces.

`runtime.ui[].signature` remains the only other recorded divergence, still asserted as the only one
by `TestPublishedSchemaAndValidatorAgreeOnRendererSignatures` (`residuals != 1` is fatal there).
The two records are disjoint by construction: the signature residual is a *pattern-engine* limit on
a published pattern, so it is not a missing conditional and is not in
`publishedSchemaResiduals` — the comment above that map says why.

## 3. `assertSchemaDefinition`, rewritten

It asserted `required` **equals** the non-`omitempty` field set exactly, which is why the fifteen
could not land in the same slice that found them. It now proves three things with three distinct
messages, because which way parity broke decides whether the fix is a tag, a `required` entry or a
conditional keyword:

1. **Shape** (unchanged): property set equals the tagged field set, declared types match, struct
   properties `$ref` a real definition.
2. **The unconditional half, in both directions and named.** Every field without `omitempty` is in
   `required` ("the published contract accepts a manifest ggg refuses"); every entry in `required`
   is a field without `omitempty` ("the published contract refuses a manifest ggg accepts — a
   requirement that only holds sometimes belongs in if/then, dependentRequired, oneOf or not, never
   in required"). This is what forces the conditional rules out of `required` and into keywords.
3. **The conditional half.** For each field the inventory records a rule about, the definition must
   carry a conditional keyword naming it — read out of the schema by
   `schemaConditionalRequirements`, never asserted from a hand-written expectation of *where* the
   keyword sits. So deleting an `if`/`then` fails here, not only in the catalog sweep.

It cannot pass on an empty schema: `type`, `additionalProperties: false`, the property-set equality
and the required-set assertions are all still fatal, and a definition missing entirely still fatals
on the first lookup.

`assertSchemaDefinition` lives in `registry_test.go`, which is **not** `self_host` — derivatives get
it. So the two inventory maps and the schema walker live in the self-host file and are passed in as
plain `map[string]bool` / `map[string]string` parameters; a derivative compiles without them.

Five definitions joined the parity sweep while I was there — `LocalService`, `LocalServicePort`,
`LocalServiceEnv`, `LocalServiceVolume`, `LocalServiceHealth`, plus `TargetInput` — because two of
the recorded rules are about them and nothing was checking their presence parity at all.

## 4. The gate: presence parity → conditional parity

`TestValidatorRequiredFieldsAreNotOptionalInTheContract` reported **0** because a rescue excused the
finding: a conditional requirement proved the rule could not live in `required`, and the measurement
stopped there. A rescue is no longer an excuse — only a reason for the schema to say something other
than "always required".

Both sides stay derived, not written down:

- **Candidates** (unchanged): reflect-walk all 297 published manifests, zero each `omitempty` field
  in place, keep the ones some real manifest is refused for. Free-form regions (`json.RawMessage`,
  maps) are left whole.
- **Conditionality** (unchanged): `searchForRescue` — one single-field change at a time, excluding
  the target's own ancestors and descendants.
- **The schema's side** (new): `schemaConditionalRequirements` reads `Definition.field` pairs out of
  every `required` inside `if`/`then`/`else`/`not`/`oneOf`/`anyOf`/`allOf`/`contains`/
  `dependentSchemas`, and out of both sides of every `dependentRequired` entry. It is a walk, not a
  list, because the two-halves rules do not live on the definition that owns the field: it carries
  the definition each instance location belongs to and switches it when it steps through a property
  whose declared schema is a `$ref`, or an array of them.

The verdict per candidate: rescued **and** named by a schema conditional → pass; rescued and not
named → fail with the field, the witness manifest and the rescuing sibling; not rescued → the
original "drop the omitempty" finding (this is the check that fires on C1 and on nothing else).

The inventory is **closed** in both directions, so it cannot rot into a waiver list:

- a candidate the inventory records nothing about fails, naming what to add;
- a recorded rule the schema no longer states fails;
- a recorded rule no manifest exercises any more fails as stale;
- a recorded residual the schema now expresses fails as stale;
- a recorded rule whose owning definition no model type walks fails (in
  `TestPublishedSchemasMatchModels`), because a typo'd key would otherwise be asserted by nobody.

Kept: the positive control (`UIContribution.name` must find **no** rescue, or a search that rescues
everything would report a clean sweep) and the floors (≥200 field sites, ≥8 candidates, plus a new
floor that the schema walk find at least as many conditionals as the inventory records), so a
collapsed walk fails loudly rather than passing vacuously.

**Measured on this tree:** 15 candidates, all 15 rescued, 14 answered by a conditional keyword, 1
recorded residual, 0 findings.

## 5. Both-engines agreement, per rule

`TestPublishedSchemaAndValidatorAgreeOnConditionalRequirements` — 27 rows over 7 real published
manifests, each mutated in one place and run through both engines
(`decodeStrict` + `requireJSONValue` + `validateManifest` on one side, the compiled published schema
on the other), in the style of the signature test. Every rule has at least one row the tool refuses
and one row where the **discriminator is absent and both engines accept the very same missing
field** — without which a conditional keyword is indistinguishable from an unconditional
requirement bolted onto `required`.

| rule | refused rows | accepted counterpart |
|---|---|---|
| assets engine/integrity | engine with no integrity; integrity with no engine | an ordinary asset with neither |
| local service env | neither value nor from_key; both | `from_key` instead of `value` |
| local service health | http probe with no path | the same probe as tcp with no path |
| navigation target | neither route_id nor href; both | a route id instead of an href |
| navigation group | a footer entry with no group; a non-footer entry with a group | the same entry moved out of the footer with no group |
| jobs two halves | a declared kind with no claim; a claimed kind with no declaration | neither half |
| claims.cli | a contributed command with no claim | no command and no claim |
| claims.packages | a derivation with no claimed package | no derivation and no claimed package |
| runtime.system / adapter | narrowed env key with no adapter; with no `runtime.system` at all | no adapter and no narrowed env key |
| targets provisioner | a `provision` target with no provisioner; a `configure` target with no provisioner | a `manual` target with no provisioner |

The base manifests are asserted **accepted unmutated by both engines** first, once per base; without
that control every row would pass even if its mutation were harmless. The three mutators
(`schemaDropKeys`, `schemaAddKey`, `schemaReplaceKey`) fail when the key they touch is not already in
the state they assume, and `mutate` refuses to return bytes identical to what it read — a row that
mutated nothing would otherwise assert about the base manifest rather than about the rule.

## 6. Driven to red

Deleting the `if`/`then` pair from `ServiceTarget` in the published schema (nothing else) puts all
three gates on it:

```
--- FAIL: TestPublishedSchemasMatchModels
    schema definition ServiceTarget states no conditional requirement about provisioner, and the
    validator enforces one (if automation is provision or configure/then required). `required`
    cannot carry it: the field is optional whenever the condition is absent, so a third party's
    manifest would validate clean and be refused by ggg

--- FAIL: TestPublishedSchemaAndValidatorAgreeOnConditionalRequirements/ServiceTarget.provisioner/a_provision_target_with_no_provisioner
    published schema refuses = false, want true — the external extension contract disagrees with
    the tool about ServiceTarget.provisioner

--- FAIL: TestValidatorRequiredFieldsAreNotOptionalInTheContract
    ServiceTarget.provisioner is CONDITIONALLY required — the validator refuses its zero value in
    registry/modules/system/database-postgres/module.json and
    .runtime.system.adapter.targets[1].automation rescues it — and the published schema states no
    conditional requirement about it (recorded rule: if automation is provision or configure/then
    required), so a third party's manifest validates clean and ggg refuses it.
```

The gate names the field, the witness manifest, **and the sibling that rescues it** — the
discriminator, derived, not written down. Removing the two `jobs` entries from `Manifest.allOf`
instead produces the same shape for both halves, each naming its own rescuer (`.runtime` and
`.claims`) in `registry/modules/system/billing/module.json`. The schema was restored byte-identically
after each experiment (`cmp` clean).

## 7. Docs

`content/docs/extending.md`:

- The `claims` bullet said "Every declaration needs a matching claim". True but half the rule: it
  now says job kinds run **both** ways and why (a claim with no declaration is a kind nothing
  dispatches).
- New subsection **"The conditional rules the contract states"** in the manifest reference: the ten
  rules as a table with their conditions, then the two recorded divergences with the keyword each
  would need, and the tests that fail if a third appears or a recorded one becomes expressible.
- The `signature` paragraph said "one documented gap". It now says one of exactly two, and points at
  the new subsection.

Same-page anchor links were deliberately not used: `docsLinkRe` only validates `/docs/...` links and
the renderer emits no heading ids, so a `](#…)` link would be an unchecked dead link.

## 8. Recorded limit of the gate

The sweep is field-granular. A **new** condition added to a field the inventory already records —
say a second reason `navigation[].group` is required — would still be rescued and still be "named by
a conditional", so the gate would pass while the schema stated only the older, weaker rule.
Reflection over manifests can see *that* a field is conditionally required, never *which* condition.
The row-level agreement test is what covers a specific rule's shape, which is why each rule carries
its refused and accepted rows rather than relying on the sweep alone.

## 9. Gates

| gate | result |
|---|---|
| `go test -race ./internal/modkit` | ok (323 s) |
| `go test -race ./internal/content` | ok |
| `make check` | **2088 passed, 0 skipped, 1 inapplicable, 0 failed** across 91 packages (was 2061; +27 new rows) |
| `bin/ggg registry validate` | exit 0 — every example and fixture closure installed, compiled, restored byte for byte |
| `GGG_GENESIS_SWEEP=1 go test ./internal/gggcli -run TestEveryShippedProfileCreatesAProjectThatIsSyncClean` | ok (206 s) |
| all 297 published manifests + 4 profiles against the tightened schema | `TestPublishedSchemaInstancesValidate` ok |
| `bin/ggg sync --check --offline` | exit 0 |
| `TestCommittedSnapshotVerifiesUnderThePinnedCoreKey` | ok |

`make e2e` was not run: nothing under `internal/web` changed except the one-line `index:` digest
comment every generated aggregate carries after a manifest revision moves (`git diff` over
`internal/web` and `static` is comment-only, verified line by line).

Release order over the final tree: `registry build` → `registry sign --dir . --key-file …` →
`sync --offline` → `sync --check --offline`, re-run after the last test edit. The first pass had
already written revision 74 into the lock, so the second `build` refused
("payload digests changed without a revision bump") — the lock, snapshot and signature were reset to
`HEAD` and the whole order re-run once at revision 74 rather than bumping to 75 for one logical
change. `build` is idempotent on the final tree (second run exits 0 with no writes).

Revisions bumped, read fresh off disk immediately before writing: `ggg/system/modkit` 73 → 74 (the
two test payloads), `ggg/system/content-assets` 36 → 37 (`extending.md`).
`registry/schema/module.schema.json` is format-owned, not module-owned, so it carries no revision —
only the snapshot digest and signature.

Not pushed, not tagged.
