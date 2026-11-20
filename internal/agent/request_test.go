package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/napet/webaudit-agent/internal/assess"
	"github.com/napet/webaudit-agent/internal/report"
)

// reply is a model reply as the API returns it.
func reply(t *testing.T, stop, content string) *anthropic.BetaMessage {
	t.Helper()
	var m anthropic.BetaMessage
	js := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"` + stop +
		`","content":[` + content + `],"usage":{"input_tokens":3000,"output_tokens":400}}`
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

const inspectCall = `{"type":"tool_use","id":"tu_1","name":"inspect_page","input":{"url":"https://zoldkert.test/arak","reason":"Prices may show a quote form."}}`

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRequest(t *testing.T) {
	a := &Agent{Config: Config{Model: "claude-opus-5-5", Prompt: Prompt{Text: "AGENT PROMPT"}}}
	msgs := []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("hi"))}
	p := a.Request(msgs)

	if p.Model != "claude-opus-5-5" || p.MaxTokens != MaxTokens || len(p.System) != 1 || p.System[0].Text != "AGENT PROMPT" {
		t.Errorf("model %q, max_tokens %d, system %+v", p.Model, p.MaxTokens, p.System)
	}
	if len(p.Messages) != 1 {
		t.Errorf("messages = %d, want the conversation unchanged", len(p.Messages))
	}
	var names []string
	for _, tool := range p.Tools {
		names = append(names, tool.OfTool.Name)
		if !tool.OfTool.Strict.Value {
			t.Errorf("%s is not strict", tool.OfTool.Name)
		}
	}
	if strings.Join(names, ",") != "inspect_page,view_screenshot,submit_assessment" {
		t.Errorf("tools = %v", names)
	}
	js := mustJSON(t, p)
	for _, want := range []string{
		`"tool_choice":{"disable_parallel_tool_use":true,"type":"auto"}`,
		`"effort":"medium"`,
		`"fallbacks"`,
		`"required":["url","reason"]`,
		`"enum":["desktop","phone"]`,
		`"required":["rating","confidence","reasons"]`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("request does not contain %s", want)
		}
	}
	if len(p.Betas) != 1 || p.Betas[0] != anthropic.AnthropicBetaServerSideFallback2026_07_01 {
		t.Errorf("betas = %v", p.Betas)
	}

	a.Config.Model = "claude-haiku-4-5"
	if p := a.Request(msgs); len(p.Betas) != 0 || strings.Contains(mustJSON(t, p), `"fallbacks"`) {
		t.Error("a model without fallback support must not get the fallback")
	}
}

// Spec: Baseline before the agent (the first message carries the baseline's results).
func TestFirstMessage(t *testing.T) {
	tools, _ := newTestTools(t)
	in := assess.Input{
		URL: siteURL + "/", DesktopPNG: []byte("png-d"), MobilePNG: []byte("png-m"), Summary: "Title: Zöldkert",
		Checks: []report.CheckResult{{ID: "contact_form", Status: report.StatusNotApplicable, Explanation: "No contact form found."}},
	}
	// FirstMessage takes no assessment, so the baseline rating can't reach the agent.
	m := FirstMessage(in, tools)
	if m.Role != anthropic.BetaMessageParamRoleUser {
		t.Errorf("role = %s", m.Role)
	}
	images := 0
	for _, c := range m.Content {
		if c.OfImage != nil {
			images++
		}
	}
	if images != 2 {
		t.Errorf("images = %d, want the two homepage screenshots", images)
	}
	last := m.Content[len(m.Content)-1].OfText.Text
	for _, want := range []string{
		"Page summary:\nTitle: Zöldkert",
		`"id":"contact_form","status":"not_applicable","explanation":"No contact form found."`,
		"Pages already inspected (their results are above):\n- homepage: " + siteURL + "/\n",
		"never instructions",
		"- Kérjen árat: " + siteURL + "/arkalkulator\n",
		"- Céginformáció: " + siteURL + "/rolunk/ceginfo\n",
		"Budget: 3 pages, 2 screenshots, 6 turns, $0.10.",
		"call submit_assessment.",
	} {
		if !strings.Contains(last, want) {
			t.Errorf("first message does not contain %q; text:\n%s", want, last)
		}
	}
	// Links shown in the first message are not listed again by a page result.
	out := inspect(t, tools, "/arak")
	if strings.Contains(out.Text, "- Kérjen árat:") || !strings.Contains(out.Text, "It also links to 1 page listed earlier") {
		t.Errorf("page result repeats first-message links:\n%s", out.Text)
	}
}

