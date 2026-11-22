package render

import (
	"regexp"
	"strings"
	"testing"

	"github.com/rudra184/baitbet/internal/content"
)

func fixture() content.Pack {
	return content.Pack{
		Version:  "2026-11-21.1",
		Scenario: "network_reauth",
		Email: content.EmailContent{
			FromName:    "IT Helpdesk",
			Subject:     "Action needed: VPN session expired",
			BodyHTML:    "Your VPN session expired. Re-authenticate to keep working.",
			ButtonLabel: "Re-authenticate",
		},
		Debrief: content.DebriefContent{
			Title:        "Verify the request",
			Intro:        "This was a security awareness exercise.",
			NextSteps:    []string{"Check unexpected requests through the portal or contact you already use.", "Report suspicious messages using your usual Outlook reporting process."},
			ClosingNote:  "Training preview. You can close this page.",
			LogoAssetKey: "mark",
		},
	}
}

// Same input, same output, every time: preview and delivery must be
// provable as the same content version, not just "close enough".
func TestDebrief_Deterministic(t *testing.T) {
	pack := fixture()
	first, err := Debrief(pack)
	if err != nil {
		t.Fatalf("Debrief: %v", err)
	}
	for i := range 20 {
		got, err := Debrief(pack)
		if err != nil {
			t.Fatalf("Debrief (run %d): %v", i, err)
		}
		if got != first {
			t.Fatalf("Debrief output not deterministic on run %d", i)
		}
	}
}

func TestEmail_Deterministic(t *testing.T) {
	pack := fixture()
	first, err := Email(pack)
	if err != nil {
		t.Fatalf("Email: %v", err)
	}
	for i := range 20 {
		got, err := Email(pack)
		if err != nil {
			t.Fatalf("Email (run %d): %v", i, err)
		}
		if got != first {
			t.Fatalf("Email output not deterministic on run %d", i)
		}
	}
}

// A scenario author's text must never become live markup or script: this
// is the property that lets us call typed Pack fields "data", not content
// the page trusts.
func TestDebrief_EscapesAuthoredText(t *testing.T) {
	pack := fixture()
	pack.Debrief.Title = `<script>alert(1)</script>`
	pack.Debrief.Intro = `"><img src=x onerror=alert(2)>`
	pack.Debrief.NextSteps = []string{`</li><li onclick="alert(3)">injected`}

	out, err := Debrief(pack)
	if err != nil {
		t.Fatalf("Debrief: %v", err)
	}
	// The injected markup must never become a live tag: its "<" has to come
	// out escaped (&lt;), so no real <script>, <img> or <li onclick=...>
	// element is formed, however the attack text reads once inert.
	assertNoFormsScriptsOrRemoteAssets(t, out)
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("expected the injected <script> tag to come out escaped, not dropped silently")
	}
	if !strings.Contains(out, "&lt;img src=x onerror=alert(2)&gt;") {
		t.Fatal("expected the injected <img> tag to come out escaped, not dropped silently")
	}
}

func TestEmail_EscapesAuthoredText(t *testing.T) {
	pack := fixture()
	pack.Email.Subject = `<img src=x onerror=alert(1)>`
	pack.Email.BodyHTML = `<script>steal()</script>`

	out, err := Email(pack)
	if err != nil {
		t.Fatalf("Email: %v", err)
	}
	assertNoFormsScriptsOrRemoteAssets(t, out)
	if !strings.Contains(out, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Fatal("expected the injected <img> tag to come out escaped, not dropped silently")
	}
	if !strings.Contains(out, "&lt;script&gt;steal()&lt;/script&gt;") {
		t.Fatal("expected the injected <script> tag to come out escaped, not dropped silently")
	}
}

var (
	formTagRe   = regexp.MustCompile(`(?i)<form[\s>]`)
	inputTagRe  = regexp.MustCompile(`(?i)<input[\s>]`)
	scriptTagRe = regexp.MustCompile(`(?i)<script[\s>]`)
	remoteURLRe = regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?https?://`)
)

// The debrief is the page a recipient actually lands on after a simulated
// click: it must never collect credentials or reach the network, however
// the content pack is authored.
func TestDebrief_NoFormsScriptsOrRemoteAssets(t *testing.T) {
	out, err := Debrief(fixture())
	if err != nil {
		t.Fatalf("Debrief: %v", err)
	}
	assertNoFormsScriptsOrRemoteAssets(t, out)
}

func TestEmail_NoFormsScriptsOrRemoteAssets(t *testing.T) {
	out, err := Email(fixture())
	if err != nil {
		t.Fatalf("Email: %v", err)
	}
	assertNoFormsScriptsOrRemoteAssets(t, out)
}

func assertNoFormsScriptsOrRemoteAssets(t *testing.T, html string) {
	t.Helper()
	if formTagRe.MatchString(html) {
		t.Fatal("output contains a <form> element")
	}
	if inputTagRe.MatchString(html) {
		t.Fatal("output contains an <input> element")
	}
	if scriptTagRe.MatchString(html) {
		t.Fatal("output contains a <script> element")
	}
	if remoteURLRe.MatchString(html) {
		t.Fatal("output references a remote http(s):// URL")
	}
}

func TestDebrief_LogoIsLocalDataURI(t *testing.T) {
	out, err := Debrief(fixture())
	if err != nil {
		t.Fatalf("Debrief: %v", err)
	}
	// html/template HTML-entity-escapes the URL within the attribute value
	// (the "+" of "svg+xml" becomes "&#43;"); browsers decode entities in
	// attribute values before using the URL, so this is the correct,
	// still-functional escaped form, not a corrupted one.
	if !strings.Contains(out, `src="data:image/svg&#43;xml;base64,`) {
		t.Fatal("expected the logo to be embedded as a local data: URI")
	}
}

func TestDebrief_UnknownAssetKeyRejected(t *testing.T) {
	pack := fixture()
	pack.Debrief.LogoAssetKey = "not-a-real-asset"
	if _, err := Debrief(pack); err == nil {
		t.Fatal("expected an error for an unresolvable local asset key, got nil")
	}
}

func TestEmailDelivery_ContainsExactTrackingLink(t *testing.T) {
	out, err := EmailDelivery(fixture(), "http://127.0.0.1:8765/t/abc123token")
	if err != nil {
		t.Fatalf("EmailDelivery: %v", err)
	}
	if !strings.Contains(out, `href="http://127.0.0.1:8765/t/abc123token"`) {
		t.Fatal("expected the exact tracking URL to appear as the button href")
	}
}

func TestEmailDelivery_EscapesAuthoredText(t *testing.T) {
	pack := fixture()
	pack.Email.BodyHTML = `<script>steal()</script>`
	out, err := EmailDelivery(pack, "http://127.0.0.1:8765/t/abc123token")
	if err != nil {
		t.Fatalf("EmailDelivery: %v", err)
	}
	if strings.Contains(out, "<script>steal()</script>") {
		t.Fatal("raw <script> tag survived into rendered output")
	}
	if !strings.Contains(out, "&lt;script&gt;steal()&lt;/script&gt;") {
		t.Fatal("expected the injected <script> tag to come out escaped")
	}
}

func TestValidate_RejectsIncompletePack(t *testing.T) {
	if _, err := Debrief(content.Pack{}); err == nil {
		t.Fatal("expected Debrief to reject an empty pack")
	}
	if _, err := Email(content.Pack{}); err == nil {
		t.Fatal("expected Email to reject an empty pack")
	}
}
