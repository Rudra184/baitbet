# baitbet — architecture, integration contract, and status

Labeling: **CONFIRMED** = read directly in code/config or verified by running it.
**INFERRED** = follows from confirmed facts but wasn't directly exercised.
**UNVERIFIED** = needs a decision or environment this document doesn't have.

---

## 1. What this is, and the consumption model

baitbet is a standalone phishing-simulation / security-awareness-training
service: operator workflow (draft → test → approve → release → close),
form-free email + debrief rendering, evidence, and reporting. It runs
independently, like GoPhish — a service you run and talk to over HTTP, never
a library imported into a host application's own process. A host platform
(any language, any stack) can optionally gate access to it per its own users
and organizations using the handoff-token contract in §2, but nothing about
baitbet requires that; it works standalone with no host at all.

Deliberately out of scope: credential harvesting / live page cloning. This is
an awareness/training tool — recipients see a simulated phish and a debrief
page, nothing captures what they type.

## 2. Integration contract (for an embedding host application)

This section describes how *any* host application — not a specific one —
can gate and scope access to baitbet per its own users/organizations,
without baitbet re-implementing that host's authentication or billing.

### 2.1 Enable / disable / re-enable

baitbet has no opinion on licensing or plans. A host integrates by reusing
whatever per-org feature flag it already has:

- The host remains the entitlement source of truth. baitbet never
  re-implements billing/licensing — it only trusts a signed token and an
  explicit per-org `enabled` flag it's told about (§2.1, `/internal/sync-org`
  below).
- **Admin navigation**: however the host already gates optional modules
  (a route guard, a feature flag, a sidebar check) is where this plugs in.
  The host can either proxy baitbet's admin workspace through its own
  origin (recommended: one origin for admins, simpler cookie/CSRF story),
  or redirect to baitbet's own origin carrying a short-lived signed token.
- **Sign-on contract (handoff token)**: the host mints a short-lived
  (minutes, not hours) signed JWT — `internal/authn` in this repo — with a
  fixed audience (`aud: "baitbet"`), `org_id`, `sub` (user id), and `role`
  claims, signed with a secret shared **only** between the host and
  baitbet, never the host's own session-signing secret (a leak of one must
  not compromise the other). baitbet verifies signature + audience +
  expiry + algorithm (HS256 only — no `alg: none` acceptance) on every
  request; see `internal/authn/authn_test.go`.
