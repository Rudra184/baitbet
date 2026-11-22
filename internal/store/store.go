// Package store is the service's only database access point. Nothing
// outside this package touches SQL directly, and no host application
// embedding this service touches this database at all - separate
// credentials, separate instance, separate migrations (see
// ARCHITECTURE.md).
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed all:migrations
var migrationsFS embed.FS

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies every embedded .sql file once, in filename order. There's
// no down-migration support - schema changes are additive only; nothing
// here has needed a rollback yet.
func (s *Store) Migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		if err != nil {
			return fmt.Errorf("store: read %s: %w", name, err)
		}
		if _, err := s.pool.Exec(ctx, string(b)); err != nil {
			return fmt.Errorf("store: apply %s: %w", name, err)
		}
	}
	return nil
}

// --- Organizations ---

type Organization struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (s *Store) UpsertOrganization(ctx context.Context, id, name string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO organizations (id, name, enabled) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = $2, enabled = $3`, id, name, enabled)
	return err
}

func (s *Store) SetOrganizationEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE organizations SET enabled = $2 WHERE id = $1`, id, enabled)
	return err
}

func (s *Store) GetOrganization(ctx context.Context, id string) (Organization, error) {
	var o Organization
	err := s.pool.QueryRow(ctx, `SELECT id, name, enabled FROM organizations WHERE id = $1`, id).
		Scan(&o.ID, &o.Name, &o.Enabled)
	return o, err
}

// --- Campaigns ---

type Campaign struct {
	ID                     string     `json:"id"`
	OrganizationID         string     `json:"organization_id"`
	Name                   string     `json:"name"`
	Scenario               string     `json:"scenario"`
	ContentVersion         string     `json:"content_version"`
	EmailSubject           string     `json:"email_subject"`
	EmailBodyHTML          string     `json:"email_body_html"`
	EmailButtonLabel       string     `json:"email_button_label"`
	EmailFromName          string     `json:"email_from_name"`
	DebriefTitle           string     `json:"debrief_title"`
	DebriefIntro           string     `json:"debrief_intro"`
	DebriefNextSteps       string     `json:"debrief_next_steps"` // newline-separated
	DebriefClosing         string     `json:"debrief_closing"`
	MailerProfile          string     `json:"mailer_profile"`
	Status                 string     `json:"status"`
	ApprovedContentVersion *string    `json:"approved_content_version,omitempty"`
	ApprovedAt             *time.Time `json:"approved_at,omitempty"`
	ReleasedAt             *time.Time `json:"released_at,omitempty"`
	ClosedAt               *time.Time `json:"closed_at,omitempty"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// CampaignSummary is a Campaign plus the counts the list view needs,
// computed in one query rather than one round trip per campaign.
type CampaignSummary struct {
	Campaign
	RecipientCount int `json:"recipient_count"`
	SentCount      int `json:"sent_count"`
	ClickedCount   int `json:"clicked_count"`
}

const campaignColumns = `id, organization_id, name, scenario, content_version,
	email_subject, email_body_html, email_button_label, email_from_name,
	debrief_title, debrief_intro, debrief_next_steps, debrief_closing,
	mailer_profile, status, approved_content_version, approved_at,
	released_at, closed_at, expires_at, created_at, updated_at`

func scanCampaign(row interface {
	Scan(dest ...any) error
}) (Campaign, error) {
	var c Campaign
	err := row.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Scenario, &c.ContentVersion,
		&c.EmailSubject, &c.EmailBodyHTML, &c.EmailButtonLabel, &c.EmailFromName,
		&c.DebriefTitle, &c.DebriefIntro, &c.DebriefNextSteps, &c.DebriefClosing,
		&c.MailerProfile, &c.Status, &c.ApprovedContentVersion, &c.ApprovedAt,
		&c.ReleasedAt, &c.ClosedAt, &c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *Store) CreateCampaign(ctx context.Context, c Campaign) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO campaigns (id, organization_id, name, scenario, content_version,
			email_subject, email_body_html, email_button_label, email_from_name,
			debrief_title, debrief_intro, debrief_next_steps, debrief_closing,
			mailer_profile, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		c.ID, c.OrganizationID, c.Name, c.Scenario, c.ContentVersion,
		c.EmailSubject, c.EmailBodyHTML, c.EmailButtonLabel, c.EmailFromName,
		c.DebriefTitle, c.DebriefIntro, c.DebriefNextSteps, c.DebriefClosing,
		c.MailerProfile, c.Status)
	return err
}

func (s *Store) GetCampaign(ctx context.Context, id string) (Campaign, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+campaignColumns+` FROM campaigns WHERE id = $1`, id)
	return scanCampaign(row)
}

func (s *Store) ListCampaigns(ctx context.Context, orgID string) ([]Campaign, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+campaignColumns+` FROM campaigns WHERE organization_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateDraftContent overwrites a draft campaign's content fields and bumps
// its content_version. Only callable while status = 'draft': editing
// released content without a new version would let a change slip out
// without a fresh approval.
func (s *Store) UpdateDraftContent(ctx context.Context, id string, c Campaign, newVersion string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE campaigns SET name=$2, scenario=$3, content_version=$4,
			email_subject=$5, email_body_html=$6, email_button_label=$7, email_from_name=$8,
			debrief_title=$9, debrief_intro=$10, debrief_next_steps=$11, debrief_closing=$12,
			mailer_profile=$13, updated_at=now()
		WHERE id=$1 AND status='draft'`,
		id, c.Name, c.Scenario, newVersion,
		c.EmailSubject, c.EmailBodyHTML, c.EmailButtonLabel, c.EmailFromName,
		c.DebriefTitle, c.DebriefIntro, c.DebriefNextSteps, c.DebriefClosing,
		c.MailerProfile)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: campaign %s is not editable (not in draft)", id)
	}
	return nil
}

func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE campaigns SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	return err
}

// Approve locks in the exact content version this approval covers. Release
// later checks the campaign's current content_version still matches this
// snapshot - if a draft edit happened in between (shouldn't be possible once
// approved, but defence in depth), release refuses rather than shipping
// content nobody signed off on.
func (s *Store) Approve(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE campaigns
		SET status='approved', approved_content_version=content_version, approved_at=now(), updated_at=now()
		WHERE id=$1 AND status='tested'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: campaign %s must be in status 'tested' to approve", id)
	}
	return nil
}

// Release is a one-way, idempotency-guarded transition: it only succeeds
// once (status must still be 'approved'), which is the server-side
// duplicate-release guard the backlog calls for.
func (s *Store) Release(ctx context.Context, id string, expiresAt time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE campaigns
		SET status='released', released_at=now(), expires_at=$2, updated_at=now()
		WHERE id=$1 AND status='approved' AND content_version = approved_content_version`,
		id, expiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: campaign %s must be in status 'approved' with unchanged content to release", id)
	}
	return nil
}

