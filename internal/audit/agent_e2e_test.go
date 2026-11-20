package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/napet/webaudit-agent/internal/agent"
	"github.com/napet/webaudit-agent/internal/assess"
	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
	"github.com/napet/webaudit-agent/internal/testsite"
)

// scriptedModel returns canned replies in order and keeps every request as
// JSON. It fails the test when called more often than scripted.
type scriptedModel struct {
	t       *testing.T
	replies []string
	sent    []string
}

func (m *scriptedModel) New(_ context.Context, body anthropic.BetaMessageNewParams, _ ...option.RequestOption) (*anthropic.BetaMessage, error) {
	b, _ := json.Marshal(body)
	m.sent = append(m.sent, string(b))
	if len(m.sent) > len(m.replies) {
		m.t.Fatalf("model call %d, but only %d replies are scripted", len(m.sent), len(m.replies))
	}
	var msg anthropic.BetaMessage
	if err := json.Unmarshal([]byte(m.replies[len(m.sent)-1]), &msg); err != nil {
		m.t.Fatal(err)
	}
	return &msg, nil
}

func toolReply(id, name string, input any) string {
	in, _ := json.Marshal(input)
	return `{"id":"msg_` + id + `","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"tool_use",` +
		`"content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + string(in) + `}],` +
		`"usage":{"input_tokens":5000,"output_tokens":300}}`
}

// 7.4: the user's loop, the real tools and Chrome over the agentsite
// fixture, with a scripted model. The agent finds the quote form and the
// impresszum the baseline missed, and the report shows the changes.
// agentPipeline is a live agent-mode pipeline over a fixture site: Chrome,
// the user's loop and the real tools, with a scripted model that inspects
// the quote page and the company details, then submits.
func agentPipeline(t *testing.T, site *testsite.Server, root string) (*Pipeline, *scriptedModel) {
	t.Helper()
	p := tlsPipeline(t, site, root, &fakeAssessor{rating: 3})
	model := &scriptedModel{t: t, replies: []string{
		toolReply("tu_1", agent.ToolInspectPage, map[string]string{"url": site.URL + "/arkalkulator", "reason": "The quote link may lead to the enquiry form."}),
		toolReply("tu_2", agent.ToolInspectPage, map[string]string{"url": site.URL + "/rolunk/ceginfo", "reason": "Company details are often behind such a link."}),
		toolReply("tu_3", agent.ToolSubmit, map[string]any{"rating": 2, "confidence": "high",
			"reasons": []map[string]string{{"subject": "hero", "detail": "Clean, current layout."}, {"subject": "navigation", "detail": "Short, clear top menu."}}}),
	}}
	l, s := loadConfig(t)
	rubric, err := assess.LoadRubric("../../prompts/" + assess.RubricFile)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := agent.LoadPrompt("../../prompts/"+agent.PromptFile, rubric)
	if err != nil {
		t.Fatal(err)
	}
	chrome := p.Capturer.(*capture.Chrome)
	p.Agent = &agent.Runner{
		API:    model,
		Config: agent.Config{Model: "claude-opus-5-5", Price: config.ModelPrice{InputPerMTok: 5, OutputPerMTok: 25}, Prompt: prompt, Budgets: s.Agent},
		Open:   func(ctx context.Context) (capture.Session, error) { return chrome.Open(ctx) },
		HTTP:   p.HTTP,
		Locale: l,
	}
	return p, model
}

func TestAgentEndToEnd(t *testing.T) {
	needChrome(t)
	site := testsite.StartTLS("agentsite")
	defer site.Close()
	p, model := agentPipeline(t, site, t.TempDir())

	rep, err := p.Run(context.Background(), site.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if len(model.sent) != 3 {
		t.Fatalf("model calls = %d, want 3", len(model.sent))
	}
	// The tools ran for real: the second request carries the quote page's result.
	if !strings.Contains(model.sent[1], "contact form: no action address, handled by a script") {
		t.Error("the second request should carry the inspected quote page")
	}

	ag := rep.Agent
	if ag == nil || ag.StoppedBy != "submit" || ag.Turns != 3 || len(ag.Pages) != 2 {
		t.Fatalf("agent section = %+v", ag)
	}
	for _, pg := range ag.Pages {
		if pg.Status != 200 || pg.Error != "" || pg.Reason == "" {
			t.Errorf("page %+v", pg)
		}
	}
	if !hasChange(ag.Changed, "contact_form", report.StatusNotApplicable, report.StatusPass) ||
		!hasChange(ag.Changed, "legal_provider_details", report.StatusFail, report.StatusPass) {
		t.Errorf("changes = %+v", ag.Changed.Checks)
	}
	if len(ag.Changed.LegalGapsResolved) == 0 || ag.Changed.LegalGapsResolved[0] != "legal_provider_details" {
		t.Errorf("legal gaps resolved = %v", ag.Changed.LegalGapsResolved)
	}
	if rep.Assessment == nil || rep.Assessment.Rating != 2 || rep.Assessment.InputTokens != 15000 || !near(rep.Assessment.CostUSD, 0.0975) {
		t.Errorf("assessment = %+v", rep.Assessment)
	}
	if !near(rep.CostUSD, 0.0975+0.01) {
		t.Errorf("cost_usd = %v", rep.CostUSD)
	}
	if posts := site.Posts(); len(posts) != 0 {
		t.Errorf("POST requests: %v", posts)
	}
}
