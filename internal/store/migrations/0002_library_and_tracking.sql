-- Adds the reusable content library (templates, landing pages, mailer
-- profiles), richer recipient/event tracking fields, and remediation
-- records. Additive only, per the no-rollback convention in store.go.

CREATE TABLE IF NOT EXISTS templates (
    id              TEXT PRIMARY KEY,
    organization_id TEXT REFERENCES organizations(id), -- NULL = builtin/global
    name            TEXT NOT NULL,
    scenario        TEXT NOT NULL DEFAULT '',
    subject         TEXT NOT NULL DEFAULT '',
    sender_name     TEXT NOT NULL DEFAULT '',
    sender_email    TEXT NOT NULL DEFAULT '',
    body_html       TEXT NOT NULL DEFAULT '',
    cta_text        TEXT NOT NULL DEFAULT 'Open',
    is_builtin      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS landing_pages (
    id                TEXT PRIMARY KEY,
    organization_id   TEXT REFERENCES organizations(id), -- NULL = builtin/global
    name              TEXT NOT NULL,
    page_type         TEXT NOT NULL DEFAULT 'login',
    html              TEXT NOT NULL DEFAULT '',
    capture_username  BOOLEAN NOT NULL DEFAULT TRUE,
    capture_password  BOOLEAN NOT NULL DEFAULT TRUE,
    redirect_url      TEXT NOT NULL DEFAULT '',
    logo_asset_key    TEXT NOT NULL DEFAULT '',
    brand_color       TEXT NOT NULL DEFAULT '',
    is_builtin        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- smtp_password_encrypted holds a server-side-encrypted secret; this
-- service never returns it to a caller once set (see store.go doc on
-- MailerProfile).
CREATE TABLE IF NOT EXISTS mailer_profiles (
    id                      TEXT PRIMARY KEY,
    organization_id         TEXT NOT NULL REFERENCES organizations(id),
    name                    TEXT NOT NULL,
    from_name               TEXT NOT NULL DEFAULT '',
    from_email              TEXT NOT NULL DEFAULT '',
    reply_to                TEXT NOT NULL DEFAULT '',
    smtp_host               TEXT NOT NULL DEFAULT '',
    smtp_username           TEXT NOT NULL DEFAULT '',
    smtp_password_encrypted TEXT NOT NULL DEFAULT '',
    no_auth                 BOOLEAN NOT NULL DEFAULT FALSE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS template_id TEXT REFERENCES templates(id);
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS landing_page_id TEXT REFERENCES landing_pages(id);
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS objective TEXT NOT NULL DEFAULT '';
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS business_unit TEXT NOT NULL DEFAULT '';

ALTER TABLE recipients ADD COLUMN IF NOT EXISTS full_name TEXT NOT NULL DEFAULT '';
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS department TEXT NOT NULL DEFAULT '';
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT '';
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS location TEXT NOT NULL DEFAULT '';
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS delivered_at TIMESTAMPTZ;
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS opened_at TIMESTAMPTZ;
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS click_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS submission_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS reported BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS reported_at TIMESTAMPTZ;
-- The landing page's username field only, if the campaign's landing page
-- captures one. NEVER a password column - the password value itself is
-- never received or persisted anywhere in this service.
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS captured_username TEXT;
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS risk_score REAL NOT NULL DEFAULT 0;
ALTER TABLE recipients ADD COLUMN IF NOT EXISTS risk_level TEXT NOT NULL DEFAULT 'low';

ALTER TABLE events ADD COLUMN IF NOT EXISTS ip_address TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN IF NOT EXISTS user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN IF NOT EXISTS device TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN IF NOT EXISTS ip_region TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN IF NOT EXISTS is_scanner_event BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE events ADD COLUMN IF NOT EXISTS confidence REAL NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS remediations (
    id            TEXT PRIMARY KEY,
    campaign_id   TEXT NOT NULL REFERENCES campaigns(id),
    recipient_id  TEXT NOT NULL REFERENCES recipients(id),
    type          TEXT NOT NULL, -- training | password_reset | session_revoke
    status        TEXT NOT NULL DEFAULT 'pending', -- pending | in_progress | completed | failed
    assignee      TEXT NOT NULL DEFAULT '',
    due_date      DATE,
    completed_at  TIMESTAMPTZ,
    verified_source TEXT NOT NULL DEFAULT '',
    external_ref  TEXT NOT NULL DEFAULT '',
    notes         TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_templates_org ON templates(organization_id);
CREATE INDEX IF NOT EXISTS idx_landing_pages_org ON landing_pages(organization_id);
CREATE INDEX IF NOT EXISTS idx_mailer_profiles_org ON mailer_profiles(organization_id);
CREATE INDEX IF NOT EXISTS idx_remediations_campaign ON remediations(campaign_id);
CREATE INDEX IF NOT EXISTS idx_remediations_recipient ON remediations(recipient_id);
