-- Service-owned schema. Separate database, separate credentials from the
-- core platform (per PLAN.md §1.3/§2) - this migration never runs against
-- compliance_platform's own Postgres instance.

CREATE TABLE IF NOT EXISTS organizations (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    -- Standalone-mode activation switch. In integrated mode this is set from
    -- the core platform's entitlement endpoint instead (PLAN.md §2.1) - the
    -- column is the same either way so the enable/disable code path doesn't
    -- fork between modes.
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS campaigns (
    id                TEXT PRIMARY KEY,
    organization_id   TEXT NOT NULL REFERENCES organizations(id),
    name              TEXT NOT NULL,
    scenario          TEXT NOT NULL,
    -- The exact content revision approval is tied to (Quality plan: "Tie
    -- approval to the exact content version, recipient list and mailer").
    content_version   TEXT NOT NULL,
    email_subject     TEXT NOT NULL DEFAULT '',
    email_body_html   TEXT NOT NULL DEFAULT '',
    email_button_label TEXT NOT NULL DEFAULT '',
    email_from_name   TEXT NOT NULL DEFAULT '',
    debrief_title     TEXT NOT NULL DEFAULT '',
    debrief_intro     TEXT NOT NULL DEFAULT '',
    debrief_next_steps TEXT NOT NULL DEFAULT '', -- newline-separated
    debrief_closing   TEXT NOT NULL DEFAULT '',
    mailer_profile    TEXT NOT NULL DEFAULT '',
    -- draft -> tested -> approved -> released -> closed. Any edit to a
    -- released campaign's content must go through draft again (no silent
    -- resume) - see record_content_change below.
    status            TEXT NOT NULL DEFAULT 'draft',
    approved_content_version TEXT, -- snapshot of content_version at approval time
    approved_at       TIMESTAMPTZ,
    released_at       TIMESTAMPTZ,
    closed_at         TIMESTAMPTZ,
    expires_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS recipients (
    id            TEXT PRIMARY KEY,
    campaign_id   TEXT NOT NULL REFERENCES campaigns(id),
    email         TEXT NOT NULL,
    is_test       BOOLEAN NOT NULL DEFAULT FALSE,
    token         TEXT NOT NULL UNIQUE,
    sent_at       TIMESTAMPTZ,
    send_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS events (
    id            BIGSERIAL PRIMARY KEY,
    recipient_id  TEXT NOT NULL REFERENCES recipients(id),
    event_type    TEXT NOT NULL, -- mail_accepted, opened, reported, debrief_viewed
    metadata      TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_recipients_campaign ON recipients(campaign_id);
CREATE INDEX IF NOT EXISTS idx_events_recipient ON events(recipient_id);
CREATE INDEX IF NOT EXISTS idx_campaigns_org ON campaigns(organization_id);
