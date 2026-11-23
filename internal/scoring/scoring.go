// Package scoring computes per-recipient phishing risk scores and detects
// automated scanner traffic (security gateways, link-followers). Pure
// functions, no DB - generic across any host application.
package scoring

import "strings"

// Event is the minimal shape scoring needs from a recorded event.
type Event struct {
	EventType      string
	IsScannerEvent bool
}

// actionPoints maps the highest-severity action reached to base points.
var actionPoints = map[string]float64{
	"mail_accepted":       0,
	"email_opened":        5,
	"link_clicked":        25,
	"debrief_viewed":      35,
	"form_started":        50,
	"username_entered":    60,
	"password_field_used": 75,
	"form_submitted":      90,
}

// scannerAgents are matched case-insensitively against the User-Agent.
var scannerAgents = []string{
	"mimecast", "proofpoint", "barracuda", "safelinks", "atp", "microsoft",
	"google", "googlebot", "bot", "crawler", "spider", "scanner", "urldefense",
	"forcepoint", "symantec", "cisco", "ironport", "fireeye", "trendmicro",
	"okta", "headless", "curl", "wget", "python-requests", "go-http-client",
}

type riskBand struct {
	threshold float64
	label     string
}

var riskBands = []riskBand{
	{80, "critical"},
	{50, "high"},
	{25, "medium"},
	{0, "low"},
}

// BandForScore maps a 0-100 score to its risk label.
func BandForScore(score float64) string {
	for _, b := range riskBands {
		if score >= b.threshold {
			return b.label
		}
	}
	return "low"
}

// IsScanner heuristically flags a click as automated rather than human.
func IsScanner(userAgent string, secsSinceDelivery float64, hasSecsSinceDelivery bool, hasInteraction bool) bool {
	ua := strings.ToLower(userAgent)
	for _, tok := range scannerAgents {
		if strings.Contains(ua, tok) {
			return true
		}
	}
	if ua == "" {
		return true
	}
	if hasSecsSinceDelivery && secsSinceDelivery <= 2 && !hasInteraction {
		return true
	}
	return false
}

// ComputeRecipientRisk returns (score, band) from non-scanner events,
// repeat submissions, a self-report, and training completion.
func ComputeRecipientRisk(events []Event, submissionCount int, reported bool, trainingCompleted bool) (float64, string) {
	var score float64
	for _, e := range events {
		if e.IsScannerEvent {
			continue
		}
		if p, ok := actionPoints[e.EventType]; ok && p > score {
			score = p
		}
	}

	if submissionCount > 1 {
		score += 10 * float64(submissionCount-1)
	}
	if reported {
		score -= 30
	}
	if trainingCompleted {
		score -= 20
	}

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return roundTo1(score), BandForScore(score)
}

func roundTo1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