- **Org provisioning**: `campaigns.organization_id` has a hard foreign-key
  to `organizations(id)` — a token naming an org baitbet has never seen
  would otherwise fail every write with a constraint violation instead of
  a clean error. `POST /internal/sync-org` (also token-gated, scoped to the
  caller's own `org_id` — never a request-body value) upserts the org's
  name and enabled flag. The host calls this before or alongside a user's
  first admin request each session; it's idempotent, so calling it every
  time (rather than caching "already synced") means a later rename or
  re-enable is picked up for free. See
  `TestSyncOrg_ProvisionsAnOrgTheTokenNamesSoCampaignCreationSucceeds`.
- **Standalone mode**: when no handoff secret is configured
  (`SIM_HANDOFF_SECRET` unset), baitbet manages a single local org itself —
  this is the GoPhish-style "run it on its own" mode, not a fork of the
  integrated code path.

### 2.2 Disable behavior

| State | Behavior |
|---|---|
| Org disables the module (host-side) | Host stops calling `/internal/sync-org` with `enabled: true` (or calls it with `enabled: false`). baitbet blocks new sends/edits for that org; in-flight approvals are frozen, not silently continued. |
| Recipient opens an old tracking link, org disabled | baitbet serves a neutral "this page is no longer active" response — no event recorded, no 500, no leak of whether the org/campaign ever existed. |
| Org re-enables | A draft left in-flight requires a fresh explicit approval before it can release — re-enable is never a silent resume. |
| baitbet itself is down | The host's own module-availability check should fail closed to "module unavailable" for this feature only; every other part of the host app stays functional. |
| One tenant disabled | Must not affect any other tenant — true by construction, since every handler scopes to the token's own `org_id` (`ownedCampaign` in `internal/server/server.go`), confirmed by `TestIntegratedMode_ValidTokenWorksAndScopesToItsOrg`. |

### 2.3 Diagrams

**Admin path**
```
Admin browser -> Host application (its own session auth + entitlement check)
    -> allowed: proxy/redirect into baitbet's admin workspace, carrying a
       short-lived signed handoff token (org_id, user_id, role, exp)
    -> baitbet verifies the token, syncs the org, serves the workspace
```

**Recipient path**
```
Recipient clicks tracking link -> baitbet (public route, no host auth at
  all - the opaque per-recipient token IS the credential)
    -> campaign active, org enabled: render debrief, record event
    -> campaign closed, expired, or org disabled: neutral closed response,
       no event recorded
```

**Service connections**
```
baitbet -- owns --> its own Postgres (separate credentials, own migrations),
  an approved SMTP profile
baitbet -- never --> reads/writes a host application's tables directly
Host application -- never --> reads/writes baitbet's tables directly
```

### 2.4 Does baitbet need its own Postgres server?

No — running a second full Postgres *process* would be the wrong call once
embedded, though a second *logical database* is still the right one. The
actual requirement is separate credentials, schema, and migration
ownership (so baitbet's release cycle and data model stay independent of
the host's) — never a mandate for a second physical server. The cheap,
correct version: a host that already runs its own Postgres container adds
baitbet's database as a second logical database inside that same
container, with its own role and its own migrations
(`internal/store/migrations`) — one `CREATE DATABASE baitbet OWNER
baitbet_role;`, plus pointing `SIM_DB_URL` at it. The host's own database
connections never get a grant on that role, so cross-reads stay impossible
at the database level, not just by convention.

What doesn't work: baitbet cannot be imported as a library into a host
written in another language — there's no `import` path across a language
runtime boundary without an RPC layer of some kind, which is exactly what
the HTTP contract above already is. A library/package consumption model
only makes sense if the host itself is also Go.

---

## 3. Deployment specifications

Values marked **(ENV-DEPENDENT)** need the actual target environment to pin
down; they aren't guesses, they're genuinely open until an operator decides.

### 3.1 Local

- `docker compose up -d db mailsink` — this repo's own Postgres
  (`sim_user`/`simulation_service`) and a local SMTP sink
  (`axllent/mailpit`, web UI at `:8025`). Never publish a real `SMTP_HOST`
  in local config — the absence of real credentials is the actual safety
  control, not a firewall rule.
- `go run ./cmd/server` with the env vars documented in `README.md`/`USAGE.md`.
- No internet egress required to run or test it (see §3.3 for why that's
  provable, not assumed).

### 3.2 SaaS / multi-tenant

- **Release independence**: own image, own version tag, deployed on its own
  CI/CD pipeline rather than coupled to a host's build — this is the whole
  point of the standalone-repo decision; see `.github/workflows/ci.yml`.
- **Tenant access check**: the `/internal/sync-org` contract in §2.1, called
  per admin request.
- **Admin vs. recipient route separation**: admin workspace behind the
  handoff-token contract; recipient-facing tracking/debrief routes on a
  separate public origin/subdomain from the admin origin — this also limits
  blast radius if a lookalike-domain configuration ever needs its own TLS
  cert, independent of the admin domain's cert. **(ENV-DEPENDENT**: which
  origin pattern a given deployment prefers.)
- **Secrets/DB scoping**: separate Postgres credentials, separate SMTP
  secrets, in whatever secret store the deployment already uses — never a
  host application's own encryption/session-signing keys.
- **Pilot rollout**: one org at a time via the `enabled` flag on
  `/internal/sync-org`, defaulting to disabled everywhere else — the
  rollout gate is this existing per-org flag, not a new one.
- **Rollback**: redeploy the previous image tag; no DB rollback needed if
  the migration that shipped stayed additive.

### 3.3 Air-gapped / on-premises

- **Signed image bundle**: `docker save` + a signature, verified before
  load — whatever signing tooling the deployment already uses for other
  on-prem components.
- **Local identity/TLS**: reachable on the internal network only; TLS trust
  from the deployment's own local CA. **(ENV-DEPENDENT**: actual cert
  provisioning per site.)
- **Approved internal SMTP relay**: configured per deployment; baitbet
  validates TLS + timeout against it at startup and on a periodic health
  probe, never silently falling back to any other transport.
- **No internet at runtime — provable, not assumed**: this binary has zero
  third-party Go modules beyond `pgx` and `golang-jwt` (`go list -m all`),
  and the renderer needs no browser engine. There is no page-cloning
  feature to need outbound reachability for in the first place — this is
  by design (§1), not a limitation worked around.
