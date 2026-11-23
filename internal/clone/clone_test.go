package clone

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestSanitize_StripsScript(t *testing.T) {
	out, err := Sanitize(`<html><body><script>alert(1)</script><p>hi</p></body></html>`)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if contains(out, "<script") {
		t.Fatalf("expected <script> to be stripped, got: %s", out)
	}
	if !contains(out, "<p>hi</p>") {
		t.Fatalf("expected ordinary content to survive, got: %s", out)
	}
}

func TestSanitize_StripsForm(t *testing.T) {
	out, err := Sanitize(`<html><body><form action="https://evil.example/collect"><input name="password"></form></body></html>`)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if contains(out, "<form") {
		t.Fatalf("expected <form> to be stripped, got: %s", out)
	}
}

func TestSanitize_StripsEventHandlers(t *testing.T) {
	out, err := Sanitize(`<html><body><div onclick="steal()">click me</div></body></html>`)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if contains(out, "onclick") {
		t.Fatalf("expected onclick attribute to be stripped, got: %s", out)
	}
}

func TestSanitize_StripsRemoteSrcAndHref(t *testing.T) {
	out, err := Sanitize(`<html><body><img src="https://cdn.example/logo.png"><a href="http://example.com">link</a></body></html>`)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if contains(out, "https://cdn.example") || contains(out, "http://example.com") {
		t.Fatalf("expected remote src/href to be stripped, got: %s", out)
	}
}

func TestSanitize_StripsIframeObjectEmbed(t *testing.T) {
	out, err := Sanitize(`<html><body><iframe src="https://evil.example"></iframe><object data="x.swf"></object><embed src="x.swf"></body></html>`)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	for _, tag := range []string{"<iframe", "<object", "<embed"} {
		if contains(out, tag) {
			t.Fatalf("expected %s to be stripped, got: %s", tag, out)
		}
	}
}

func TestFetch_RejectsNonHTTP(t *testing.T) {
	if _, err := Fetch("file:///etc/passwd"); err == nil {
		t.Fatal("expected Fetch to reject a non-http(s) URL")
	}
	if _, err := Fetch("javascript:alert(1)"); err == nil {
		t.Fatal("expected Fetch to reject a javascript: URL")
	}
}

func TestSanitizeWithFormMarker_ReplacesFirstForm(t *testing.T) {
	out, found, err := SanitizeWithFormMarker(`<html><body><form action="https://evil.example/collect"><input name="password"></form><form action="https://accounts.example/oauth" class="sso-btn"><button>Continue with Example</button></form></body></html>`)
	if err != nil {
		t.Fatalf("SanitizeWithFormMarker: %v", err)
	}
	if !found {
		t.Fatal("expected a form to be found")
	}
	if contains(out, "<form") {
		t.Fatalf("expected no <form> tags to remain (the first becomes the marker, later ones are demoted to <div>), got: %s", out)
	}
	if !contains(out, FormMarkerID) {
		t.Fatalf("expected marker id %q in output, got: %s", FormMarkerID, out)
	}
	if strings.Count(out, FormMarkerID) != 1 {
		t.Fatalf("expected exactly one marker (only the first form gets one), got: %s", out)
	}
	if !contains(out, "Continue with Example") {
		t.Fatalf("expected a later form's content (e.g. an OAuth button) to survive, demoted to an inert <div>, got: %s", out)
	}
	if contains(out, "accounts.example") {
		t.Fatalf("expected a demoted form's action attribute to be dropped, got: %s", out)
	}
	if !contains(out, `class="sso-btn"`) {
		t.Fatalf("expected a demoted form's non-action attributes (e.g. class) to survive, got: %s", out)
	}
}

func TestSanitizeWithFormMarker_PreservesRealFormWithPasswordField(t *testing.T) {
	out, found, err := SanitizeWithFormMarker(`<html><body><form action="https://real.example/session" method="post" enctype="application/x-www-form-urlencoded">` +
		`<input type="text" class="form-control" name="login" autocomplete="username">` +
		`<input type="password" class="form-control" name="pass">` +
		`<input type="submit" class="btn btn-primary" value="Sign in"></form></body></html>`)
	if err != nil {
		t.Fatalf("SanitizeWithFormMarker: %v", err)
	}
	if !found {
		t.Fatal("expected the form to be found")
	}
	if contains(out, FormMarkerID) {
		t.Fatalf("expected the real form to be kept, not replaced by the marker, got: %s", out)
	}
	if !contains(out, "<form") {
		t.Fatalf("expected the real <form> element to survive, got: %s", out)
	}
	if contains(out, "real.example") {
		t.Fatalf("expected the real action to be replaced by the submit sentinel, got: %s", out)
	}
	if !contains(out, `action="`+SubmitActionSentinel+`"`) {
		t.Fatalf("expected the submit sentinel in the form's action, got: %s", out)
	}
	if !contains(out, `name="`+UsernameFieldSentinel+`"`) || !contains(out, `name="`+PasswordFieldSentinel+`"`) {
		t.Fatalf("expected both field sentinels, got: %s", out)
	}
	if !contains(out, `class="form-control"`) || !contains(out, `class="btn btn-primary"`) {
		t.Fatalf("expected the real form's classes to survive untouched, got: %s", out)
	}
	if contains(out, "enctype") {
		t.Fatalf("expected enctype to be stripped, got: %s", out)
	}
}

