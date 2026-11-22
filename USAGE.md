# Using baitbet

A walkthrough of the actual operator workflow, screen by screen. For
architecture and integration details, see `ARCHITECTURE.md`; for the
one-line quickstart, see `README.md`.

**Before you start**: this walkthrough runs standalone - `SIM_HANDOFF_SECRET`
unset, every request treated as one local org, no token required. That's
intentional for running baitbet on its own. The moment it's embedded behind
a host application, `SIM_HANDOFF_SECRET` must be set, and every admin
request then needs a signed token minted by that host (see `internal/authn`
and `ARCHITECTURE.md`) - don't run with it unset anywhere but `127.0.0.1`.
Everything here assumes you're running it locally per the README's
quickstart.

## 1. Start the stack

```sh
docker compose up -d db mailsink
```

`db` is this service's own Postgres (port 5434 — won't collide with any
other Postgres on 5432/5433). `mailsink` is Mailpit: a fake SMTP server at
port 1025 and a web inbox at `http://127.0.0.1:8025/`. Nothing this service
sends can leave your machine while it's pointed at `mailsink`.

```sh
SIM_DB_URL='postgres://sim_user:changeme_in_production@127.0.0.1:5434/simulation_service?sslmode=disable' \
SIM_SMTP_HOST='127.0.0.1:1025' SIM_SMTP_NO_AUTH=true \
SIM_MAIL_FROM='it-helpdesk@sim.local' SIM_PUBLIC_BASE='http://127.0.0.1:8766' \
SIM_LISTEN_ADDR='127.0.0.1:8766' go run ./cmd/server
```

On first run this creates its schema automatically (`internal/store/
migrations/0001_init.sql`) and seeds one organization, `org_local_demo`
(every campaign in this run belongs to it — there's no multi-org switch in
the UI; multi-org only exists via the handoff-token mode described in
`ARCHITECTURE.md`).

Open `http://127.0.0.1:8766/`.

## 2. Draft a campaign

Click **New campaign**. Fill in:

- **Name** — just a label for your own list, not sent anywhere.
- **Scenario** — currently one option, `network_reauth` ("VPN session
  expired"). More scenarios are a content-authoring task, not a code
  change — see `internal/content/pack.go`.
- **Mailer profile** — just a label for now. It's recorded on the campaign,
  but every send goes to whatever `SIM_SMTP_HOST` the server was started
  with. Picking between multiple configured relays isn't built yet.
- **Email fields** (from name, subject, body, button label) and **debrief
  fields** (title, intro, next steps — one per line, closing note) — this
  is the actual content a recipient will see. There's no rich-text editor;
  it's plain text, rendered through an escaped HTML template, so pasting
  HTML tags will show up as literal text, not get interpreted.

Click **Save draft**. You land on the campaign's workspace page — this one
page is used for every remaining step; you don't navigate elsewhere.

## 3. The workspace page

At the top: a progress bar (`draft → tested → approved → released →
closed`) and, right below it, an **Unresolved** box listing what's still
blocking you from moving forward (e.g. "Not yet sent to a test mailbox").
This is deliberate — read it before looking for a button to click.

Below that: live previews of the email and the debrief page, rendered from
exactly what you typed — the same rendering code that will actually be
delivered later (no separate "preview-only" version to get out of sync).

### Edit (while status = draft)

The draft form is still here and still editable. **Every save bumps the
content version** (shown next to "Status:" at the top as a timestamp-like
string). This matters later: approval is tied to one exact version.

### Test

Enter one or more email addresses (space- or comma-separated) under **Test
— send to named test mailboxes** and click **Send test**. This:

- actually sends real SMTP mail to `mailsink` (check
  `http://127.0.0.1:8025/` — you'll see it land, with the real subject and
  body),
- moves the campaign to status `tested`,
- **locks the draft form** — you can't edit content once it's left draft;
  if you need to change something, there's currently no "back to draft"
  button in the UI (the store would allow it, the handler for it isn't
  wired up yet). For now: finish the content before you test-send.

If the mailer is unreachable, the send still "succeeds" at the workflow
level (status still moves to `tested`) but the recipient row records the
failure — check the **Recipients** table at the bottom of the page; a
failed send shows its error in the Error column instead of a blank.

### Approve (status = tested)

A new section appears: **Approve — review content, sender, named
recipients and expiry**. It shows the mailer profile and the exact content
version you're about to approve. Click **Approve this exact revision**.

This is a one-way gate per revision: approving again (if you'd somehow
gotten back here) returns an error (`409 Conflict`) rather than silently
re-approving — there's no "oops, approve again" button that does anything.

### Release (status = approved)

Enter the real recipient list and click **Release now**. This:

- sends the actual delivery email (with a real, working tracking link) to
  every address you listed,
- sets a 14-day expiry on the campaign (hardcoded — not yet configurable
  from the UI),
- is also single-shot: releasing twice returns `409`, it will not send a
  duplicate wave.

### What a recipient sees

Each person on the list gets an email with a button. Clicking it hits
`/t/{token}` — a real, working link — which records a `debrief_viewed`
event and serves the debrief page (the "you've been tested" explanation
page). There's no credential-capture step here; it's a pure
awareness/training flow, not a credential-harvesting simulation — baitbet
never clones a real login page.

### Close and report (status = released)

Once you're done collecting results, click **Download report (CSV)** to
get every recipient's email, whether they were a test recipient, when they
were sent to, any send error, and their tracking token. Then click **Close
campaign**. Closing again also returns `409`.

### Evidence

Below the recipient table, the **Evidence** section shows a count per event
type (`mail_accepted`, `debrief_viewed`, etc.) for the whole campaign — the
same numbers a real report would reconcile against.

## 4. Organization enable/disable (not yet exposed in the UI)

There's no UI control for this today — it's a direct database flip, meant
to prove the behavior works, not meant as the real admin experience:

```sh
docker exec baitbet-db psql -U sim_user -d simulation_service \
  -c "update organizations set enabled=false where id='org_local_demo';"
```

With the org disabled: releasing a new campaign is blocked (`403`), and any
existing recipient's tracking link returns a neutral "This page is no
longer active" response and records no new event. Flip `enabled` back to
`true` to re-enable.

## 5. Troubleshooting

- **`address already in use` on startup** — something else is already
  bound to `SIM_LISTEN_ADDR`'s port. Find it with
  `ss -tlnp | grep 8766` (or your port) and kill it, or just pick a
  different `SIM_LISTEN_ADDR`/`SIM_PUBLIC_BASE`.
- **`409` on approve/release/close** — not a bug: it means the campaign
  isn't in the required prior status (e.g. you already approved it, or
  you're trying to release a second time). Reload the workspace page to
  see its actual current status.
- **Test/release "succeeded" but nothing shows in Mailpit** — check the
  server's stdout log for `test-send to ... failed:` / `release send to
  ... failed:`; the workflow doesn't fail the HTTP request on a send
  error, it records it on the recipient row instead (see the Recipients
  table's Error column).
- **Changed draft content after testing and it's not reflected** — expected
  per §3 above; content is locked once you leave draft. There's currently
  no "revert to draft" action in the UI.
