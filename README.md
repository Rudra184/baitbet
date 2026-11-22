# baitbet

A self-hosted phishing-simulation and security-awareness tool. Run a
campaign, send real email, track who clicks, and show them what they fell
for — without using a fake login page or capturing any credentials.

Think GoPhish, but server-rendered, no headless browser, and small enough
to read in an afternoon.

## Why

Most "phishing simulation" tools in this space either shell out to a
Chromium instance to clone a real login page, or need a heavyweight stack
just to send one email and track one click. baitbet does neither: it's a
single Go binary with one Postgres database, and the pages it serves are
original — plain `html/template`, auto-escaped, no forms, no scripts, no
remote assets. It trains people to recognize a phishing email, not to type
a password into a convincing fake.

## What it does

- A full campaign lifecycle: draft → test send → approve → release → close.
- Real SMTP delivery through whatever mail relay you point it at.
- A tracking link per recipient, and a debrief page that tells them what
  happened and what to watch for next time, once they click.
- A CSV export of who clicked and when.
- Guardrails baked into the data layer, not just the UI: you can't approve
  or release a campaign twice, you can't edit content after it's released,
  and disabling an organization makes old tracking links go neutral
  instead of erroring (so a disabled org's state is never exposed by what
  its errors *don't* say).

## Quick start

```sh
docker compose up -d db mailsink

SIM_DB_URL='postgres://sim_user:changeme_in_production@127.0.0.1:5434/simulation_service?sslmode=disable' \
SIM_SMTP_HOST='127.0.0.1:1025' SIM_SMTP_NO_AUTH=true \
SIM_MAIL_FROM='it-helpdesk@sim.local' SIM_PUBLIC_BASE='http://127.0.0.1:8766' \
SIM_LISTEN_ADDR='127.0.0.1:8766' go run ./cmd/server
```

Then open:

- `http://127.0.0.1:8766/` — the operator workspace
- `http://127.0.0.1:8025/` — the Mailpit inbox. Everything baitbet sends
  lands here; nothing reaches a real mailbox in this setup.

For a screen-by-screen walkthrough of the actual workflow — drafting a
campaign, test-sending, approving, releasing, and reading the report — see
[USAGE.md](USAGE.md).

## Running it against something you've already built

baitbet also works as a service another application calls into, gating
access per organization with a short-lived signed token instead of running
wide open. That contract — the token shape, how tenant isolation works,
what happens when an org is disabled — is written up in
[ARCHITECTURE.md](ARCHITECTURE.md).

## Testing

```sh
go build ./... && go vet ./...
go test ./...                      # pure unit tests, no DB needed
SIM_TEST_DB_ADMIN_URL='postgres://sim_user:changeme_in_production@127.0.0.1:5434/postgres?sslmode=disable' \
  go test -tags=integration ./...
```

The integration tests spin up and tear down a throwaway database per run
against the `db` container above; they skip rather than fail if it isn't
reachable.

## Layout

```
cmd/server/        the real binary: operator workspace + tracking endpoints
cmd/preview/       offline template preview, no DB, no network
internal/content/  the content-pack type (email/debrief copy) + validation
internal/render/   html/template rendering — email, debrief, delivery email
internal/store/    Postgres access and the workflow's state-machine rules
internal/mailer/   SMTP sending
internal/authn/    handoff-token verification for the embedded mode
internal/server/   HTTP handlers and the server-rendered admin UI
```

## Known limitations

- No TLS in the binary itself — put it behind a reverse proxy if it's
  reachable from anywhere but a private network.
- Only tested against a local SMTP sink and a deliberately unreachable
  host, not a real relay yet.
- Reports export as CSV only, no PDF.

## License

MIT — see [LICENSE](LICENSE).