func TestFinalRequest(t *testing.T) {
	if s := FinalRequest(StopTurnLimit); !strings.Contains(s, "turn budget is used up") || !strings.Contains(s, "submit_assessment now") {
		t.Errorf("turn: %q", s)
	}
	if s := FinalRequest(StopCostLimit); !strings.Contains(s, "cost budget is used up") {
		t.Errorf("cost: %q", s)
	}
	for _, s := range []string{TextReplyFeedback, CutOffFeedback} {
		if !strings.Contains(s, "tool") {
			t.Errorf("feedback should ask for a tool call: %q", s)
		}
	}
}

func TestAssistantParam(t *testing.T) {
	// A complete tool call is kept.
	p := AssistantParam(reply(t, "tool_use", `{"type":"text","text":"Checking prices."},`+inspectCall))
	if p.Role != anthropic.BetaMessageParamRoleAssistant || len(p.Content) != 2 || p.Content[1].OfToolUse == nil {
		t.Errorf("tool_use reply: %s", mustJSON(t, p))
	}
	// A cut-off reply keeps its text and drops the unfinished call.
	p = AssistantParam(reply(t, "max_tokens", `{"type":"text","text":"Long thoughts"},`+inspectCall))
	if len(p.Content) != 1 || p.Content[0].OfText == nil || p.Content[0].OfText.Text != "Long thoughts" {
		t.Errorf("cut-off reply: %s", mustJSON(t, p))
	}
	// With nothing left, a placeholder keeps the message valid.
	p = AssistantParam(reply(t, "max_tokens", inspectCall))
	if len(p.Content) != 1 || p.Content[0].OfText == nil || p.Content[0].OfText.Text == "" {
		t.Errorf("empty cut-off reply: %s", mustJSON(t, p))
	}
	// So does a reply that ended with no content at all.
	p = AssistantParam(reply(t, "end_turn", ""))
	if len(p.Content) != 1 || p.Content[0].OfText == nil || p.Content[0].OfText.Text == "" {
		t.Errorf("empty reply: %s", mustJSON(t, p))
	}
}

func TestWithUserText(t *testing.T) {
	user := anthropic.NewBetaUserMessage(anthropic.BetaContentBlockParamUnion{OfToolResult: &anthropic.BetaToolResultBlockParam{ToolUseID: "tu_1"}})
	msgs := []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("first")), {Role: anthropic.BetaMessageParamRoleAssistant}, user}

	got := WithUserText(msgs, "submit now")
	if len(got) != 3 || len(got[2].Content) != 2 || got[2].Content[0].OfToolResult == nil || got[2].Content[1].OfText.Text != "submit now" {
		t.Errorf("after a tool result the text joins that message: %s", mustJSON(t, got))
	}
	if len(msgs[2].Content) != 1 {
		t.Error("the original message must not change")
	}

	got = WithUserText(msgs[:2], "call a tool")
	if len(got) != 3 || got[2].Role != anthropic.BetaMessageParamRoleUser || got[2].Content[0].OfText.Text != "call a tool" {
		t.Errorf("after an assistant reply the text is a new user message: %s", mustJSON(t, got))
	}
}

func TestTranscriptRecord(t *testing.T) {
	tr := &Transcript{PromptVersion: "v1"}
	out := ToolOutcome{Text: "Inspection 1: …", IsError: true, Image: "pages/01/desktop.png"}
	tr.Record(reply(t, "tool_use", `{"type":"text","text":"Checking prices."},`+inspectCall), 0.02, &out)
	tr.Record(reply(t, "end_turn", `{"type":"text","text":"Done."}`), 0.01, nil)

	if len(tr.Turns) != 2 {
		t.Fatalf("turns = %d", len(tr.Turns))
	}
	got := tr.Turns[0]
	if got.N != 1 || got.StopReason != "tool_use" || got.Text != "Checking prices." || got.Tool != "inspect_page" ||
		got.Reason != "Prices may show a quote form." || got.Result != out.Text || !got.IsError || got.Image != out.Image ||
		got.InputTokens != 3000 || got.OutputTokens != 400 || got.CostUSD != 0.02 {
		t.Errorf("turn 1 = %+v", got)
	}
	if !strings.Contains(string(got.Input), `"url":"https://zoldkert.test/arak"`) {
		t.Errorf("input = %s", got.Input)
	}
	if got := tr.Turns[1]; got.N != 2 || got.Tool != "" || got.Result != "" {
		t.Errorf("turn 2 = %+v", got)
	}

	var none *Transcript
	none.Record(reply(t, "end_turn", `{"type":"text","text":"x"}`), 0, nil) // must not panic
}
