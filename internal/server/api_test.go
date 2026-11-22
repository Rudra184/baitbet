//go:build integration

package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/rudra184/baitbet/internal/store"
)

func jsonRequest(t *testing.T, method, url string, body any) *http.Request {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode json: %v", err)
	}
}

func TestAPI_CreateListGetDraft(t *testing.T) {
	ts, _ := newTestServer(t)

	var created store.Campaign
	resp, err := http.DefaultClient.Do(jsonRequest(t, http.MethodPost, ts.URL+"/api/campaigns", campaignRequest{
		Name: "API campaign", Scenario: "network_reauth", EmailSubject: "s", DebriefTitle: "t",
		DebriefNextSteps: []string{"Step one", "Step two"},
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	decodeJSON(t, resp, &created)
	if created.ID == "" || created.Status != "draft" {
		t.Fatalf("unexpected created campaign: %+v", created)
	}

	var list []store.Campaign
	resp, err = http.Get(ts.URL + "/api/campaigns")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	decodeJSON(t, resp, &list)
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("expected the one created campaign in the list, got %+v", list)
	}

	var fetched store.Campaign
	resp, err = http.Get(ts.URL + "/api/campaigns/" + created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	decodeJSON(t, resp, &fetched)
	if fetched.Name != "API campaign" {
		t.Fatalf("unexpected fetched campaign: %+v", fetched)
	}

	var updated store.Campaign
	resp, err = http.DefaultClient.Do(jsonRequest(t, http.MethodPut, ts.URL+"/api/campaigns/"+created.ID+"/draft", campaignRequest{
		Name: "Renamed", Scenario: "network_reauth", EmailSubject: "s2", DebriefTitle: "t2",
	}))
	if err != nil {
		t.Fatalf("update draft: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating a draft, got %d", resp.StatusCode)
	}
	decodeJSON(t, resp, &updated)
	if updated.Name != "Renamed" || updated.ContentVersion == created.ContentVersion {
		t.Fatalf("expected a new name and a bumped content_version, got %+v", updated)
	}
}

func TestAPI_GetUnknownCampaignIs404(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/campaigns/no-such-id")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown campaign, got %d", resp.StatusCode)
	}
}

func TestAPI_TestSendRequiresAtLeastOneRecipient(t *testing.T) {
	ts, _ := newTestServer(t)
	var created store.Campaign
	resp, _ := http.DefaultClient.Do(jsonRequest(t, http.MethodPost, ts.URL+"/api/campaigns", campaignRequest{
		Name: "c", EmailSubject: "s", DebriefTitle: "t",
	}))
	decodeJSON(t, resp, &created)

	resp, err := http.DefaultClient.Do(jsonRequest(t, http.MethodPost, ts.URL+"/api/campaigns/"+created.ID+"/test-send", recipientsRequest{}))
	if err != nil {
		t.Fatalf("test-send: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 with no test recipients, got %d", resp.StatusCode)
	}
}

// TestAPI_FullWorkflow drives draft -> tested -> approved -> released ->
// closed entirely through the JSON API, the same lifecycle
// TestWorkflow_HappyPathWithMailFailure exercises through HTML forms - the
// two encodings must reach the same end state.
func TestAPI_FullWorkflow(t *testing.T) {
	ts, _ := newTestServer(t)

	var c store.Campaign
	resp, _ := http.DefaultClient.Do(jsonRequest(t, http.MethodPost, ts.URL+"/api/campaigns", campaignRequest{
		Name: "Full workflow", Scenario: "network_reauth", EmailSubject: "s", DebriefTitle: "t",
	}))
	decodeJSON(t, resp, &c)

	resp, err := http.DefaultClient.Do(jsonRequest(t, http.MethodPost, ts.URL+"/api/campaigns/"+c.ID+"/test-send",
		recipientsRequest{Recipients: []string{"tester@example.com"}}))
	if err != nil {
		t.Fatalf("test-send: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 from test-send, got %d: %s", resp.StatusCode, b)
	}
	decodeJSON(t, resp, &c)
	if c.Status != "tested" {
		t.Fatalf("expected status 'tested' after test-send, got %q", c.Status)
	}

	resp, err = http.DefaultClient.Do(jsonRequest(t, http.MethodPost, ts.URL+"/api/campaigns/"+c.ID+"/approve", nil))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	decodeJSON(t, resp, &c)
	if c.Status != "approved" {
		t.Fatalf("expected status 'approved', got %q", c.Status)
	}

	resp, err = http.DefaultClient.Do(jsonRequest(t, http.MethodPost, ts.URL+"/api/campaigns/"+c.ID+"/release",
		recipientsRequest{Recipients: []string{"victim@example.com"}}))
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	decodeJSON(t, resp, &c)
	if c.Status != "released" {
		t.Fatalf("expected status 'released', got %q", c.Status)
	}

	// A second release must be rejected, not silently repeated.
	resp, err = http.DefaultClient.Do(jsonRequest(t, http.MethodPost, ts.URL+"/api/campaigns/"+c.ID+"/release",
		recipientsRequest{Recipients: []string{"victim2@example.com"}}))
	if err != nil {
		t.Fatalf("second release: %v", err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on a duplicate release, got %d", resp.StatusCode)
	}

	var recipients []store.Recipient
	resp, err = http.Get(ts.URL + "/api/campaigns/" + c.ID + "/recipients")
	if err != nil {
		t.Fatalf("list recipients: %v", err)
	}
	decodeJSON(t, resp, &recipients)
	if len(recipients) != 2 {
		t.Fatalf("expected 2 recipients (1 test + 1 real), got %d", len(recipients))
	}

	resp, err = http.DefaultClient.Do(jsonRequest(t, http.MethodPost, ts.URL+"/api/campaigns/"+c.ID+"/close", nil))
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	decodeJSON(t, resp, &c)
	if c.Status != "closed" {
		t.Fatalf("expected status 'closed', got %q", c.Status)
	}
}
