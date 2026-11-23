package store

import (
	"context"
	"time"
)

// --- Email templates ---

type Template struct {
	ID             string    `json:"id"`
	OrganizationID *string   `json:"organization_id,omitempty"` // nil = builtin/global
	Name           string    `json:"name"`
	Scenario       string    `json:"scenario"`
	Subject        string    `json:"subject"`
	SenderName     string    `json:"sender_name"`
	SenderEmail    string    `json:"sender_email"`
	BodyHTML       string    `json:"body_html"`
	CTAText        string    `json:"cta_text"`
	IsBuiltin      bool      `json:"is_builtin"`
	CreatedAt      time.Time `json:"created_at"`
}

const templateColumns = `id, organization_id, name, scenario, subject, sender_name, sender_email, body_html, cta_text, is_builtin, created_at`

func scanTemplate(row interface{ Scan(dest ...any) error }) (Template, error) {
	var t Template
	err := row.Scan(&t.ID, &t.OrganizationID, &t.Name, &t.Scenario, &t.Subject, &t.SenderName,
		&t.SenderEmail, &t.BodyHTML, &t.CTAText, &t.IsBuiltin, &t.CreatedAt)
	return t, err
}

func (s *Store) CreateTemplate(ctx context.Context, t Template) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO templates (id, organization_id, name, scenario, subject, sender_name, sender_email, body_html, cta_text, is_builtin)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		t.ID, t.OrganizationID, t.Name, t.Scenario, t.Subject, t.SenderName, t.SenderEmail, t.BodyHTML, t.CTAText, t.IsBuiltin)
	return err
}

// ListTemplates returns an org's own templates plus every builtin one.
func (s *Store) ListTemplates(ctx context.Context, orgID string) ([]Template, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+templateColumns+` FROM templates
		WHERE organization_id = $1 OR organization_id IS NULL ORDER BY is_builtin DESC, created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTemplate(ctx context.Context, id string) (Template, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+templateColumns+` FROM templates WHERE id=$1`, id)
	return scanTemplate(row)
}

// UpdateTemplate only ever touches a caller-owned (non-builtin) row.
func (s *Store) UpdateTemplate(ctx context.Context, id, orgID string, t Template) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE templates SET name=$3, scenario=$4, subject=$5, sender_name=$6, sender_email=$7, body_html=$8, cta_text=$9
		WHERE id=$1 AND organization_id=$2 AND is_builtin=FALSE`,
		id, orgID, t.Name, t.Scenario, t.Subject, t.SenderName, t.SenderEmail, t.BodyHTML, t.CTAText)
	return err
}

func (s *Store) DeleteTemplate(ctx context.Context, id, orgID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM templates WHERE id=$1 AND organization_id=$2 AND is_builtin=FALSE`, id, orgID)
	return err
}

// --- Landing pages ---

type LandingPage struct {
	ID              string    `json:"id"`
	OrganizationID  *string   `json:"organization_id,omitempty"` // nil = builtin/global
	Name            string    `json:"name"`
	PageType        string    `json:"page_type"`
	HTML            string    `json:"html"`
	CaptureUsername bool      `json:"capture_username"`
	CapturePassword bool      `json:"capture_password"`
	RedirectURL     string    `json:"redirect_url"`
	LogoAssetKey    string    `json:"logo_asset_key"`
	BrandColor      string    `json:"brand_color"`
	IsBuiltin       bool      `json:"is_builtin"`
	CreatedAt       time.Time `json:"created_at"`
}

const landingPageColumns = `id, organization_id, name, page_type, html, capture_username, capture_password, redirect_url, logo_asset_key, brand_color, is_builtin, created_at`

func scanLandingPage(row interface{ Scan(dest ...any) error }) (LandingPage, error) {
	var p LandingPage
	err := row.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.PageType, &p.HTML, &p.CaptureUsername,
		&p.CapturePassword, &p.RedirectURL, &p.LogoAssetKey, &p.BrandColor, &p.IsBuiltin, &p.CreatedAt)
	return p, err
}

func (s *Store) CreateLandingPage(ctx context.Context, p LandingPage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO landing_pages (id, organization_id, name, page_type, html, capture_username, capture_password, redirect_url, logo_asset_key, brand_color, is_builtin)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		p.ID, p.OrganizationID, p.Name, p.PageType, p.HTML, p.CaptureUsername, p.CapturePassword,
		p.RedirectURL, p.LogoAssetKey, p.BrandColor, p.IsBuiltin)
	return err
}

func (s *Store) ListLandingPages(ctx context.Context, orgID string) ([]LandingPage, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+landingPageColumns+` FROM landing_pages
		WHERE organization_id = $1 OR organization_id IS NULL ORDER BY is_builtin DESC, created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LandingPage
	for rows.Next() {
		p, err := scanLandingPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetLandingPage(ctx context.Context, id string) (LandingPage, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+landingPageColumns+` FROM landing_pages WHERE id=$1`, id)
	return scanLandingPage(row)
}

func (s *Store) UpdateLandingPage(ctx context.Context, id, orgID string, p LandingPage) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE landing_pages SET name=$3, page_type=$4, html=$5, capture_username=$6, capture_password=$7,
			redirect_url=$8, logo_asset_key=$9, brand_color=$10
		WHERE id=$1 AND organization_id=$2 AND is_builtin=FALSE`,
		id, orgID, p.Name, p.PageType, p.HTML, p.CaptureUsername, p.CapturePassword, p.RedirectURL, p.LogoAssetKey, p.BrandColor)
	return err
}

func (s *Store) DeleteLandingPage(ctx context.Context, id, orgID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM landing_pages WHERE id=$1 AND organization_id=$2 AND is_builtin=FALSE`, id, orgID)
	return err
}

