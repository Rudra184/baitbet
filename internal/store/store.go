// Package store is this service's only database access point - its own
// credentials and instance, never shared with a host app (ARCHITECTURE.md).
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed all:migrations
var migrationsFS embed.FS

type Store struct {
	pool *pgxpool.Pool
}

func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// ForeignKeyConstraintName returns the violated constraint's name, or ""
// if err isn't a foreign-key violation - several different constraints can
// raise 23503, so callers choosing an error message must not assume which
// one fired (see server/api.go's apiFail).
func ForeignKeyConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return pgErr.ConstraintName
	}
	return ""
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

// Migrate applies every embedded .sql file once, in filename order (no down-migrations).
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

	// Snapshotted at create/update-draft time; a later library edit never
	// alters what was actually sent.
	TemplateID    *string `json:"template_id,omitempty"`
	LandingPageID *string `json:"landing_page_id,omitempty"`
	Objective     string  `json:"objective"`
	BusinessUnit  string  `json:"business_unit"`
}

// CampaignSummary adds the list view's counts, computed in one query.
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
	released_at, closed_at, expires_at, created_at, updated_at,
	template_id, landing_page_id, objective, business_unit`

func scanCampaign(row interface {
	Scan(dest ...any) error
}) (Campaign, error) {
	var c Campaign
	err := row.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.Scenario, &c.ContentVersion,
		&c.EmailSubject, &c.EmailBodyHTML, &c.EmailButtonLabel, &c.EmailFromName,
		&c.DebriefTitle, &c.DebriefIntro, &c.DebriefNextSteps, &c.DebriefClosing,
		&c.MailerProfile, &c.Status, &c.ApprovedContentVersion, &c.ApprovedAt,
		&c.ReleasedAt, &c.ClosedAt, &c.ExpiresAt, &c.CreatedAt, &c.UpdatedAt,
		&c.TemplateID, &c.LandingPageID, &c.Objective, &c.BusinessUnit)
	return c, err
}

func (s *Store) CreateCampaign(ctx context.Context, c Campaign) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO campaigns (id, organization_id, name, scenario, content_version,
			email_subject, email_body_html, email_button_label, email_from_name,
			debrief_title, debrief_intro, debrief_next_steps, debrief_closing,
			mailer_profile, status, template_id, landing_page_id, objective, business_unit)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		c.ID, c.OrganizationID, c.Name, c.Scenario, c.ContentVersion,
		c.EmailSubject, c.EmailBodyHTML, c.EmailButtonLabel, c.EmailFromName,
		c.DebriefTitle, c.DebriefIntro, c.DebriefNextSteps, c.DebriefClosing,
		c.MailerProfile, c.Status, c.TemplateID, c.LandingPageID, c.Objective, c.BusinessUnit)
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

// UpdateDraftContent only applies while status='draft', so released
// content can't change without a fresh approval.
func (s *Store) UpdateDraftContent(ctx context.Context, id string, c Campaign, newVersion string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE campaigns SET name=$2, scenario=$3, content_version=$4,
			email_subject=$5, email_body_html=$6, email_button_label=$7, email_from_name=$8,
			debrief_title=$9, debrief_intro=$10, debrief_next_steps=$11, debrief_closing=$12,
			mailer_profile=$13, template_id=$14, landing_page_id=$15, objective=$16,
			business_unit=$17, updated_at=now()
		WHERE id=$1 AND status='draft'`,
		id, c.Name, c.Scenario, newVersion,
		c.EmailSubject, c.EmailBodyHTML, c.EmailButtonLabel, c.EmailFromName,
		c.DebriefTitle, c.DebriefIntro, c.DebriefNextSteps, c.DebriefClosing,
		c.MailerProfile, c.TemplateID, c.LandingPageID, c.Objective, c.BusinessUnit)
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

// Approve locks in content_version; Release refuses if it later changed.
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

