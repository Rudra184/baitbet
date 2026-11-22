//go:build integration

package store

import (
	"context"
	"testing"
	"time"
)

func mustOrg(t *testing.T, st *Store, ctx context.Context) string {
	t.Helper()
	id := "org_test"
	if err := st.UpsertOrganization(ctx, id, "Test org", true); err != nil {
		t.Fatalf("UpsertOrganization: %v", err)
	}
	return id
}

func mustDraftCampaign(t *testing.T, st *Store, ctx context.Context, orgID string) Campaign {
	t.Helper()
	c := Campaign{
		ID:             "camp_" + t.Name(),
		OrganizationID: orgID,
		Name:           "Test campaign",
		Scenario:       "network_reauth",
		ContentVersion: "v1",
		EmailSubject:   "subject",
		DebriefTitle:   "title",
		MailerProfile:  "local-sink",
		Status:         "draft",
	}
	if err := st.CreateCampaign(ctx, c); err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	got, err := st.GetCampaign(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCampaign: %v", err)
	}
	return got
}

func TestOrganization_EnableDisableRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	id := mustOrg(t, st, ctx)

	org, err := st.GetOrganization(ctx, id)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if !org.Enabled {
		t.Fatal("expected org to be enabled by default")
	}

	if err := st.SetOrganizationEnabled(ctx, id, false); err != nil {
		t.Fatalf("SetOrganizationEnabled: %v", err)
	}
	org, err = st.GetOrganization(ctx, id)
	if err != nil {
		t.Fatalf("GetOrganization after disable: %v", err)
	}
	if org.Enabled {
		t.Fatal("expected org to be disabled")
	}
}

