package store

import "context"

// seedTemplate/seedLandingPage are the builtin library rows every org sees by default.
type seedTemplate struct {
	id, name, scenario, subject, senderName, senderEmail, bodyHTML, ctaText string
}

type seedLandingPage struct {
	id, name, pageType, html, brandColor string
	captureUsername, capturePassword     bool
}

// SeedBuiltinLibrary upserts by id, so a content fix here takes effect on next startup.
func (s *Store) SeedBuiltinLibrary(ctx context.Context) error {
	for _, t := range builtinTemplates {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO templates (id, organization_id, name, scenario, subject, sender_name, sender_email, body_html, cta_text, is_builtin)
			VALUES ($1, NULL, $2, $3, $4, $5, $6, $7, $8, TRUE)
			ON CONFLICT (id) DO UPDATE SET name=$2, scenario=$3, subject=$4, sender_name=$5, sender_email=$6, body_html=$7, cta_text=$8`,
			t.id, t.name, t.scenario, t.subject, t.senderName, t.senderEmail, t.bodyHTML, t.ctaText); err != nil {
			return err
		}
	}
	for _, p := range builtinLandingPages {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO landing_pages (id, organization_id, name, page_type, html, capture_username, capture_password, brand_color, is_builtin)
			VALUES ($1, NULL, $2, $3, $4, $5, $6, $7, TRUE)
			ON CONFLICT (id) DO UPDATE SET name=$2, page_type=$3, html=$4, capture_username=$5, capture_password=$6, brand_color=$7`,
			p.id, p.name, p.pageType, p.html, p.captureUsername, p.capturePassword, p.brandColor); err != nil {
			return err
		}
	}
	return nil
}

func doc(title, body, bg string) string {
	if bg == "" {
		bg = "#f3f2f1"
	}
	return `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1"><title>` + title + `</title>` +
		`<style>*{box-sizing:border-box}body{margin:0;background:` + bg + `;` +
		`font-family:"Segoe UI",-apple-system,Roboto,Helvetica,Arial,sans-serif;}` +
		`.in{width:100%;padding:10px 8px;border:1px solid #666;border-radius:2px;font-size:15px;}` +
		`.in:focus{outline:2px solid #0067b8;border-color:#0067b8;}</style></head>` +
		`<body>` + body + `</body></html>`
}

const (
	msLogo = `<svg width="108" height="24" viewBox="0 0 108 24" xmlns="http://www.w3.org/2000/svg">` +
		`<rect width="10" height="10" x="0" y="0" fill="#F25022"/><rect width="10" height="10" x="12" y="0" fill="#7FBA00"/>` +
		`<rect width="10" height="10" x="0" y="12" fill="#00A4EF"/><rect width="10" height="10" x="12" y="12" fill="#FFB900"/>` +
		`<text x="30" y="17" font-family="Segoe UI,Arial,sans-serif" font-size="15" fill="#5E5E5E">Microsoft</text></svg>`
	googleG = `<svg width="40" height="40" viewBox="0 0 48 48" xmlns="http://www.w3.org/2000/svg">` +
		`<path fill="#EA4335" d="M24 9.5c3.5 0 6.6 1.2 9 3.6l6.7-6.7C35.6 2.4 30.2 0 24 0 14.6 0 6.5 5.4 2.6 13.2l7.8 6.1C12.3 13.2 17.6 9.5 24 9.5z"/>` +
		`<path fill="#4285F4" d="M46.1 24.6c0-1.6-.1-3.1-.4-4.6H24v9.1h12.4c-.5 2.9-2.1 5.3-4.6 7l7.1 5.5c4.1-3.8 6.5-9.4 6.5-17z"/>` +
		`<path fill="#FBBC05" d="M10.4 28.7c-.5-1.4-.8-2.9-.8-4.7s.3-3.3.8-4.7l-7.8-6.1C1 16.6 0 20.2 0 24s1 7.4 2.6 10.8l7.8-6.1z"/>` +
		`<path fill="#34A853" d="M24 48c6.5 0 11.9-2.1 15.9-5.8l-7.1-5.5c-2 1.3-4.5 2.1-8.8 2.1-6.4 0-11.7-3.7-13.6-9.8l-7.8 6.1C6.5 42.6 14.6 48 24 48z"/></svg>`
)

func loginForm(logoHTML, heading, subheading, userType, userPlaceholder, submitLabel, submitBg, submitColor, containerExtra string) string {
	sub := ""
	if subheading != "" {
		sub = `<p style="text-align:center;font-size:16px;color:#202124;margin:0 0 28px;">` + subheading + `</p>`
	}
	return `<div style="min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px;` + containerExtra + `">` +
		`<div style="background:#fff;width:420px;max-width:100%;padding:44px;box-shadow:0 2px 6px rgba(0,0,0,.13);">` +
		`<div style="margin-bottom:16px;">` + logoHTML + `</div>` +
		`<h1 style="font-size:22px;font-weight:600;color:#1b1b1b;margin:0 0 4px;">` + heading + `</h1>` + sub +
		`<form method="post" action="">` +
		`<input class="in" name="username" type="` + userType + `" placeholder="` + userPlaceholder + `" autocomplete="off" style="margin-bottom:16px;">` +
		`<input class="in" name="password" type="password" placeholder="Password" autocomplete="off" style="margin-bottom:20px;">` +
		`<button type="submit" style="width:100%;background:` + submitBg + `;color:` + submitColor + `;border:none;padding:11px;font-size:15px;font-weight:600;cursor:pointer;border-radius:2px;">` + submitLabel + `</button>` +
		`</form></div></div>`
}

