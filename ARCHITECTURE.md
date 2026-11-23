# Architecture

## Consumption model

baitbet is a service, not a library. It runs on its own and you talk to it
over HTTP — the same model as GoPhish. There's no Go package to import,
and no reason there would be: the point of the separation is that whatever
calls into it can be written in any language.

It can run completely standalone (a single local org, no auth), or gated
per-organization by a host application using the handoff-token contract
below.

## Package layout

```
cmd/server/      the real operator/tracking binary (Postgres + SMTP)
cmd/preview/     offline fixture preview, no DB, no network
internal/content/  versioned content-pack type + validation
internal/render/   html/template rendering: email, debrief, delivery email
internal/store/    Postgres access + the workflow's state-machine guards
internal/mailer/   SMTP send via an approved mailer profile
internal/authn/    handoff-token verification for the integrated mode
internal/server/   HTTP handlers + server-rendered admin templates
```

## Integrating baitbet into a host application

This describes how any host app — not a specific one — can gate access to
baitbet per its own users and organizations, without baitbet needing to
know anything about that host's authentication system.

### Two ways to consume it

- **Embed the HTML workspace.** Every route under `/` and `/campaigns/...`
  renders a full page meant to be viewed directly (optionally inside an
  iframe behind the host's own auth). Zero integration work beyond the
  handoff token.
- **Build your own UI against the JSON API** under `/api/campaigns/...`
  (same routes, `Accept: application/json` in spirit — see below). Every
  JSON handler is a thin encoding of the exact same `internal/store` calls
  the HTML handlers use, so the two can never drift in workflow behavior.
  The JSON API is intentionally a plain mirror of baitbet's own data model
  — list/get/create/update-draft/test-send/approve/release/close, plus
  recipients and event counts — with no aggregation, renaming, or
  shaping for any particular consumer's UI. That belongs in the host
  application, which is free to combine these primitives however its own
  product needs.

| Method & path | What it does |
|---|---|
| `GET /api/campaigns` | List campaigns for the caller's org |
| `POST /api/campaigns` | Create a draft |
| `GET /api/campaigns/{id}` | Fetch one campaign |
| `PUT /api/campaigns/{id}/draft` | Replace a draft's content (draft only) |
| `GET /api/campaigns/{id}/recipients` | List recipients + send status |
| `GET /api/campaigns/{id}/events` | Event counts (e.g. `debrief_viewed`) |
| `POST /api/campaigns/{id}/test-send` | `{"recipients": ["a@b.com"]}` → status `tested` |
| `POST /api/campaigns/{id}/approve` | `tested` → `approved` |
| `POST /api/campaigns/{id}/release` | `{"recipients": [...]}`, `approved` → `released` |
| `POST /api/campaigns/{id}/close` | `released` → `closed` |
| `GET /campaigns/{id}/report.csv` | CSV report (not under `/api` — already a generic format) |

All of these require the handoff token in integrated mode, same as the
HTML routes, and return `{"error": "..."}` with the matching status code
(400/404/409/500) on failure.

### Handoff tokens

The host does its own login/permission check first. Once a user is allowed
to use the phishing-simulation feature, the host mints a short-lived
(minutes, not hours) signed JWT:

- `aud: "baitbet"` — a fixed audience claim. This stops the host's *own*
  session token (signed with the same secret) from being replayed against
  baitbet; a token minted for something else is rejected even if the
  signature checks out.
- `org_id`, `sub` (user id), `role`.
- Signed with HS256, using a secret shared **only** between the host and
  baitbet — never the host's own session-signing secret.

baitbet verifies the signature, audience, expiry, and algorithm
(`internal/authn`) on every admin request. There's no `alg: none`
acceptance and no algorithm-confusion path.

Set `SIM_HANDOFF_SECRET` to turn this mode on. Leave it unset and baitbet
runs standalone instead — a single local org, no token required.

### Org provisioning

`campaigns.organization_id` has a foreign key into `organizations(id)`, so
a token naming an org baitbet has never seen would otherwise fail every
write with a constraint violation. `POST /internal/sync-org` (also
token-gated, scoped to the caller's own `org_id`) upserts that org's name
and enabled flag. Call it before or alongside a user's first request each
session — it's cheap and idempotent, so there's no need to cache whether
you've already called it.

### Tenant isolation

Every admin handler resolves the org from the verified token, never from a
client-supplied value, and 404s (not 403) if a campaign belongs to a
different org than the token names — a cross-tenant lookup looks exactly
like "doesn't exist," not "forbidden."

### Disable behavior

| State | What happens |
|---|---|
| Org disabled | New sends/edits blocked; in-flight approvals are frozen, not silently continued. |
| Recipient opens an old link, org disabled | Neutral "this page is no longer active" response. No event recorded, no leak of whether the campaign ever existed. |
| Org re-enabled | Any draft left in-flight needs a fresh explicit approval before release — never a silent resume. |
| baitbet is unreachable | Should fail closed on the host's side to "feature unavailable," without affecting anything else the host does. |

### Database

baitbet owns its own Postgres database, with its own schema and
migrations. If the host already runs its own Postgres instance, the
cheapest correct setup is a second database inside that same instance with
its own role — not a second full Postgres server. The host's own database
connections should never have a grant on baitbet's role, so cross-reads
are impossible at the database level, not just by convention.

## Rendering

Email and debrief pages are rendered with the standard library's
`html/template` — auto-escaped by default, no third-party template engine.
The one asset reference (a logo) is embedded via `go:embed` and served as a
`data:` URI, so there's never a remote image to fetch.

Two deliberate, narrowly scoped exceptions to that form-free, self-contained
design exist, both load-bearing and covered by tests:

- **A real credential-capture form.** A campaign's landing page can render
  an actual `<form>` (`internal/render.Landing`/`LandingFromClone`) posting
  to this service's own `/t/{token}/submit` route. The password value is
  never read back into Go by the renderer or persisted by the handler on
  the other end — only whether a password field was used is recorded (see
  `internal/store.RecordSubmission`).
- **A headless-browser clone pipeline** (`internal/browserclone`, using
  `chromedp` against a separate headless-Chrome container), used by "Clone
  a webpage" so a landing page can actually look like its real target — JS
  executed, CSS applied — instead of a static-fetched skeleton. The
  rendered page is sanitized before it's ever stored
  (`internal/clone.SanitizeWithFormMarker` strips scripts/iframes/objects/
  event handlers and the real `<form>`, leaving a marker `internal/render`
  replaces with the service's own safe form at serve time;
  `internal/clone.InlineStylesheetsWithRuntimeCSS` inlines every linked
  stylesheet). Stylesheet CSS is read straight out of the live page's own
  CSSOM (`document.styleSheets`, captured by `browserclone.collectRuntimeCSS`
  just before the tab closes) rather than re-fetched independently, and
  falls back to fetching the `<link>`'s `href` (or `data-href`) only when
  that capture is missing or incomplete for a given stylesheet. This isn't
  just an optimization: some sites (github.com/login among them) load a
  stylesheet via `<link href="...">`, let the browser apply it, then
  strip or move that `href` attribute afterward — by the time this page's
  rendered DOM is captured, a second, independent fetch would have no URL
  left to fetch for that stylesheet at all, and would in any case be more
  likely to be rate-limited by the origin than the single fetch the real
  browser already made. One documented gap remains: background-image/font
  `url(...)` references inside that CSS still load live from the original
  host at serve time, rather than being rehosted — acceptable for now, not
  silent.

  This reintroduces the kind of infrastructure this service otherwise
  avoids, so two things keep it safe: every URL is validated by
  `internal/netguard` (http/s only, every resolved IP must be public —
  blocks localhost/RFC1918/link-local/cloud-metadata/CGNAT) before
  navigation and again on the page's final URL after it; and, since a
  rendering page can run arbitrary JS that issues its own sub-requests no
  app-layer check can fully police, the headless-Chrome container
  (`baitbet-browser` in docker-compose.yml) sits on its own network with no
  route to this service's database, cache, or any other internal
  container — the actual backstop is that an internal address has nowhere
  to be reached from inside that container, regardless of what the page
  running inside it tries.

## Testing

Two tiers:

- `go test ./...` — pure unit tests, no database, no network. Covers
  rendering determinism, HTML escaping of attacker-controlled content, and
  that no forms, scripts, or remote assets ever appear in output.
- `go test -tags=integration ./...` — hits a real Postgres
  (`SIM_TEST_DB_ADMIN_URL`), creating and dropping a throwaway database per
  run. Covers the full campaign lifecycle, the duplicate-action guards
  (approving or releasing twice is rejected, not silently repeated),
  handoff-token verification and cross-tenant isolation, org provisioning,
  and mail-failure handling (tested against a deliberately unreachable
  SMTP host, so the failure path is exercised for real).

## Known gaps

- No TLS in the binary — put a TLS-terminating reverse proxy in front of
  it if you're exposing it beyond a private network.
- Not yet tested against a real SMTP relay, only a local sink and a
  deliberately-down host.
- No PDF export, only CSV.
