# baitbet

A standalone phishing-simulation service: campaign workflow, form-free
training content rendering, evidence and reporting. Runs independently or
is enabled per-organization from another platform via an HTTP integration
contract (see `PLAN.md` §2) — it is never imported as a Go package by a
caller; it's a service you run and talk to over HTTP, the same model as
GoPhish.

No Chromium, no headless browser, no third-party renderer: email and
debrief pages are server-rendered Go `html/template`, auto-escaped,
self-contained (local assets only, embedded via `go:embed`).

## Status

Milestones A–C (operator workspace: Draft → Preview → Test → Approve →
Release → Close, real DB, real local SMTP send, real tracking link,
evidence, CSV report) are built and verified locally, with an integration
test suite covering the workflow's guards (duplicate approve/release/close,
mail-failure handling, disabled-org neutral response). Milestones D
(multi-environment deployment, pilot) and E (legacy-module retirement) are
specified in `PLAN.md` but not yet executed — there is no pilot tenant or
production environment wired up yet. See `PLAN.md` for the full audit,
architecture, deployment specs, and backlog this was built against.

## Run it locally

```sh
docker compose up -d db mailsink

SIM_DB_URL='postgres://sim_user:changeme_in_production@127.0.0.1:5434/simulation_service?sslmode=disable' \
SIM_SMTP_HOST='127.0.0.1:1025' SIM_SMTP_NO_AUTH=true \
SIM_MAIL_FROM='it-helpdesk@sim.local' SIM_PUBLIC_BASE='http://127.0.0.1:8766' \
SIM_LISTEN_ADDR='127.0.0.1:8766' go run ./cmd/server
```

Open `http://127.0.0.1:8766/` for the operator workspace and
`http://127.0.0.1:8025/` for the Mailpit inbox (everything the service
sends lands there — nothing reaches a real mailbox from this stack).

**See `USAGE.md` for a full screen-by-screen walkthrough of the operator
workflow** (drafting, testing, approving, releasing, the evidence/report
view, and the enable/disable behavior). See `PLAN.md` §8 for the
production-readiness checklist and §9 for the handoff-token auth model
used when this runs embedded in another platform instead of standalone —
admin routes now require a signed token (`SIM_HANDOFF_SECRET`) once that's
set; unset, it stays in today's no-token local-demo mode. Several other
gaps (TLS termination, a real mail path, milestones D/E) remain — see
`PLAN.md` §8.

## Test

```sh
go build ./... && go vet ./...
go test ./...                      # internal/render: pure unit tests, no DB
SIM_TEST_DB_ADMIN_URL='postgres://sim_user:changeme_in_production@127.0.0.1:5434/postgres?sslmode=disable' \
  go test -tags=integration ./...
```

The integration tests create and drop a throwaway database per run against
the already-running `db` container; they skip (not fail) if that database
isn't reachable.

## Layout

```
cmd/server/      the real operator/tracking binary (Postgres + SMTP)
cmd/preview/     offline fixture preview, no DB, no network
internal/content/  versioned content-pack type + validation
internal/render/   html/template rendering: email, debrief, delivery email
internal/store/    Postgres access + the workflow's state-machine guards
internal/mailer/   SMTP send via an "approved mailer profile"
internal/server/   HTTP handlers + server-rendered admin templates
```