func (s *Store) CloseCampaign(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE campaigns SET status='closed', closed_at=now(), updated_at=now() WHERE id=$1 AND status='released'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: campaign %s must be in status 'released' to close", id)
	}
	return nil
}

// --- Recipients & events ---

type Recipient struct {
	ID         string     `json:"id"`
	CampaignID string     `json:"campaign_id"`
	Email      string     `json:"email"`
	IsTest     bool       `json:"is_test"`
	Token      string     `json:"token"`
	SentAt     *time.Time `json:"sent_at,omitempty"`
	SendError  *string    `json:"send_error,omitempty"`
}

func (s *Store) AddRecipient(ctx context.Context, r Recipient) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO recipients (id, campaign_id, email, is_test, token)
		VALUES ($1,$2,$3,$4,$5)`, r.ID, r.CampaignID, r.Email, r.IsTest, r.Token)
	return err
}

func (s *Store) MarkSent(ctx context.Context, id string, sendErr error) error {
	var errText *string
	if sendErr != nil {
		s := sendErr.Error()
		errText = &s
	}
	_, err := s.pool.Exec(ctx, `UPDATE recipients SET sent_at=now(), send_error=$2 WHERE id=$1`, id, errText)
	return err
}

func (s *Store) ListRecipients(ctx context.Context, campaignID string) ([]Recipient, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, campaign_id, email, is_test, token, sent_at, send_error FROM recipients WHERE campaign_id=$1 ORDER BY created_at`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recipient
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(&r.ID, &r.CampaignID, &r.Email, &r.IsTest, &r.Token, &r.SentAt, &r.SendError); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRecipientByToken(ctx context.Context, token string) (Recipient, error) {
	var r Recipient
	err := s.pool.QueryRow(ctx, `SELECT id, campaign_id, email, is_test, token, sent_at, send_error FROM recipients WHERE token=$1`, token).
		Scan(&r.ID, &r.CampaignID, &r.Email, &r.IsTest, &r.Token, &r.SentAt, &r.SendError)
	return r, err
}

func (s *Store) RecordEvent(ctx context.Context, recipientID, eventType, metadata string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO events (recipient_id, event_type, metadata) VALUES ($1,$2,$3)`, recipientID, eventType, metadata)
	return err
}

type EventCount struct {
	EventType string `json:"event_type"`
	Count     int    `json:"count"`
}

func (s *Store) EventCountsForCampaign(ctx context.Context, campaignID string) ([]EventCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.event_type, COUNT(*) FROM events e
		JOIN recipients r ON r.id = e.recipient_id
		WHERE r.campaign_id = $1
		GROUP BY e.event_type ORDER BY e.event_type`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventCount
	for rows.Next() {
		var ec EventCount
		if err := rows.Scan(&ec.EventType, &ec.Count); err != nil {
			return nil, err
		}
		out = append(out, ec)
	}
	return out, rows.Err()
}
