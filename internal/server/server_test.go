//go:build integration

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rudra184/baitbet/internal/authn"
	"github.com/rudra184/baitbet/internal/mailer"
	"github.com/rudra184/baitbet/internal/secrets"
	"github.com/rudra184/baitbet/internal/store"
)

func testSecretsBox(t *testing.T) *secrets.Box {
	t.Helper()
	box, err := secrets.NewBox(secrets.RandomKey())
	if err != nil {
		t.Fatalf("secrets.NewBox: %v", err)
	}
	return box
}

const testOrgID = "org_test"
const testHandoffSecret = "integration-test-handoff-secret"

// noFollowClient captures a redirect's Location header instead of following
// it, the same way the manual curl walkthrough used -D - earlier.
func noFollowClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	ctx := t.Context()
	st := newTestStoreForServer(t)
	if err := st.UpsertOrganization(ctx, testOrgID, "Test org", true); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	// An unreachable local address: dials fail fast and deterministically,
	// without needing a real (or even fake) SMTP listener. This is exactly
	// the "mail failure handling" case the backlog calls for - exercised
	// for real here, not just asserted in prose.
	mp := mailer.Profile{Name: "broken-sink", Host: "127.0.0.1:1", From: "it@sim.local", NoAuth: true}

	srv := New(st, mp, testSecretsBox(t), "http://sim.invalid", "", testOrgID, "") // "" = standalone mode, no token required
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts, st
}

// newIntegratedTestServer is the "embedded in a platform" variant: a
// handoff secret is configured, so every admin route requires a valid
// signed token (internal/authn), the same as production would.
func newIntegratedTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	ctx := t.Context()
	st := newTestStoreForServer(t)
	for _, org := range []string{testOrgID, "org_other"} {
		if err := st.UpsertOrganization(ctx, org, "Test org "+org, true); err != nil {
			t.Fatalf("seed org: %v", err)
		}
	}
	mp := mailer.Profile{Name: "broken-sink", Host: "127.0.0.1:1", From: "it@sim.local", NoAuth: true}
	srv := New(st, mp, testSecretsBox(t), "http://sim.invalid", testHandoffSecret, "", "")
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return ts, st
}

func mustToken(t *testing.T, orgID string) string {
	t.Helper()
	tok, err := authn.Mint(testHandoffSecret, orgID, "user_1", "admin", time.Minute)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok
}

func authedRequest(t *testing.T, method, url, token string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

func TestIntegratedMode_RejectsRequestsWithoutAToken(t *testing.T) {
	ts, _ := newIntegratedTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with no handoff token, got %d", resp.StatusCode)
	}
}

func TestIntegratedMode_RejectsTokenSignedWithWrongSecret(t *testing.T) {
	ts, _ := newIntegratedTestServer(t)
	badTok, err := authn.Mint("not-the-real-secret", testOrgID, "user_1", "admin", time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	req := authedRequest(t, http.MethodGet, ts.URL+"/", badTok, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a token signed with the wrong secret, got %d", resp.StatusCode)
	}
}

func TestIntegratedMode_ValidTokenWorksAndScopesToItsOrg(t *testing.T) {
	ts, st := newIntegratedTestServer(t)
	ctx := t.Context()
	tok := mustToken(t, testOrgID)

	form := url.Values{
		"name": {"Token-scoped campaign"}, "scenario": {"network_reauth"},
		"mailer_profile": {"broken-sink"}, "email_subject": {"s"},
		"debrief_title": {"t"},
	}
	req := authedRequest(t, http.MethodPost, ts.URL+"/campaigns", tok, strings.NewReader(form.Encode()))
	client := noFollowClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create via token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 303, got %d: %s", resp.StatusCode, b)
	}
	id := strings.TrimPrefix(resp.Header.Get("Location"), "/campaigns/")

	campaigns, err := st.ListCampaigns(ctx, testOrgID)
	if err != nil || len(campaigns) != 1 || campaigns[0].ID != id {
		t.Fatalf("expected the campaign to be created under %s, got %+v err=%v", testOrgID, campaigns, err)
	}

	// A valid token for a DIFFERENT org must not be able to see or act on
	// this campaign - a mismatch must look identical to "doesn't exist".
	otherTok := mustToken(t, "org_other")
	req2 := authedRequest(t, http.MethodGet, ts.URL+"/campaigns/"+id, otherTok, nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("cross-org get: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when org_other requests org_test's campaign, got %d", resp2.StatusCode)
	}

	req3 := authedRequest(t, http.MethodPost, ts.URL+"/campaigns/"+id+"/approve", otherTok, nil)
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatalf("cross-org approve: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when org_other tries to approve org_test's campaign, got %d", resp3.StatusCode)
	}
}