var builtinLandingPages = []seedLandingPage{
	{
		id: "lp_builtin_ms_login", name: "Microsoft sign-in", pageType: "login", brandColor: "#0067b8",
		captureUsername: true, capturePassword: true,
		html: doc("Sign in to your account", loginForm(msLogo, "Sign in", "", "email", "Email, phone, or Skype", "Sign in", "#0067b8", "#fff", ""), ""),
	},
	{
		id: "lp_builtin_google_login", name: "Google sign-in", pageType: "login", brandColor: "#1a73e8",
		captureUsername: true, capturePassword: true,
		html: doc("Sign in - Google Accounts", loginForm(googleG, "Sign in", "to continue", "email", "Email or phone", "Next", "#1a73e8", "#fff", ""), "#fff"),
	},
	{
		id: "lp_builtin_o365_login", name: "Outlook / Office 365 sign-in", pageType: "login", brandColor: "#0067b8",
		captureUsername: true, capturePassword: true,
		html: doc("Sign in to Outlook", loginForm(msLogo, "Outlook", "Connect. Organize. Get things done.", "email", "Email address", "Sign in", "#0067b8", "#fff",
			"background:linear-gradient(135deg,#0a2342,#0067b8);"), ""),
	},
	{
		id: "lp_builtin_docusign_login", name: "DocuSign log in", pageType: "login", brandColor: "#d8472b",
		captureUsername: true, capturePassword: true,
		html: doc("Log in to DocuSign", loginForm(`<div style="font-size:24px;font-weight:800;color:#d8472b;">DocuSign</div>`,
			"Log in", "Review and sign your document", "email", "Email", "Log in", "#ffd24d", "#1a1a1a", "background:#f7f7f7;"), ""),
	},
	{
		id: "lp_builtin_okta_login", name: "Okta SSO sign-in", pageType: "login", brandColor: "#00297a",
		captureUsername: true, capturePassword: true,
		html: doc("Sign In", loginForm(`<div style="font-size:22px;font-weight:800;color:#00297a;">okta</div>`,
			"Sign In", "", "text", "Username", "Sign In", "#00297a", "#fff", "background:#f9f9f9;"), ""),
	},
}

// Bodies must stay plain text: html/template auto-escapes BodyHTML, so literal tags here would show as escaped text.
var builtinTemplates = []seedTemplate{
	{
		id: "tpl_builtin_shared_document", name: "Shared document for review", scenario: "shared_document",
		subject: "Draft for the Q3 team update", senderName: "Priya Sharma", ctaText: "Open document",
		bodyHTML: "Hi, I've put together the draft for the Q3 team update and added a few notes where I'd " +
			"value your input. Could you take a look when you get a chance? No rush — sometime this week is fine. " +
			"Thanks so much, Priya",
	},
	{
		id: "tpl_builtin_doc_comment", name: "Comment on a shared file", scenario: "document",
		subject: "Alex commented on the file you're working on", senderName: "Alex Morgan", ctaText: "View comment",
		bodyHTML: "Hi, Alex left a comment on the file you're both working on and mentioned you so it reaches you: " +
			`"Left a couple of suggestions on the second section — happy to chat if easier." Thanks, Alex`,
	},
	{
		id: "tpl_builtin_expense_approval", name: "Expense report — approval", scenario: "invoice_payment",
		subject: "An expense report is waiting for your approval", senderName: "Finance Team", ctaText: "Review report",
		bodyHTML: "Hi, an expense report (Travel & meals — client visit, ₹8,450, submitted by R. Menon) has been " +
			"submitted and is waiting for your approval. Whenever you have a minute, please review and approve " +
			"so it can move to processing. Finance team",
	},
	{
		id: "tpl_builtin_portal_resignin", name: "Staff portal — sign in again", scenario: "account_verification",
		subject: "Please sign in again to the staff portal", senderName: "IT Helpdesk", ctaText: "Go to the portal",
		bodyHTML: "Hi, we recently updated the staff portal, and everyone will need to sign in once more the next " +
			"time they visit so the new settings apply to your account. It only takes a moment. Thanks, IT Helpdesk",
	},
	{
		id: "tpl_builtin_hr_details", name: "HR — confirm your details", scenario: "benefits_enrollment",
		subject: "Quick reminder: confirm your details", senderName: "People & Culture", ctaText: "Update my details",
		bodyHTML: "Hi, a quick reminder to confirm your contact and emergency details in the HR portal — we're " +
			"tidying up records this quarter and a few entries are missing. It should take under two minutes. " +
			"Thanks for your help, People & Culture",
	},
	{
		id: "tpl_builtin_meeting_notes", name: "Meeting notes & action items", scenario: "shared_document",
		subject: "Notes and action items from today", senderName: "Sam Patel", ctaText: "View notes",
		bodyHTML: "Hi, thanks for joining today. I've written up the notes and the action items — a couple are " +
			"assigned to you, so worth a quick look when convenient. Cheers, Sam",
	},
}