// --- Mailer profiles ---

// MailerProfile is an org's own SMTP route; SMTPPasswordEnc is encrypted
// (internal/secrets) and never sent back to the caller once set.
type MailerProfile struct {
	ID              string    `json:"id"`
	OrganizationID  string    `json:"organization_id"`
	Name            string    `json:"name"`
	FromName        string    `json:"from_name"`
	FromEmail       string    `json:"from_email"`
	ReplyTo         string    `json:"reply_to"`
	SMTPHost        string    `json:"smtp_host"`
	SMTPUsername    string    `json:"smtp_username"`
	SMTPPasswordEnc string    `json:"-"`
	NoAuth          bool      `json:"no_auth"`
	CreatedAt       time.Time `json:"created_at"`
}

const mailerProfileColumns = `id, organization_id, name, from_name, from_email, reply_to, smtp_host, smtp_username, smtp_password_encrypted, no_auth, created_at`

func scanMailerProfile(row interface{ Scan(dest ...any) error }) (MailerProfile, error) {
	var m MailerProfile
	err := row.Scan(&m.ID, &m.OrganizationID, &m.Name, &m.FromName, &m.FromEmail, &m.ReplyTo,
		&m.SMTPHost, &m.SMTPUsername, &m.SMTPPasswordEnc, &m.NoAuth, &m.CreatedAt)
	return m, err
}

func (s *Store) UpsertMailerProfile(ctx context.Context, m MailerProfile) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mailer_profiles (id, organization_id, name, from_name, from_email, reply_to, smtp_host, smtp_username, smtp_password_encrypted, no_auth)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (organization_id, name) DO UPDATE SET
			from_name=$4, from_email=$5, reply_to=$6, smtp_host=$7, smtp_username=$8,
			smtp_password_encrypted=CASE WHEN $9 = '' THEN mailer_profiles.smtp_password_encrypted ELSE $9 END,
			no_auth=$10`,
		m.ID, m.OrganizationID, m.Name, m.FromName, m.FromEmail, m.ReplyTo, m.SMTPHost, m.SMTPUsername, m.SMTPPasswordEnc, m.NoAuth)
	return err
}

func (s *Store) ListMailerProfiles(ctx context.Context, orgID string) ([]MailerProfile, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mailerProfileColumns+` FROM mailer_profiles WHERE organization_id=$1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailerProfile
	for rows.Next() {
		m, err := scanMailerProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMailerProfile(ctx context.Context, orgID, name string) (MailerProfile, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+mailerProfileColumns+` FROM mailer_profiles WHERE organization_id=$1 AND name=$2`, orgID, name)
	return scanMailerProfile(row)
}

func (s *Store) DeleteMailerProfile(ctx context.Context, orgID, name string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM mailer_profiles WHERE organization_id=$1 AND name=$2`, orgID, name)
	return err
}