// Release is one-way and idempotency-guarded: status must still be 'approved'.
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

	FullName         string     `json:"full_name"`
	Department       string     `json:"department"`
	Role             string     `json:"role"`
	Location         string     `json:"location"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	OpenedAt         *time.Time `json:"opened_at,omitempty"`
	ClickCount       int        `json:"click_count"`
	SubmissionCount  int        `json:"submission_count"`
	Reported         bool       `json:"reported"`
	ReportedAt       *time.Time `json:"reported_at,omitempty"`
	CapturedUsername *string    `json:"captured_username,omitempty"`
	RiskScore        float64    `json:"risk_score"`
	RiskLevel        string     `json:"risk_level"`
}

const recipientColumns = `id, campaign_id, email, is_test, token, sent_at, send_error,
	full_name, department, role, location, delivered_at, opened_at, click_count,
	submission_count, reported, reported_at, captured_username, risk_score, risk_level`

func scanRecipient(row interface {
	Scan(dest ...any) error
}) (Recipient, error) {
	var r Recipient
	err := row.Scan(&r.ID, &r.CampaignID, &r.Email, &r.IsTest, &r.Token, &r.SentAt, &r.SendError,
		&r.FullName, &r.Department, &r.Role, &r.Location, &r.DeliveredAt, &r.OpenedAt, &r.ClickCount,
		&r.SubmissionCount, &r.Reported, &r.ReportedAt, &r.CapturedUsername, &r.RiskScore, &r.RiskLevel)
	return r, err
}

// AddRecipient registers a row, either an ad-hoc test-send/release address
// or a pre-launch audience row (SentAt nil until Release sends to it).
func (s *Store) AddRecipient(ctx context.Context, r Recipient) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO recipients (id, campaign_id, email, is_test, token, full_name, department, role, location)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		r.ID, r.CampaignID, r.Email, r.IsTest, r.Token, r.FullName, r.Department, r.Role, r.Location)
	return err
}

// FindRecipientByEmail prevents Release from creating a duplicate row.
func (s *Store) FindRecipientByEmail(ctx context.Context, campaignID, email string) (Recipient, bool, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+recipientColumns+` FROM recipients
		WHERE campaign_id=$1 AND email=$2 AND is_test=FALSE`, campaignID, email)
	r, err := scanRecipient(row)
	if err != nil {
		return Recipient{}, false, nil
	}
	return r, true, nil
}

// ListUnsentAudience is the audience Release sends to.
func (s *Store) ListUnsentAudience(ctx context.Context, campaignID string) ([]Recipient, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+recipientColumns+` FROM recipients
		WHERE campaign_id=$1 AND is_test=FALSE AND sent_at IS NULL ORDER BY created_at`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recipient
	for rows.Next() {
		r, err := scanRecipient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteCampaign removes it and everything scoped to it (FKs don't cascade).
func (s *Store) DeleteCampaign(ctx context.Context, orgID, campaignID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM campaigns WHERE id=$1 AND organization_id=$2)`,
		campaignID, orgID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("store: campaign %s not found", campaignID)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM remediations WHERE campaign_id=$1`, campaignID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM events WHERE recipient_id IN (SELECT id FROM recipients WHERE campaign_id=$1)`, campaignID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM recipients WHERE campaign_id=$1`, campaignID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM campaigns WHERE id=$1`, campaignID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteRecipient(ctx context.Context, campaignID, recipientID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM recipients WHERE id=$1 AND campaign_id=$2 AND sent_at IS NULL`, recipientID, campaignID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store: recipient %s is not deletable (already sent, or not found)", recipientID)
	}
	return nil
}

func (s *Store) MarkSent(ctx context.Context, id string, sendErr error) error {
	var errText *string
	if sendErr != nil {
		s := sendErr.Error()
		errText = &s
	}
	// delivered_at = sent_at: no delivery-receipt webhook, so SMTP accept is the best signal.
	_, err := s.pool.Exec(ctx, `UPDATE recipients SET sent_at=now(), delivered_at=now(), send_error=$2 WHERE id=$1`, id, errText)
	return err
}

func (s *Store) ListRecipients(ctx context.Context, campaignID string) ([]Recipient, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+recipientColumns+` FROM recipients WHERE campaign_id=$1 ORDER BY created_at`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recipient
	for rows.Next() {
		r, err := scanRecipient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRecipientByToken(ctx context.Context, token string) (Recipient, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+recipientColumns+` FROM recipients WHERE token=$1`, token)
	return scanRecipient(row)
}

func (s *Store) GetRecipient(ctx context.Context, id string) (Recipient, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+recipientColumns+` FROM recipients WHERE id=$1`, id)
	return scanRecipient(row)
}

// MarkOpened records the first-open timestamp (idempotent - a repeat open
// pixel fetch doesn't move it).
func (s *Store) MarkOpened(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE recipients SET opened_at=COALESCE(opened_at, now()) WHERE id=$1`, id)
	return err
}