func TestSanitizeWithFormMarker_FirstFormWithoutPasswordFallsBackToMarker(t *testing.T) {
	out, found, err := SanitizeWithFormMarker(`<html><body><form action="https://real.example/search"><input type="text" name="q"></form></body></html>`)
	if err != nil {
		t.Fatalf("SanitizeWithFormMarker: %v", err)
	}
	if !found {
		t.Fatal("expected a form to be found")
	}
	if !contains(out, FormMarkerID) {
		t.Fatalf("expected a form with no password field to fall back to the marker, got: %s", out)
	}
	if contains(out, "<form") {
		t.Fatalf("expected the password-less form to be replaced, got: %s", out)
	}
}

func TestSanitizeWithFormMarker_NoFormPresent(t *testing.T) {
	out, found, err := SanitizeWithFormMarker(`<html><body><p>no form here</p></body></html>`)
	if err != nil {
		t.Fatalf("SanitizeWithFormMarker: %v", err)
	}
	if found {
		t.Fatal("expected found=false when there is no form")
	}
	if contains(out, FormMarkerID) {
		t.Fatalf("expected no marker when there is no form, got: %s", out)
	}
}

func TestInlineStylesheets_FetchesAndInlines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Write([]byte(`body { background: url(bg.png); color: #333; }`))
	}))
	defer srv.Close()

	origFetch := doFetch
	doFetch = func(url string) ([]byte, string, error) {
		resp, err := http.Get(url)
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
		body := make([]byte, 0)
		buf := make([]byte, 4096)
		for {
			n, rerr := resp.Body.Read(buf)
			body = append(body, buf[:n]...)
			if rerr != nil {
				break
			}
		}
		return body, url, nil
	}
	defer func() { doFetch = origFetch }()

	doc, err := html.Parse(strings.NewReader(`<html><head><link rel="stylesheet" href="/style.css"></head><body><p>hi</p></body></html>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := InlineStylesheets(doc, srv.URL+"/"); err != nil {
		t.Fatalf("InlineStylesheets: %v", err)
	}
	var buf strings.Builder
	if err := html.Render(&buf, doc); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if contains(out, "<link") {
		t.Fatalf("expected <link rel=stylesheet> to be replaced, got: %s", out)
	}
	if !contains(out, "<style") || !contains(out, "color: #333") {
		t.Fatalf("expected fetched CSS inlined in a <style> tag, got: %s", out)
	}
	if !contains(out, srv.URL+"/bg.png") {
		t.Fatalf("expected relative url() rewritten to absolute against the stylesheet's own URL, got: %s", out)
	}
}

func TestInlineStylesheetsWithRuntimeCSS_PrefersRuntimeOverFetch(t *testing.T) {
	origFetch := doFetch
	doFetch = func(url string) ([]byte, string, error) {
		t.Fatalf("doFetch should not be called when runtime CSS already has this stylesheet: %s", url)
		return nil, "", nil
	}
	defer func() { doFetch = origFetch }()

	doc, err := html.Parse(strings.NewReader(`<html><head><link rel="stylesheet" href="https://example.com/style.css"></head><body><p>hi</p></body></html>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	runtimeCSS := map[string]string{
		"https://example.com/style.css": `body { color: #123456; }`,
	}
	if err := InlineStylesheetsWithRuntimeCSS(doc, "https://example.com/", runtimeCSS); err != nil {
		t.Fatalf("InlineStylesheetsWithRuntimeCSS: %v", err)
	}
	var buf strings.Builder
	if err := html.Render(&buf, doc); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if contains(out, "<link") {
		t.Fatalf("expected <link rel=stylesheet> to be replaced, got: %s", out)
	}
	if !contains(out, "#123456") {
		t.Fatalf("expected runtime CSS inlined verbatim, got: %s", out)
	}
}

func TestInlineStylesheetsWithRuntimeCSS_FallsBackToDataHrefWhenHrefMissing(t *testing.T) {
	// e.g. github.com/login: page JS strips <link href> and stashes it in data-href instead.
	doc, err := html.Parse(strings.NewReader(`<html><head><link rel="stylesheet" data-href="https://example.com/theme.css"></head><body><p>hi</p></body></html>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	runtimeCSS := map[string]string{
		"https://example.com/theme.css": `body { color: #abcdef; }`,
	}
	if err := InlineStylesheetsWithRuntimeCSS(doc, "https://example.com/", runtimeCSS); err != nil {
		t.Fatalf("InlineStylesheetsWithRuntimeCSS: %v", err)
	}
	var buf strings.Builder
	if err := html.Render(&buf, doc); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !contains(out, "#abcdef") {
		t.Fatalf("expected runtime CSS matched via data-href and inlined, got: %s", out)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