func TestCampaignLifecycle_HappyPath(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	orgID := mustOrg(t, st, ctx)
	c := mustDraftCampaign(t, st, ctx, orgID)

	if c.Status != "draft" {
		t.Fatalf("expected draft, got %s", c.Status)
	}

	// draft -> tested (test-send does this directly via SetStatus in the
	// server layer; exercised here at the store layer)
	if err := st.SetStatus(ctx, c.ID, "tested"); err != nil {
		t.Fatalf("SetStatus tested: %v", err)
	}

	if err := st.Approve(ctx, c.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	approved, err := st.GetCampaign(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCampaign after approve: %v", err)
	}
	if approved.Status != "approved" {
		t.Fatalf("expected approved, got %s", approved.Status)
	}
	if approved.ApprovedContentVersion == nil || *approved.ApprovedContentVersion != c.ContentVersion {
		t.Fatal("expected approved_content_version to snapshot the content_version at approval time")
	}

	if err := st.Release(ctx, c.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Release: %v", err)
	}
	released, err := st.GetCampaign(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCampaign after release: %v", err)
	}
	if released.Status != "released" || released.ReleasedAt == nil {
		t.Fatalf("expected released with ReleasedAt set, got status=%s releasedAt=%v", released.Status, released.ReleasedAt)
	}

	if err := st.CloseCampaign(ctx, c.ID); err != nil {
		t.Fatalf("CloseCampaign: %v", err)
	}
	closed, err := st.GetCampaign(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCampaign after close: %v", err)
	}
	if closed.Status != "closed" || closed.ClosedAt == nil {
		t.Fatalf("expected closed with ClosedAt set, got status=%s closedAt=%v", closed.Status, closed.ClosedAt)
	}
}

// Approve/Release/CloseCampaign are the server-side guards behind the
// workflow plan's "Changes require a new approval" and the backlog's
// duplicate-release-prevention acceptance test - each must only succeed
// once, from the specific prior status, never as a no-op success.
func TestApprove_RejectsWrongStatus(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	orgID := mustOrg(t, st, ctx)
	c := mustDraftCampaign(t, st, ctx, orgID) // still "draft", not "tested"

	if err := st.Approve(ctx, c.ID); err == nil {
		t.Fatal("expected Approve to reject a campaign still in draft")
	}
}

func TestRelease_RejectsWrongStatusAndDuplicateRelease(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	orgID := mustOrg(t, st, ctx)
	c := mustDraftCampaign(t, st, ctx, orgID)

	if err := st.Release(ctx, c.ID, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("expected Release to reject a campaign still in draft")
	}

	if err := st.SetStatus(ctx, c.ID, "tested"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if err := st.Approve(ctx, c.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := st.Release(ctx, c.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("first Release should succeed: %v", err)
	}
	if err := st.Release(ctx, c.ID, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("expected the second Release call to be rejected (duplicate-release guard)")
	}
}

func TestCloseCampaign_RejectsWrongStatus(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	orgID := mustOrg(t, st, ctx)
	c := mustDraftCampaign(t, st, ctx, orgID)

	if err := st.CloseCampaign(ctx, c.ID); err == nil {
		t.Fatal("expected CloseCampaign to reject a campaign still in draft")
	}
}

func TestUpdateDraftContent_OnlyAllowedInDraft(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	orgID := mustOrg(t, st, ctx)
	c := mustDraftCampaign(t, st, ctx, orgID)

	c.Name = "renamed while draft"
	if err := st.UpdateDraftContent(ctx, c.ID, c, "v2"); err != nil {
		t.Fatalf("UpdateDraftContent while draft: %v", err)
	}
	updated, err := st.GetCampaign(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCampaign: %v", err)
	}
	if updated.Name != "renamed while draft" || updated.ContentVersion != "v2" {
		t.Fatalf("expected the edit and version bump to apply, got name=%q version=%q", updated.Name, updated.ContentVersion)
	}

	// Once past draft, content is locked - no silent resume of editing.
	if err := st.SetStatus(ctx, c.ID, "tested"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	c.Name = "attempted edit after tested"
	if err := st.UpdateDraftContent(ctx, c.ID, c, "v3"); err == nil {
		t.Fatal("expected UpdateDraftContent to reject editing a non-draft campaign")
	}
}

func TestRecipientsAndEvents_RoundTrip(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	orgID := mustOrg(t, st, ctx)
	c := mustDraftCampaign(t, st, ctx, orgID)

	rec := Recipient{ID: "rec_1", CampaignID: c.ID, Email: "alice@example.test", Token: "tok_1"}
	if err := st.AddRecipient(ctx, rec); err != nil {
		t.Fatalf("AddRecipient: %v", err)
	}

	got, err := st.GetRecipientByToken(ctx, "tok_1")
	if err != nil {
		t.Fatalf("GetRecipientByToken: %v", err)
	}
	if got.Email != "alice@example.test" {
		t.Fatalf("expected alice@example.test, got %s", got.Email)
	}

	if err := st.RecordEvent(ctx, got.ID, "mail_accepted", ""); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}
	if err := st.RecordEvent(ctx, got.ID, "debrief_viewed", ""); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	counts, err := st.EventCountsForCampaign(ctx, c.ID)
	if err != nil {
		t.Fatalf("EventCountsForCampaign: %v", err)
	}
	if len(counts) != 2 {
		t.Fatalf("expected 2 distinct event types, got %d (%+v)", len(counts), counts)
	}
}

// Report reconciliation (backlog item C): the recipient list is the data
// a CSV report is built from, so it must reflect send outcomes accurately,
// including a failed send - distinct from "no attempt made".
func TestMarkSent_RecordsSendFailureDistinctly(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	orgID := mustOrg(t, st, ctx)
	c := mustDraftCampaign(t, st, ctx, orgID)

	rec := Recipient{ID: "rec_fail", CampaignID: c.ID, Email: "bob@example.test", Token: "tok_fail"}
	if err := st.AddRecipient(ctx, rec); err != nil {
		t.Fatalf("AddRecipient: %v", err)
	}
	sendErr := context.DeadlineExceeded // stand-in for a real SMTP error
	if err := st.MarkSent(ctx, rec.ID, sendErr); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	list, err := st.ListRecipients(ctx, c.ID)
	if err != nil {
		t.Fatalf("ListRecipients: %v", err)
	}
	if len(list) != 1 || list[0].SendError == nil {
		t.Fatalf("expected the recorded recipient to carry a non-nil SendError, got %+v", list)
	}
	if list[0].SentAt == nil {
		t.Fatal("expected SentAt to be set even on a failed send (an attempt was made)")
	}
}
