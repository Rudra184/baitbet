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
`data:` URI, so there's never a remote image to fetch. There's no headless
browser anywhere in this service; it doesn't clone real login pages, it
renders original, form-free templates.

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
