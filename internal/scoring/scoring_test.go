package scoring

import "testing"

func TestBandForScore(t *testing.T) {
	cases := []struct {
		score float64
		want  string
	}{
		{0, "low"}, {24.9, "low"}, {25, "medium"}, {49.9, "medium"},
		{50, "high"}, {79.9, "high"}, {80, "critical"}, {100, "critical"},
	}
	for _, c := range cases {
		if got := BandForScore(c.score); got != c.want {
			t.Errorf("BandForScore(%v) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestIsScanner(t *testing.T) {
	if !IsScanner("Mozilla/5.0 Mimecast-SafeLinks", 10, true, true) {
		t.Error("known gateway UA should be flagged as scanner")
	}
	if !IsScanner("", 10, true, true) {
		t.Error("empty UA should be flagged as scanner")
	}
	if !IsScanner("Mozilla/5.0 (Windows)", 1, true, false) {
		t.Error("near-instant click with no interaction should be flagged as scanner")
	}
	if IsScanner("Mozilla/5.0 (Windows)", 30, true, true) {
		t.Error("ordinary browser UA with a real delay and interaction should not be flagged")
	}
	if IsScanner("Mozilla/5.0 (Windows)", 0, false, true) {
		t.Error("no delivery-time info plus real interaction should not be flagged")
	}
}

func TestComputeRecipientRisk(t *testing.T) {
	score, band := ComputeRecipientRisk(nil, 0, false, false)
	if score != 0 || band != "low" {
		t.Errorf("no events: got (%v, %q), want (0, low)", score, band)
	}

	events := []Event{{EventType: "form_submitted"}}
	score, band = ComputeRecipientRisk(events, 1, false, false)
	if score != 90 || band != "critical" {
		t.Errorf("single submission: got (%v, %q), want (90, critical)", score, band)
	}

	score, band = ComputeRecipientRisk(events, 1, false, true)
	if score != 70 || band != "high" {
		t.Errorf("training completed should reduce score: got (%v, %q), want (70, high)", score, band)
	}

	score, _ = ComputeRecipientRisk(events, 1, true, false)
	if score != 60 {
		t.Errorf("self-reported should reduce score: got %v, want 60", score)
	}

	score, _ = ComputeRecipientRisk(events, 3, false, false)
	if score != 100 {
		t.Errorf("repeat submissions should add and clamp at 100: got %v, want 100", score)
	}

	scannerEvents := []Event{{EventType: "form_submitted", IsScannerEvent: true}}
	score, band = ComputeRecipientRisk(scannerEvents, 1, false, false)
	if score != 0 || band != "low" {
		t.Errorf("scanner-only events should score 0: got (%v, %q)", score, band)
	}
}