func (s *Store) IncrementClickCount(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE recipients SET click_count=click_count+1 WHERE id=$1`, id)
	return err
}

// RecordSubmission takes a username, never a password - callers must not pass one.
func (s *Store) RecordSubmission(ctx context.Context, id string, capturedUsername *string) error {
	_, err := s.pool.Exec(ctx, `UPDATE recipients SET submission_count=submission_count+1,
		captured_username=COALESCE($2, captured_username) WHERE id=$1`, id, capturedUsername)
	return err
}

func (s *Store) MarkReported(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE recipients SET reported=TRUE, reported_at=COALESCE(reported_at, now()) WHERE id=$1`, id)
	return err
}

func (s *Store) UpdateRisk(ctx context.Context, id string, score float64, level string) error {
	_, err := s.pool.Exec(ctx, `UPDATE recipients SET risk_score=$2, risk_level=$3 WHERE id=$1`, id, score, level)
	return err
}

type Event struct {
	ID             int64     `json:"id"`
	RecipientID    string    `json:"recipient_id"`
	EventType      string    `json:"event_type"`
	Metadata       string    `json:"metadata"`
	IPAddress      string    `json:"ip_address"`
	UserAgent      string    `json:"user_agent"`
	Device         string    `json:"device"`
	IPRegion       string    `json:"ip_region"`
	IsScannerEvent bool      `json:"is_scanner_event"`
	Confidence     float64   `json:"confidence"`
	CreatedAt      time.Time `json:"created_at"`
}

// RecordEvent appends one tracked action. Never pass a credential in metadata.
func (s *Store) RecordEvent(ctx context.Context, recipientID, eventType, metadata string) error {
	return s.recordEvent(ctx, recipientID, eventType, metadata, "", "", "", "", false, 1)
}

func (s *Store) RecordEventDetailed(ctx context.Context, recipientID, eventType, metadata,
	ipAddress, userAgent, device, ipRegion string, isScannerEvent bool, confidence float64) error {
	return s.recordEvent(ctx, recipientID, eventType, metadata, ipAddress, userAgent, device, ipRegion, isScannerEvent, confidence)
}

func (s *Store) recordEvent(ctx context.Context, recipientID, eventType, metadata,
	ipAddress, userAgent, device, ipRegion string, isScannerEvent bool, confidence float64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO events (recipient_id, event_type, metadata, ip_address, user_agent, device, ip_region, is_scanner_event, confidence)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		recipientID, eventType, metadata, ipAddress, userAgent, device, ipRegion, isScannerEvent, confidence)
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

// TimelineEntry is one Live Timeline row: an event plus the recipient
// email it belongs to, newest first.
type TimelineEntry struct {
	Event
	RecipientEmail string `json:"recipient_email"`
}