func createDraftCampaign(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	form := url.Values{
		"name":               {"Integration test campaign"},
		"scenario":           {"network_reauth"},
		"mailer_profile":     {"broken-sink"},
		"email_from_name":    {"IT Helpdesk"},
		"email_subject":      {"Action needed"},
		"email_body":         {"Body text"},
		"email_button_label": {"Go"},
		"debrief_title":      {"Verify the request"},
		"debrief_intro":      {"Awareness exercise"},
		"debrief_next_steps": {"Step one\nStep two"},
		"debrief_closing":    {"Done"},
	}
	client := noFollowClient()
	resp, err := client.PostForm(ts.URL+"/campaigns", form)
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 303 from create, got %d: %s", resp.StatusCode, b)
	}
	loc := resp.Header.Get("Location")
	id := strings.TrimPrefix(loc, "/campaigns/")
	if id == "" || id == loc {
		t.Fatalf("could not parse campaign id from Location %q", loc)
	}
	return id
}

func TestWorkflow_HappyPathWithMailFailure(t *testing.T) {
	ts, st := newTestServer(t)
	client := noFollowClient()
	id := createDraftCampaign(t, ts)

	c, err := st.GetCampaign(t.Context(), id)
	if err != nil || c.Status != "draft" {
		t.Fatalf("expected draft after create, got %+v err=%v", c, err)
	}

	// Test — send to a named test mailbox. The mailer is unreachable, so the
	// send itself fails, but the workflow still advances and records the
	// failure on the recipient row rather than silently losing it.
	resp, err := client.PostForm(ts.URL+"/campaigns/"+id+"/test-send", url.Values{"test_recipients": {"qa1@example.test"}})
	if err != nil {
		t.Fatalf("test-send: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 from test-send even with a failed send, got %d", resp.StatusCode)
	}
	c, _ = st.GetCampaign(t.Context(), id)
	if c.Status != "tested" {
		t.Fatalf("expected status=tested after test-send despite mail failure, got %s", c.Status)
	}
	recipients, _ := st.ListRecipients(t.Context(), id)
	if len(recipients) != 1 || recipients[0].SendError == nil {
		t.Fatalf("expected the test recipient to carry a recorded send error, got %+v", recipients)
	}

	// Approve, then confirm a duplicate approve is rejected, not a silent success.
	resp, err = client.Post(ts.URL+"/campaigns/"+id+"/approve", "", nil)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 from approve, got %d", resp.StatusCode)
	}
	resp, _ = client.Post(ts.URL+"/campaigns/"+id+"/approve", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 from a duplicate approve, got %d", resp.StatusCode)
	}

	// Release to the real recipient list, then confirm duplicate release
	// is rejected (this is the backlog's duplicate-release-prevention test).
	resp, err = client.PostForm(ts.URL+"/campaigns/"+id+"/release", url.Values{"recipients": {"alice@example.test"}})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 from release, got %d", resp.StatusCode)
	}
	resp, _ = client.PostForm(ts.URL+"/campaigns/"+id+"/release", url.Values{"recipients": {"alice@example.test"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 from a duplicate release, got %d", resp.StatusCode)
	}

	// The real recipient's tracking link must resolve to the debrief page
	// and record an event - the thing an actual recipient would trigger.
	recipients, _ = st.ListRecipients(t.Context(), id)
	var aliceToken string
	for _, r := range recipients {
		if r.Email == "alice@example.test" {
			aliceToken = r.Token
		}
	}
	if aliceToken == "" {
		t.Fatal("expected a recipient row for alice@example.test after release")
	}
	trackResp, err := http.Get(ts.URL + "/t/" + aliceToken)
	if err != nil {
		t.Fatalf("track: %v", err)
	}
	body, _ := io.ReadAll(trackResp.Body)
	trackResp.Body.Close()
	if !strings.Contains(string(body), "Verify the request") {
		t.Fatalf("expected the debrief page content, got: %s", body)
	}
	counts, _ := st.EventCountsForCampaign(t.Context(), id)
	foundViewed := false
	for _, ec := range counts {
		if ec.EventType == "debrief_viewed" {
			foundViewed = true
		}
	}
	if !foundViewed {
		t.Fatalf("expected a debrief_viewed event to be recorded, got %+v", counts)
	}

	// Close, then confirm a duplicate close is rejected.
	resp, err = client.Post(ts.URL+"/campaigns/"+id+"/close", "", nil)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 from close, got %d", resp.StatusCode)
	}
	resp, _ = client.Post(ts.URL+"/campaigns/"+id+"/close", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 from a duplicate close, got %d", resp.StatusCode)
	}

	// Report reconciliation: the CSV must include every recipient this
	// workflow actually created, by email.
	reportResp, err := http.Get(ts.URL + "/campaigns/" + id + "/report.csv")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	reportBody, _ := io.ReadAll(reportResp.Body)
	reportResp.Body.Close()
	for _, want := range []string{"qa1@example.test", "alice@example.test"} {
		if !strings.Contains(string(reportBody), want) {
			t.Fatalf("expected report to contain %q, got:\n%s", want, reportBody)
		}
	}
}