- **External delivery without internet**: only through an approved
  reachable mail relay. The operator workspace should show that route's
  reachability state explicitly rather than failing silently.

### 3.4 Configuration table

| Setting | Required | Secret | Local | SaaS | Air-gapped |
|---|---|---|---|---|---|
| `SIM_DB_URL` | yes | yes | local container, test password | managed Postgres, rotated creds | appliance-local Postgres |
| `SIM_HANDOFF_SECRET` | yes (integrated mode) | yes | shared dev value | rotated secret, shared only via secret store | shared only if host is co-located; else n/a |
| `SIM_SMTP_HOST` / profile | yes | yes (creds) | local sink, no auth | approved relay | internal relay only |
| `SIM_PUBLIC_BASE` | yes | no | `localhost` | tenant subdomain | internal hostname only |
| Image tag | yes | no | local build | pinned per release | pinned per signed bundle |

---

## 4. Backlog (milestones, acceptance criteria, rollback)

**A. Feasibility** — offline renderer, content-pack type + validation, local
preview server. Tests: determinism, escaping, no forms/scripts/remote
assets, local-asset-only logo. **Done.**

**B. Operator pilot** — Draft→Preview→Test→Approve→Release→Close workspace;
approved-SMTP-profile selection; test-send to named mailboxes; approval tied
to an exact content version + recipient list + mailer.
**Acceptance**: a named operator completes one campaign end-to-end against a
local mail sink; Outlook-rendering/visual-diff checks pass for reviewer-
approved fixtures at common breakpoints.
**Rollback**: disable the pilot org's flag; no data loss since nothing else
depends on it. **Done** (workflow + real local SMTP send; the
Outlook/visual-diff QA gate is still open — see §6).

**C. Closeout** — evidence review distinguishing accepted vs. verified-
delivered vs. rejected/unknown; a report built from the same reviewed
dataset.
**Acceptance**: report totals reconcile against mail-provider evidence.
**Done** (CSV report + event counts; no PDF export yet).

**D. Deployment and pilot** — signed air-gapped bundle, a real SaaS
manifest/CI entry, the enable/disable lifecycle exercised against a real
host, a reviewed migration-from-legacy inventory tool if replacing an
existing system (reviewed templates/drafts/closed reports only — never
captured credentials or old SMTP passwords).
**Acceptance**: cross-tenant isolation test (disabling org A doesn't affect
org B); disable-then-old-link test (neutral response, no event logged);
duplicate-release-prevention test; mail-failure-handling test;
startup-with-no-egress test.
**Rollback**: stop pointing the pilot org's entitlement at baitbet; whatever
previously served that org resumes — never both systems authorized to send
for one org at once.
**Not started** — no pilot tenant or real host integration exists yet to
exercise this against.

**E. Legacy retirement** (only relevant if replacing an existing tool) —
remove the old system only after every pilot org has cut over, retention
commitments on old links are met, and a restore-from-backup test confirms
the host app runs cleanly with the old module fully absent.
**Not applicable until D is done.**

---

## 5. What's built and verified

### Layout

```
baitbet/
  go.mod                               # module github.com/rudra184/baitbet
  internal/content/pack.go             # Pack: Version, Scenario, Email, Debrief; Validate()
  internal/render/                     # html/template rendering: debrief, email, delivery email
  internal/mailer/mailer.go            # SMTP send (stdlib net/smtp) to an approved mailer profile
  internal/store/                      # pgx/v5-backed CRUD + the workflow's state-machine guards
  internal/authn/                      # handoff-token verification (§2.1)
  internal/server/                     # operator workspace + public /t/{token} tracking endpoint
  cmd/server/main.go                   # wires DB + mailer + server from env vars
  cmd/preview/main.go                  # offline fixture preview, no DB, no network
  Dockerfile                           # multi-stage, distroless, CGO_ENABLED=0
  .github/workflows/ci.yml             # test -> build -> push image (main only)
```

### Rendering safety (determinism, escaping, no forms/remote assets)

Verified by `internal/render/render_test.go` (offline, no deps beyond the
stdlib): same content pack produces byte-identical HTML; injected markup in
every text field comes out HTML-entity-escaped, never as live tags; no
`<form>`, `<input>`, `<script>`, or remote `http(s)://` reference anywhere
in rendered output; the one asset reference (a logo) is a local embedded
`data:` URI, never a URL; an unresolvable asset key errors rather than
silently falling back to a network fetch.

