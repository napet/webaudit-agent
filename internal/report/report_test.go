package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

func sample() Report {
	one, zero := 1.0, 0.0
	return Report{
		SchemaVersion: SchemaVersion,
		URL:           "https://example.hu",
		FinalURL:      "https://www.example.hu/",
		Timestamp:     time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC),
		Locale:        "hu",
		Tier:          1,
		Score:         61.5,
		SubScores:     map[string]float64{CatPerformance: 40, CatDatedness: 75},
		Weights:       map[string]float64{CatPerformance: 1, CatDatedness: 1},
		Flags:         []string{FlagBrokenContactForm},
		Checks: []CheckResult{{
			ID: "contact_form", Category: CatContact, Status: StatusFail, Flag: FlagBrokenContactForm,
			Explanation: "The contact form submits to a page that does not exist.",
			Evidence:    map[string]any{"action": "https://www.example.hu/contact.php", "status": 404},
		}},
		LegalGaps: []CheckResult{{
			ID: "legal_provider_details", Category: CatLegal, Status: StatusFail,
			Explanation: "Could not find provider details (impresszum).",
			Evidence:    map[string]any{"pages_searched": []string{"https://www.example.hu/"}},
		}},
		PageSpeed: &PageSpeed{
			Strategy: "mobile",
			Scores:   map[string]float64{"performance": 0.6},
			Audits:   map[string]*float64{"viewport": &one, "font-size": &zero},
		},
		Accessibility: &Accessibility{
			AxeVersion: "4.x", ByImpact: map[string]int{"serious": 3},
			Violations: []AxeViolation{{Rule: "color-contrast", Impact: "serious", Nodes: 3, Examples: []string{"footer p"}}},
		},
		Assessment: &Assessment{
			Rating: 4, Confidence: "medium",
			Reasons:       []Reason{{Subject: "check: table_layout", Detail: "Layout built from tables."}},
			RubricVersion: "v1", Model: "claude-opus-5-5", InputTokens: 4000, OutputTokens: 600, CostUSD: 0.028,
		},
		Components: map[string]Component{"pagespeed": {Status: "ok"}, "assessment": {Status: "ok"}},
		CostUSD:    0.028,
		DurationMS: 31000,
	}
}

// sampleAgent is sample in agent mode: the same report plus the agent section.
func sampleAgent() Report {
	r := sample()
	from, to := 3, 4
	r.Agent = &Agent{
		PromptVersion: "v1", Model: "claude-opus-5-5", StoppedBy: "submit",
		Pages: []AgentPage{
			{URL: "https://www.example.hu/arkalkulator", Reason: "The menu's quote link may lead to the enquiry form.", FinalURL: "https://www.example.hu/arkalkulator", Status: 200},
			{URL: "https://www.example.hu/referenciak", Reason: "References may show inner pages.", Status: 404, Error: "answered with HTTP 404"},
		},
		ScreenshotsViewed: 1,
		Budgets:           AgentBudgets{MaxPages: 5, MaxScreenshots: 3, MaxTurns: 12, MaxCostUSD: 0.5},
		Turns:             4, InputTokens: 24000, OutputTokens: 1800, CostUSD: 0.132,
		Changed: AgentChanges{
			Checks:            []CheckChange{{ID: "contact_form", From: StatusNotApplicable, To: StatusFail}},
			LegalGapsResolved: []string{"legal_provider_details"},
			LegalGapsAdded:    []string{},
		},
	}
	r.Agent.Changed.Rating.From, r.Agent.Changed.Rating.To = &from, &to
	r.Agent.Changed.Tier.From, r.Agent.Changed.Tier.To = 2, 1
	r.Components["agent"] = Component{Status: "ok"}
	r.CostUSD = 0.16
	return r
}

func TestReportGolden(t *testing.T) {
	for name, r := range map[string]Report{"report.golden.json": sample(), "report_agent.golden.json": sampleAgent()} {
		t.Run(name, func(t *testing.T) { checkGolden(t, r, name) })
	}
}

func checkGolden(t *testing.T, r Report, name string) {
	got, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
		t.Errorf("report JSON shape changed; if intended, bump SchemaVersion when breaking and run with -update\n got: %s", got)
	}
}