func TestTrack_UnknownTokenIsNeutral(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/t/does-not-exist")
	if err != nil {
		t.Fatalf("track: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected a neutral 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "no longer active") {
		t.Fatalf("expected the neutral closed page, got: %s", body)
	}
}

// Disabling the org must make an already-released recipient's old link go
// neutral and record no new event.
func TestTrack_DisabledOrgIsNeutralAndRecordsNoNewEvent(t *testing.T) {
	ts, st := newTestServer(t)
	ctx := t.Context()
	client := noFollowClient()
	id := createDraftCampaign(t, ts)

	mustAdvanceToReleased(t, client, ts, id)

	recipients, _ := st.ListRecipients(ctx, id)
	if len(recipients) == 0 {
		t.Fatal("expected at least one recipient after release")
	}
	token := recipients[0].Token

	// One legitimate view before disabling, to have a non-zero baseline.
	http.Get(ts.URL + "/t/" + token)
	before, _ := st.EventCountsForCampaign(ctx, id)
	totalBefore := sumCounts(before)

	if err := st.SetOrganizationEnabled(ctx, testOrgID, false); err != nil {
		t.Fatalf("disable org: %v", err)
	}
	t.Cleanup(func() { st.SetOrganizationEnabled(ctx, testOrgID, true) })

	resp, err := http.Get(ts.URL + "/t/" + token)
	if err != nil {
		t.Fatalf("track after disable: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "no longer active") {
		t.Fatalf("expected the neutral closed page once the org is disabled, got: %s", body)
	}

	after, _ := st.EventCountsForCampaign(ctx, id)
	if sumCounts(after) != totalBefore {
		t.Fatalf("expected no new event to be recorded for a disabled org's old link: before=%d after=%d", totalBefore, sumCounts(after))
	}
}

func mustAdvanceToReleased(t *testing.T, client *http.Client, ts *httptest.Server, id string) {
	t.Helper()
	resp, err := client.PostForm(ts.URL+"/campaigns/"+id+"/test-send", url.Values{"test_recipients": {"qa1@example.test"}})
	if err != nil {
		t.Fatalf("test-send: %v", err)
	}
	resp.Body.Close()
	resp, err = client.Post(ts.URL+"/campaigns/"+id+"/approve", "", nil)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	resp.Body.Close()
	resp, err = client.PostForm(ts.URL+"/campaigns/"+id+"/release", url.Values{"recipients": {"alice@example.test"}})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	resp.Body.Close()
}

func sumCounts(counts []store.EventCount) int {
	total := 0
	for _, c := range counts {
		total += c.Count
	}
	return total
}

func TestSyncOrg_ProvisionsAnOrgTheTokenNamesSoCampaignCreationSucceeds(t *testing.T) {
	ts, st := newIntegratedTestServer(t)
	ctx := t.Context()
	newOrgID := "org_never_seen_before"
	tok := mustToken(t, newOrgID)

	if _, err := st.GetOrganization(ctx, newOrgID); err == nil {
		t.Fatal("expected no row for an org baitbet has never seen")
	}

	body := strings.NewReader(`{"name":"Freshly synced org","enabled":true}`)
	req := authedRequest(t, http.MethodPost, ts.URL+"/internal/sync-org", tok, body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sync-org: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 204, got %d: %s", resp.StatusCode, b)
	}

	org, err := st.GetOrganization(ctx, newOrgID)
	if err != nil {
		t.Fatalf("GetOrganization after sync: %v", err)
	}
	if org.Name != "Freshly synced org" || !org.Enabled {
		t.Fatalf("unexpected org after sync: %+v", org)
	}

	// The whole point: campaign creation for this org - previously impossible,
	// FK violation on organization_id - now works.
	form := url.Values{
		"name": {"c"}, "scenario": {"network_reauth"}, "mailer_profile": {"m"},
		"email_subject": {"s"}, "debrief_title": {"t"},
	}
	createReq := authedRequest(t, http.MethodPost, ts.URL+"/campaigns", tok, strings.NewReader(form.Encode()))
	client := noFollowClient()
	createResp, err := client.Do(createReq)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusSeeOther {
		b, _ := io.ReadAll(createResp.Body)
		t.Fatalf("expected 303, got %d: %s", createResp.StatusCode, b)
	}
}