One real bug found while building this: the first version of the local-asset
helper returned a plain `string`, and `html/template`'s URL sanitizer —
correctly, by default — treats any dynamic `string` in a `src` attribute as
untrusted and replaces it with `#ZgotmplZ` rather than risk an unsafe scheme.
Fixed by typing the field `template.URL`, the template package's documented
way to say "this value is constructed by trusted code, never from
user-authored content."

### Operator workflow (real DB, real local SMTP, real tracking link)

Verified by running the full workflow against a real local Postgres and a
local SMTP sink (Mailpit): draft → test-send (real SMTP delivery, visible in
the sink's own inbox) → approve (second attempt correctly rejected with
409) → release (real recipient email, real tracking link; a second release
attempt correctly rejected with 409) → a recipient opening the real tracking
link resolves to the real debrief page and records a real event → CSV report
reconciling against what was actually sent → close.

Disable lifecycle exercised for real: disabling an org mid-run makes an
existing tracking link return the neutral "no longer active" response and
record no new event; re-enabling restores normal behavior.

### Automated integration-test suite

`internal/store/store_test.go` (8 tests, `//go:build integration`): org
enable/disable round-trip, full campaign lifecycle, Approve/Release/Close
each rejecting the wrong status, the duplicate-release guard, draft-only
content editing, recipient/event round-trip, and `MarkSent` recording a send
failure distinctly from "no attempt made."

`internal/server/server_test.go` (`//go:build integration`): the full
Draft→Test→Approve→Release→Close workflow **with a deliberately unreachable
mailer** (dials fail fast and deterministically), asserting the workflow
still advances and the failure is recorded on the recipient row rather than
lost; duplicate approve/release/close all return 409 over real HTTP; the
real tracking link resolves and records a real event; an unknown token and a
disabled org's old link both go neutral with no event recorded;
`TestIntegratedMode_*` (handoff-token rejection on a missing/wrong-secret
token, valid-token success, and cross-org isolation — a token for one org
gets 404, not data, on another org's campaign); and
`TestSyncOrg_ProvisionsAnOrgTheTokenNamesSoCampaignCreationSucceeds`.

Both test tiers run via `go test ./...` (offline, render only) and
`go test -tags=integration ./...` (real Postgres via `SIM_TEST_DB_ADMIN_URL`,
a throwaway database created and dropped per run — confirmed zero leaked
databases before and after a full run).

One real bug caught by writing these tests, not by inspection: the
throwaway-database cleanup helper closed its own admin connection pool via
`defer` immediately after creating it, so by the time `t.Cleanup` actually
ran (at the end of the test) the pool was already closed and every `DROP
DATABASE` silently failed, leaking a new database per test run. Fixed by
moving the pool's `Close()` call to the end of the cleanup function, after
the drop — confirmed fixed by checking for leftover `simtest_*` databases
before and after a full run.

---

## 6. Production-readiness checklist

What would have to be true before this touches a real client, most
load-bearing first:

1. ~~**Admin authentication — none.**~~ **RESOLVED** (`internal/authn`): every
   admin route requires a signed HS256 handoff token when
   `SIM_HANDOFF_SECRET` is set. Still open: this only verifies the token is
   genuine and names an org — it doesn't call back to the host to confirm
   *that specific user* still holds the role the token claims; short token
   TTL is the current mitigation. Also: with `SIM_HANDOFF_SECRET` unset,
   there is no auth at all — that's the intentional standalone/local-demo
   mode, and must never be the case in an integrated deployment.
2. ~~**No multi-tenant isolation test.**~~ **RESOLVED** — see §5.
3. **No TLS in the binary.** Plain `net/http.ListenAndServe`. Fine behind a
   TLS-terminating reverse proxy that itself has access control in front of
   it; not fine exposed directly.
4. **Never tested against a real mail path.** Everything so far is a local
   sink or a deliberately unreachable host. No real SMTP relay, no real
   SPF/DKIM/DMARC, no real mail client has seen output from this service.
5. **Milestones D and E untouched.** No real deployment manifest, no signed
   air-gapped bundle, no pilot tenant, no cutover rehearsal.
6. **No image has been pushed to a registry from a real release yet** beyond
   what CI produces on push to `main`.
7. **No Outlook/visual-diff QA gate has actually run** against
   reviewer-approved fixtures.

None of these are large individually; together they're the gap between "a
genuine, tested local prototype" (true today) and "safe to put in front of a
client" (not yet).
