package store

import (
	"context"
	"time"
)

// Remediation is a follow-up record (training, forced reset, session
// revoke); baitbet only stores/serves it, a host app performs the real
// action and PATCHes the result here.
type Remediation struct {
	ID             string     `json:"id"`
	CampaignID     string     `json:"campaign_id"`
	RecipientID    string     `json:"recipient_id"`
	Type           string     `json:"type"`   // training | password_reset | session_revoke
	Status         string     `json:"status"` // pending | in_progress | completed | failed
	Assignee       string     `json:"assignee"`
	DueDate        *time.Time `json:"due_date,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	VerifiedSource string     `json:"verified_source"`
	ExternalRef    string     `json:"external_ref"`
	Notes          string     `json:"notes"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

const remediationColumns = `id, campaign_id, recipient_id, type, status, assignee, due_date,
	completed_at, verified_source, external_ref, notes, created_at, updated_at`

func scanRemediation(row interface{ Scan(dest ...any) error }) (Remediation, error) {
	var r Remediation
	err := row.Scan(&r.ID, &r.CampaignID, &r.RecipientID, &r.Type, &r.Status, &r.Assignee,
		&r.DueDate, &r.CompletedAt, &r.VerifiedSource, &r.ExternalRef, &r.Notes, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *Store) CreateRemediation(ctx context.Context, r Remediation) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO remediations (id, campaign_id, recipient_id, type, status, assignee, due_date, notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		r.ID, r.CampaignID, r.RecipientID, r.Type, r.Status, r.Assignee, r.DueDate, r.Notes)
	return err
}

func (s *Store) RemediationsForCampaign(ctx context.Context, campaignID string) ([]Remediation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+remediationColumns+` FROM remediations WHERE campaign_id=$1 ORDER BY created_at`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Remediation
	for rows.Next() {
		r, err := scanRemediation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RemediationsForRecipient(ctx context.Context, recipientID string) ([]Remediation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+remediationColumns+` FROM remediations WHERE recipient_id=$1 ORDER BY created_at`, recipientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Remediation
	for rows.Next() {
		r, err := scanRemediation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetRemediation(ctx context.Context, id string) (Remediation, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+remediationColumns+` FROM remediations WHERE id=$1`, id)
	return scanRemediation(row)
}

// UpdateRemediationStatus only touches status/verified_source/external_ref/notes.
func (s *Store) UpdateRemediationStatus(ctx context.Context, id, status, verifiedSource, externalRef, notes string) error {
	var completedAt *time.Time
	if status == "completed" {
		now := time.Now()
		completedAt = &now
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE remediations SET status=$2, completed_at=COALESCE($3, completed_at),
			verified_source=$4, external_ref=$5, notes=$6, updated_at=now()
		WHERE id=$1`, id, status, completedAt, verifiedSource, externalRef, notes)
	return err
}
