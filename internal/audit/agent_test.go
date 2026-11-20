package audit

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/napet/webaudit-agent/internal/agent"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
	"github.com/napet/webaudit-agent/internal/safehttp"
	"github.com/napet/webaudit-agent/internal/testsite"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/default_mode.golden.json")

func fixtureHTML(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testsite.Dir(), "agentsite", filepath.FromSlash(file)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// agentsiteCapture is what the baseline capture records for the agentsite fixture:
// the homepage only, since its rules find no contact or legal page.
func agentsiteCapture(t *testing.T, base string) *capture.Result {
	return &capture.Result{
		Home: capture.Page{RequestedURL: base + "/", FinalURL: base + "/", Status: 200, LoadMS: 800, Doctype: "html",
			HTML: fixtureHTML(t, "index.html")},
		Summary:    capture.Summarize(fixtureHTML(t, "index.html"), capture.SummaryLimit),
		DesktopPNG: []byte("d"), MobilePNG: []byte("m"),
	}
}

// offlinePipeline audits the agentsite fixture without Chrome: capture is
// canned, and the probes go only to the local fixture server.
func offlinePipeline(t *testing.T, site *testsite.Server, a *fakeAssessor) *Pipeline {
	l, s := loadConfig(t)
	pool := x509.NewCertPool()
	pool.AddCert(site.Certificate())
	tr := &http.Transport{TLSClientConfig: site.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
	return &Pipeline{
		Capturer:   &fakeCapturer{res: agentsiteCapture(t, site.URL)},
		PSI:        &fakePSI{raw: psiFixture(t)},
		Assessor:   a,
		HTTP:       safehttp.New(safehttp.Options{Transport: tr, Timeout: 5 * time.Second}),
		Locale:     l,
		Scoring:    s,
		ProbeRoots: pool,
		Now:        func() time.Time { return fixedNow },
	}
}

// A default-mode report is byte-for-byte what it was before agent mode
// existed. The golden file was recorded from the pipeline before the
// change; the fixture server's random port is replaced.
func TestDefaultModeReportUnchanged(t *testing.T) {
	site := testsite.StartTLS("agentsite")
	defer site.Close()
	rep, err := offlinePipeline(t, site, &fakeAssessor{rating: 3}).Run(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	rep.DurationMS = 0
	b, err := MarshalReport(rep)
	if err != nil {
		t.Fatal(err)
	}
	got := []byte(strings.ReplaceAll(string(b), strings.TrimPrefix(site.URL, "https://"), "agentsite.test"))
	golden := filepath.Join("testdata", "default_mode.golden.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update-golden to create)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("default-mode report changed:\n%s", got)
	}
	if rep.Agent != nil {
		t.Error("a default-mode report has no agent section")
	}
}

// fakeRunner stands in for the agent: it adds its pages to the capture as
// inspect_page would and returns a fixed outcome, so these tests don't
// depend on the loop.
type fakeRunner struct {
	pages   []capture.Page
	outcome agent.Outcome
	err     error
	calls   int
	in      agent.Input
}

func (f *fakeRunner) Run(_ context.Context, in agent.Input) (*agent.Result, error) {
	f.calls++
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	var visits []agent.Visit
	for _, p := range f.pages {
		in.Capture.Agent = append(in.Capture.Agent, p)
		in.Capture.AgentShots = append(in.Capture.AgentShots, capture.Shot{Desktop: []byte("desktop " + p.FinalURL), Mobile: []byte("mobile " + p.FinalURL)})
		visits = append(visits, agent.Visit{URL: p.RequestedURL, Reason: "fake reason", FinalURL: p.FinalURL, Status: p.Status, Agent: len(in.Capture.Agent) - 1})
	}
	// One transcript turn per outcome turn; the second showed a screenshot.
	tr := &agent.Transcript{PromptVersion: "v1"}
	for i := 1; i <= f.outcome.Turns; i++ {
		turn := agent.Turn{N: i, StopReason: "tool_use", Tool: agent.ToolInspectPage, Result: "fake result"}
		if i == 2 && len(f.pages) > 0 {
			turn.Tool, turn.Image = agent.ToolViewScreenshot, agent.ShotPath(0, "desktop")
		}
		tr.Turns = append(tr.Turns, turn)
	}
	return &agent.Result{Outcome: f.outcome, Visits: visits, Screenshots: 1, Transcript: tr,
		PromptVersion: "v1", Budgets: config.DefaultAgentBudgets}, nil
}

// The agentsite quote form, as the browser reports it: no action, sent by script.
var quoteForm = capture.Form{Index: 0, Method: "get", HasHandler: true, Textarea: true, Email: true}

func agentPage(t *testing.T, base, path, file string, forms ...capture.Form) capture.Page {
	return capture.Page{RequestedURL: base + path, FinalURL: base + path, Status: 200, LoadMS: 700, HTML: fixtureHTML(t, file), Forms: forms}
}

func submitted(rating int) agent.Outcome {
	return agent.Outcome{
		StoppedBy: agent.StopSubmit, Model: "claude-opus-5-5", Turns: 3, InputTokens: 9000, OutputTokens: 1200, CostUSD: 0.15,
		Assessment: &report.Assessment{Rating: rating, Confidence: "medium", RubricVersion: "v1",
			Reasons: []report.Reason{{Subject: "hero", Detail: "Plain but tidy."}}},
	}
}

func check(t *testing.T, rep *report.Report, id string) report.CheckResult {
	t.Helper()
	for _, c := range rep.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no %s check", id)
	return report.CheckResult{}
}

func hasChange(c report.AgentChanges, id string, from, to report.Status) bool {
	for _, ch := range c.Checks {
		if ch.ID == id && ch.From == from && ch.To == to {
			return true
		}
	}
	return false
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Spec: Baseline before the agent; Agent finds the contact form; Impresszum
// found by the agent.
func TestAgentModeFindsQuoteFormAndImpresszum(t *testing.T) {
	site := testsite.StartTLS("agentsite")
	defer site.Close()
	v1 := &fakeAssessor{rating: 3}
	p := offlinePipeline(t, site, v1)
	runner := &fakeRunner{
		pages:   []capture.Page{agentPage(t, site.URL, "/arkalkulator", "arkalkulator.html", quoteForm), agentPage(t, site.URL, "/rolunk/ceginfo", "rolunk/ceginfo.html")},
		outcome: submitted(4),
	}
	p.Agent = runner
	rep, err := p.Run(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}

	// The baseline audit, its assessment included, ran first and the agent started from it.
	if v1.calls.Load() != 1 || runner.calls != 1 {
		t.Errorf("v1 assessor calls %d, agent runs %d; want 1 and 1", v1.calls.Load(), runner.calls)
	}
	var baseContact report.Status
	for _, c := range runner.in.Assess.Checks {
		if c.ID == "contact_form" {
			baseContact = c.Status
		}
	}
	if baseContact != report.StatusNotApplicable || len(runner.in.Assess.DesktopPNG) == 0 {
		t.Errorf("agent input: contact_form %q, desktop %d bytes; want the v1 results", baseContact, len(runner.in.Assess.DesktopPNG))
	}

	// The final checks ran over the agent's pages.
	for _, id := range []string{"contact_form", "legal_provider_details", "legal_privacy_notice"} {
		if c := check(t, rep, id); c.Status != report.StatusPass {
			t.Errorf("%s = %s: %s", id, c.Status, c.Explanation)
		}
	}

	// The agent's assessment is the audit's, with its usage; the cost total includes the baseline's call.
	if a := rep.Assessment; a == nil || a.Rating != 4 || a.Model != "claude-opus-5-5" || a.InputTokens != 9000 || !near(a.CostUSD, 0.15) {
		t.Errorf("assessment = %+v", a)
	}
	if !near(rep.CostUSD, 0.16) {
		t.Errorf("cost_usd = %v, want the agent's 0.15 plus v1's 0.01", rep.CostUSD)
	}
	if rep.Components["agent"].Status != "ok" || rep.Components["assessment"].Status != "ok" {
		t.Errorf("components = %+v", rep.Components)
	}

	ag := rep.Agent
	if ag == nil {
		t.Fatal("no agent section")
	}
	if ag.StoppedBy != "submit" || ag.Turns != 3 || !near(ag.CostUSD, 0.15) || ag.PromptVersion != "v1" || ag.Budgets.MaxPages != 5 || ag.ScreenshotsViewed != 1 {
		t.Errorf("agent section = %+v", ag)
	}
	if len(ag.Pages) != 2 || ag.Pages[0].URL != site.URL+"/arkalkulator" || ag.Pages[0].Reason != "fake reason" || ag.Pages[0].Status != 200 {
		t.Errorf("pages = %+v", ag.Pages)
	}
	ch := ag.Changed
	for _, want := range []struct{ id, from, to string }{
		{"contact_form", "not_applicable", "pass"},
		{"legal_provider_details", "fail", "pass"},
		{"legal_privacy_notice", "not_applicable", "pass"},
	} {
		if !hasChange(ch, want.id, report.Status(want.from), report.Status(want.to)) {
			t.Errorf("changes lack %s %s → %s: %+v", want.id, want.from, want.to, ch.Checks)
		}
	}
	if len(ch.LegalGapsResolved) != 1 || ch.LegalGapsResolved[0] != "legal_provider_details" || len(ch.LegalGapsAdded) != 0 {
		t.Errorf("legal gaps resolved %v, added %v", ch.LegalGapsResolved, ch.LegalGapsAdded)
	}
	if ch.Rating.From == nil || *ch.Rating.From != 3 || ch.Rating.To == nil || *ch.Rating.To != 4 {
		t.Errorf("rating change = %v → %v", ch.Rating.From, ch.Rating.To)
	}
	if ch.Tier.From != 2 || ch.Tier.To != rep.Tier {
		t.Errorf("tier change = %d → %d, report tier %d", ch.Tier.From, ch.Tier.To, rep.Tier)
	}
	if posts := site.Posts(); len(posts) != 0 {
		t.Errorf("POST requests: %v", posts)
	}
}

// Spec: Assessment failed after pages were inspected; Turn limit without submit.
func TestAgentModeAssessmentFails(t *testing.T) {
	site := testsite.StartTLS("agentsite")
	defer site.Close()
	p := offlinePipeline(t, site, &fakeAssessor{rating: 3})
	broken := capture.Form{Index: 0, Method: "get", Textarea: true, Email: true} // nowhere to send, no script
	p.Agent = &fakeRunner{
		pages: []capture.Page{agentPage(t, site.URL, "/arkalkulator", "arkalkulator.html", broken)},
		outcome: agent.Outcome{StoppedBy: agent.StopTurnLimit, Err: errors.New("turn limit reached without a submitted assessment"),
			Model: "claude-opus-5-5", Turns: 13, InputTokens: 9000, OutputTokens: 1200, CostUSD: 0.15},
	}
	rep, err := p.Run(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Assessment != nil || !contains(rep.MissingSubScores, "datedness") {
		t.Errorf("assessment %+v, missing %v; want it failed and left out of the score", rep.Assessment, rep.MissingSubScores)
	}
	c := rep.Components["assessment"]
	if c.Status != "failed" || !strings.Contains(c.Error, "turn_limit") || c.Usage == nil || !near(c.Usage.CostUSD, 0.15) {
		t.Errorf("assessment component = %+v", c)
	}
	// The inspected page is still checked, and its broken form raises the flag.
	if !contains(rep.Flags, report.FlagBrokenContactForm) || rep.Tier != 1 {
		t.Errorf("flags %v, tier %d; the agent page's broken form must count", rep.Flags, rep.Tier)
	}
	if !near(rep.CostUSD, 0.16) {
		t.Errorf("cost_usd = %v, want every turn's cost plus v1's", rep.CostUSD)
	}
	ag := rep.Agent
	if ag == nil || ag.StoppedBy != "turn_limit" || ag.Turns != 13 || !strings.Contains(ag.Error, "turn limit") {
		t.Fatalf("agent section = %+v", ag)
	}
	if ag.Changed.Rating.From == nil || ag.Changed.Rating.To != nil || ag.Changed.Tier.From != 2 || ag.Changed.Tier.To != 1 {
		t.Errorf("changes = %+v", ag.Changed)
	}
}

func TestAgentModeAgentCannotStart(t *testing.T) {
	site := testsite.StartTLS("agentsite")
	defer site.Close()
	p := offlinePipeline(t, site, &fakeAssessor{rating: 3})
	p.Agent = &fakeRunner{err: errors.New("open browser: chrome not found")}
	rep, err := p.Run(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if c := rep.Components["agent"]; c.Status != "failed" || !strings.Contains(c.Error, "chrome not found") {
		t.Errorf("agent component = %+v", c)
	}
	if rep.Assessment == nil || rep.Assessment.Rating != 3 || !near(rep.CostUSD, 0.01) {
		t.Errorf("v1's assessment and cost should stay: %+v, cost %v", rep.Assessment, rep.CostUSD)
	}
	if rep.Agent == nil || rep.Agent.Error == "" || len(rep.Agent.Changed.Checks) != 0 {
		t.Errorf("agent section = %+v", rep.Agent)
	}
}

func TestAgentModeSkippedWhenHomeFails(t *testing.T) {
	site := testsite.StartTLS("agentsite")
	defer site.Close()
	p := offlinePipeline(t, site, &fakeAssessor{rating: 3})
	p.Capturer.(*fakeCapturer).res.Home.Status = 503
	runner := &fakeRunner{outcome: submitted(4)}
	p.Agent = runner
	rep, err := p.Run(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if runner.calls != 0 || rep.Components["agent"].Status != "skipped" {
		t.Errorf("runner calls %d, component %+v", runner.calls, rep.Components["agent"])
	}
}