func (s *Store) Timeline(ctx context.Context, campaignID string, limit int) ([]TimelineEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.recipient_id, e.event_type, e.metadata, e.ip_address, e.user_agent,
			e.device, e.ip_region, e.is_scanner_event, e.confidence, e.created_at, r.email
		FROM events e
		JOIN recipients r ON r.id = e.recipient_id
		WHERE r.campaign_id = $1
		ORDER BY e.created_at DESC
		LIMIT $2`, campaignID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TimelineEntry
	for rows.Next() {
		var t TimelineEntry
		if err := rows.Scan(&t.ID, &t.RecipientID, &t.EventType, &t.Metadata, &t.IPAddress,
			&t.UserAgent, &t.Device, &t.IPRegion, &t.IsScannerEvent, &t.Confidence, &t.CreatedAt, &t.RecipientEmail); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RecipientEvents returns the (non-deleted) events for one recipient, for
// risk recomputation.
func (s *Store) RecipientEvents(ctx context.Context, recipientID string) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, recipient_id, event_type, metadata, ip_address, user_agent, device, ip_region, is_scanner_event, confidence, created_at
		FROM events WHERE recipient_id=$1 ORDER BY created_at`, recipientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.RecipientID, &e.EventType, &e.Metadata, &e.IPAddress,
			&e.UserAgent, &e.Device, &e.IPRegion, &e.IsScannerEvent, &e.Confidence, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Stats is the funnel + percentage-metric snapshot for one campaign,
// fields and formulas matching the old compliance-platform module's
// _compute_stats exactly.
type Stats struct {
	Recipients               int     `json:"recipients"`
	Sent                     int     `json:"sent"`
	Delivered                int     `json:"delivered"`
	Opened                   int     `json:"opened"`
	Clicked                  int     `json:"clicked"`
	Submitted                int     `json:"submitted"`
	Reported                 int     `json:"reported"`
	TrainingAssigned         int     `json:"training_assigned"`
	TrainingCompleted        int     `json:"training_completed"`
	ResetRequired            int     `json:"reset_required"`
	ResetCompleted           int     `json:"reset_completed"`
	ClickRate                float64 `json:"click_rate"`
	SubmissionRate           float64 `json:"submission_rate"`
	SubmissionAfterClickRate float64 `json:"submission_after_click_rate"`
	ReportRate               float64 `json:"report_rate"`
	TrainingCompletionRate   float64 `json:"training_completion_rate"`
	ResetCompletionRate      float64 `json:"reset_completion_rate"`
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(int(float64(a)/float64(b)*1000+0.5)) / 10
}

func (s *Store) ComputeStats(ctx context.Context, campaignID string) (Stats, error) {
	recipients, err := s.ListRecipients(ctx, campaignID)
	if err != nil {
		return Stats{}, err
	}
	var st Stats
	for _, r := range recipients {
		st.Recipients++
		if r.SentAt != nil {
			st.Sent++
		}
		if r.DeliveredAt != nil {
			st.Delivered++
		}
		if r.OpenedAt != nil {
			st.Opened++
		}
		if r.ClickCount > 0 {
			st.Clicked++
		}
		if r.SubmissionCount > 0 {
			st.Submitted++
		}
		if r.Reported {
			st.Reported++
		}
	}
	remediations, err := s.RemediationsForCampaign(ctx, campaignID)
	if err != nil {
		return Stats{}, err
	}
	for _, rem := range remediations {
		switch rem.Type {
		case "training":
			st.TrainingAssigned++
			if rem.Status == "completed" {
				st.TrainingCompleted++
			}
		case "password_reset":
			st.ResetRequired++
			if rem.Status == "completed" {
				st.ResetCompleted++
			}
		}
	}

	base := st.Delivered
	if base == 0 {
		base = st.Sent
	}
	if base == 0 {
		base = st.Recipients
	}
	st.ClickRate = pct(st.Clicked, base)
	st.SubmissionRate = pct(st.Submitted, base)
	st.SubmissionAfterClickRate = pct(st.Submitted, st.Clicked)
	st.ReportRate = pct(st.Reported, base)
	st.TrainingCompletionRate = pct(st.TrainingCompleted, st.TrainingAssigned)
	st.ResetCompletionRate = pct(st.ResetCompleted, st.ResetRequired)
	return st, nil
}
