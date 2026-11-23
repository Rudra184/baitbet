package render

import (
	"strings"
	"testing"
)

func landingFixture() LandingPage {
	return LandingPage{
		Title:           "Sign in to continue",
		BrandColor:      "#0b1f3a",
		CaptureUsername: true,
		CapturePassword: true,
		SubmitAction:    "/t/abc123token/submit",
	}
}

func TestLanding_Deterministic(t *testing.T) {
	p := landingFixture()
	first, err := Landing(p)
	if err != nil {
		t.Fatalf("Landing: %v", err)
	}
	for i := range 20 {
		got, err := Landing(p)
		if err != nil {
			t.Fatalf("Landing (run %d): %v", i, err)
		}
		if got != first {
			t.Fatalf("Landing output not deterministic on run %d", i)
		}
	}
}

func TestLanding_EscapesAuthoredText(t *testing.T) {
	p := landingFixture()
	p.Title = `<script>alert(1)</script>`
	out, err := Landing(p)
	if err != nil {
		t.Fatalf("Landing: %v", err)
	}
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Fatal("raw <script> tag survived into rendered output")
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("expected the injected <script> tag to come out escaped, not dropped silently")
	}
}

func TestLanding_NoScriptOrRemoteAssets(t *testing.T) {
	out, err := Landing(landingFixture())
	if err != nil {
		t.Fatalf("Landing: %v", err)
	}
	if scriptTagRe.MatchString(out) {
		t.Fatal("output contains a <script> element")
	}
	if remoteURLRe.MatchString(out) {
		t.Fatal("output references a remote http(s):// URL")
	}
}

func TestLanding_ExactSubmitAction(t *testing.T) {
	out, err := Landing(landingFixture())
	if err != nil {
		t.Fatalf("Landing: %v", err)
	}
	if !strings.Contains(out, `action="/t/abc123token/submit"`) {
		t.Fatal("expected the exact submit action to appear as the form action")
	}
}

func TestLanding_FieldsRespectCaptureFlags(t *testing.T) {
	p := landingFixture()
	p.CapturePassword = false
	out, err := Landing(p)
	if err != nil {
		t.Fatalf("Landing: %v", err)
	}
	if !strings.Contains(out, `name="username"`) {
		t.Fatal("expected a username field when CaptureUsername is true")
	}
	if strings.Contains(out, `name="password"`) {
		t.Fatal("expected no password field when CapturePassword is false")
	}

	p2 := landingFixture()
	p2.CaptureUsername = false
	p2.CapturePassword = false
	out2, err := Landing(p2)
	if err != nil {
		t.Fatalf("Landing: %v", err)
	}
	if strings.Contains(out2, "<input") {
		t.Fatal("expected no input fields when neither capture flag is set")
	}
}

func TestLanding_UnknownAssetKeyRejected(t *testing.T) {
	p := landingFixture()
	p.LogoAssetKey = "not-a-real-asset"
	if _, err := Landing(p); err == nil {
		t.Fatal("expected an error for an unresolvable local asset key, got nil")
	}
}

func TestLandingFromClone_SubstitutesMarker(t *testing.T) {
	cloned := `<html><body><header>Real Co</header><div id="__baitbet_form__" style="border:2px dashed #6366f1;">placeholder</div><footer>footer</footer></body></html>`
	out, err := LandingFromClone(cloned, landingFixture())
	if err != nil {
		t.Fatalf("LandingFromClone: %v", err)
	}
	if strings.Contains(out, "__baitbet_form__") {
		t.Fatal("expected the marker to be fully replaced, not just its text")
	}
	if strings.Contains(out, "placeholder") {
		t.Fatal("expected the placeholder marker div to be removed entirely")
	}
	if !strings.Contains(out, `action="/t/abc123token/submit"`) {
		t.Fatal("expected the real form's submit action to appear in place of the marker")
	}
	if !strings.Contains(out, "<header>Real Co</header>") || !strings.Contains(out, "<footer>footer</footer>") {
		t.Fatal("expected the surrounding cloned markup to survive untouched")
	}
	headerIdx := strings.Index(out, "<header>")
	formIdx := strings.Index(out, "<form")
	footerIdx := strings.Index(out, "<footer>")
	if !(headerIdx < formIdx && formIdx < footerIdx) {
		t.Fatalf("expected form between header and footer, got order in: %s", out)
	}
}

func TestLandingFromClone_FallsBackToAppendWhenMarkerMissing(t *testing.T) {
	cloned := `<html><body><p>no marker here</p></body></html>`
	out, err := LandingFromClone(cloned, landingFixture())
	if err != nil {
		t.Fatalf("LandingFromClone: %v", err)
	}
	if !strings.Contains(out, `action="/t/abc123token/submit"`) {
		t.Fatal("expected the form to be appended even without a marker")
	}
	formIdx := strings.Index(out, "<form")
	bodyCloseIdx := strings.Index(out, "</body>")
	if formIdx < 0 || bodyCloseIdx < 0 || formIdx > bodyCloseIdx {
		t.Fatal("expected the appended form to land before </body>")
	}
}

func TestLandingFromClone_PreservedForm_SubstitutesSentinels(t *testing.T) {
	cloned := `<html><body><form action="__baitbet_submit__" method="post">` +
		`<input type="text" class="form-control" name="__baitbet_username__">` +
		`<input type="password" class="form-control" name="__baitbet_password__">` +
		`<input type="submit" class="btn btn-primary" value="Sign in"></form></body></html>`
	out, err := LandingFromClone(cloned, landingFixture())
	if err != nil {
		t.Fatalf("LandingFromClone: %v", err)
	}
	if strings.Contains(out, "__baitbet_submit__") || strings.Contains(out, "__baitbet_username__") || strings.Contains(out, "__baitbet_password__") {
		t.Fatalf("expected every sentinel to be replaced, got: %s", out)
	}
	if !strings.Contains(out, `action="/t/abc123token/submit"`) {
		t.Fatalf("expected the real submit action, got: %s", out)
	}
	if !strings.Contains(out, `name="username"`) || !strings.Contains(out, `name="password"`) {
		t.Fatalf("expected the real field names, got: %s", out)
	}
	if !strings.Contains(out, `class="form-control"`) || !strings.Contains(out, `class="btn btn-primary"`) {
		t.Fatalf("expected the cloned page's own classes to survive untouched, got: %s", out)
	}
}

func TestLandingFromClone_PreservedForm_RespectsCaptureFlags(t *testing.T) {
	cloned := `<html><body><form action="__baitbet_submit__" method="post">` +
		`<input type="text" name="__baitbet_username__">` +
		`<input type="password" name="__baitbet_password__">` +
		`</form></body></html>`
	p := landingFixture()
	p.CaptureUsername = false
	out, err := LandingFromClone(cloned, p)
	if err != nil {
		t.Fatalf("LandingFromClone: %v", err)
	}
	if strings.Contains(out, "__baitbet_username__") || strings.Contains(out, `name="username"`) {
		t.Fatalf("expected the username field to be dropped entirely, got: %s", out)
	}
	if !strings.Contains(out, `name="password"`) {
		t.Fatalf("expected the password field to survive, got: %s", out)
	}
}

func TestLandingFromClone_NeverContainsAPasswordValue(t *testing.T) {
	cloned := `<html><body><div id="__baitbet_form__"></div></body></html>`
	out, err := LandingFromClone(cloned, landingFixture())
	if err != nil {
		t.Fatalf("LandingFromClone: %v", err)
	}
	if strings.Contains(out, `value=`) {
		t.Fatal("rendered form must never carry a pre-filled value attribute")
	}
}
