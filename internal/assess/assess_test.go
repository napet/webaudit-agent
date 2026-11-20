package assess

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
)

// fake returns canned replies in order and records the requests.
type fake struct {
	replies []string
	calls   []anthropic.BetaMessageNewParams
}

func (f *fake) New(_ context.Context, p anthropic.BetaMessageNewParams, _ ...option.RequestOption) (*anthropic.BetaMessage, error) {
	f.calls = append(f.calls, p)
	if len(f.replies) == 0 {
		return nil, errors.New("fake: no reply left")
	}
	var m anthropic.BetaMessage
	if err := json.Unmarshal([]byte(f.replies[0]), &m); err != nil {
		return nil, err
	}
	f.replies = f.replies[1:]
	return &m, nil
}

func reply(stop string, content string) string {
	return `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"` + stop +
		`","content":[` + content + `],"usage":{"input_tokens":3000,"output_tokens":400}}`
}

const goodTool = `{"type":"tool_use","id":"tu_1","name":"submit_assessment","input":{"rating":4,"confidence":"high","reasons":[{"subject":"check: table_layout","detail":"Fixed-width table layout."},{"subject":"mobile layout","detail":"Visitors must zoom to read."}]}}`

func claude(f *fake) *Claude {
	return &Claude{API: f, Model: "claude-opus-5-5", Price: config.ModelPrice{InputPerMTok: 4, OutputPerMTok: 20},
		Rubric: Rubric{Version: "v1", Text: "rubric text"}}
}

var input = Input{
	URL: "https://szabo.hu/", DesktopPNG: []byte("png-d"), MobilePNG: []byte("png-m"), Summary: "Title: Szabó",
	Checks: []report.CheckResult{
		{ID: "table_layout", Status: report.StatusFail, Explanation: "Tables."},
		{ID: "https", Status: report.StatusPass, Explanation: "fine"},
	},
}

func TestValidAnswer(t *testing.T) {
	f := &fake{replies: []string{reply("tool_use", goodTool)}}
	a, err := claude(f).Assess(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if a.Rating != 4 || a.Confidence != "high" || len(a.Reasons) != 2 || a.Reasons[0].Subject != "check: table_layout" {
		t.Errorf("assessment = %+v", a)
	}
	if a.RubricVersion != "v1" || a.Model != "claude-opus-5-5" || a.InputTokens != 3000 || a.OutputTokens != 400 {
		t.Errorf("metadata = %+v", a)
	}
	if want := 3000*4/1e6 + 400*20/1e6; a.CostUSD != want {
		t.Errorf("cost = %v, want %v", a.CostUSD, want)
	}
	if len(f.calls) != 1 {
		t.Errorf("calls = %d, want exactly 1", len(f.calls))
	}
}

func TestRequestShape(t *testing.T) {
	p := claude(&fake{}).Request(input)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}

	tools := m["tools"].([]any)
	tool := tools[0].(map[string]any)
	if len(tools) != 1 || tool["name"] != ToolName || tool["strict"] != true {
		t.Errorf("tools = %v", tools)
	}
	if schema := tool["input_schema"].(map[string]any); schema["additionalProperties"] != false {
		t.Errorf("schema must disallow extra properties: %v", schema)
	}
	if tc := m["tool_choice"].(map[string]any); tc["type"] != "auto" || tc["disable_parallel_tool_use"] != true {
		t.Errorf("tool_choice = %v (forced choice is rejected by current models)", tc)
	}
	if oc := m["output_config"].(map[string]any); oc["effort"] != "medium" {
		t.Errorf("output_config = %v", oc)
	}
	if m["fallbacks"] != "default" || len(p.Betas) != 1 {
		t.Errorf("fallbacks = %v betas = %v", m["fallbacks"], p.Betas)
	}
	if _, ok := m["temperature"]; ok {
		t.Error("temperature is rejected by the API and must not be sent")
	}
	content := m["messages"].([]any)[0].(map[string]any)["content"].([]any)
	images := 0
	var text strings.Builder
	for _, c := range content {
		block := c.(map[string]any)
		if block["type"] == "image" {
			images++
		}
		if s, ok := block["text"].(string); ok {
			text.WriteString(s)
		}
	}
	if images != 2 {
		t.Errorf("images = %d, want 2", images)
	}
	if !strings.Contains(text.String(), `"explanation":"Tables."`) || strings.Contains(text.String(), `"fine"`) {
		t.Errorf("check results should carry explanations for non-passing checks only: %s", text.String())
	}

	h := &Claude{Model: "claude-haiku-5-5", Rubric: Rubric{Version: "v1"}}
	if hp := h.Request(input); len(hp.Betas) != 0 {
		t.Error("Haiku has no server-side fallback; do not send the beta")
	}
}

func TestInvalidThenValid(t *testing.T) {
	bad := `{"type":"tool_use","id":"tu_1","name":"submit_assessment","input":{"rating":9,"confidence":"high","reasons":[{"subject":"x","detail":"y"}]}}`
	f := &fake{replies: []string{reply("tool_use", bad), reply("tool_use", goodTool)}}
	a, err := claude(f).Assess(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if a.Rating != 4 || len(f.calls) != 2 || a.InputTokens != 6000 {
		t.Errorf("a=%+v calls=%d", a, len(f.calls))
	}
}

func TestNoToolCallIsRetried(t *testing.T) {
	text := `{"type":"text","text":"This site looks rather old."}`
	f := &fake{replies: []string{reply("end_turn", text), reply("tool_use", goodTool)}}
	if _, err := claude(f).Assess(context.Background(), input); err != nil || len(f.calls) != 2 {
		t.Errorf("err=%v calls=%d", err, len(f.calls))
	}
}

func TestInvalidTwiceFails(t *testing.T) {
	empty := `{"type":"tool_use","id":"tu_1","name":"submit_assessment","input":{"rating":3,"confidence":"high","reasons":[]}}`
	f := &fake{replies: []string{reply("tool_use", empty), reply("max_tokens", `{"type":"text","text":"..."}`)}}
	_, err := claude(f).Assess(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "after retry") || len(f.calls) != 2 {
		t.Errorf("err=%v calls=%d", err, len(f.calls))
	}
	// Both failed calls were billed, so their usage travels with the error.
	var ce *CallError
	if !errors.As(err, &ce) || ce.Usage.InputTokens != 6000 || ce.Usage.OutputTokens != 800 || ce.Usage.CostUSD != 0.04 {
		t.Errorf("usage on failure = %+v", ce)
	}
}

func TestAPIErrorBeforeAnyReplyHasNoUsage(t *testing.T) {
	_, err := claude(&fake{}).Assess(context.Background(), input)
	var ce *CallError
	if err == nil || errors.As(err, &ce) {
		t.Errorf("err=%v, want a plain error with no usage", err)
	}
}

func TestRefusalIsNotRetried(t *testing.T) {
	r := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"x"},"content":[],"usage":{"input_tokens":10,"output_tokens":1}}`
	f := &fake{replies: []string{r}}
	_, err := claude(f).Assess(context.Background(), input)
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "cyber") || len(f.calls) != 1 {
		t.Errorf("err=%v calls=%d", err, len(f.calls))
	}
	var ce *CallError
	if !errors.As(err, &ce) || ce.Usage.InputTokens != 10 {
		t.Errorf("refusal usage = %+v", ce)
	}
}

func TestLoadRubric(t *testing.T) {
	r, err := LoadRubric("../../prompts/" + RubricFile)
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "v1" {
		t.Errorf("version = %q", r.Version)
	}
	for _, level := range []string{"**1 ", "**2 ", "**3 ", "**4 ", "**5 "} {
		if !strings.Contains(r.Text, level) {
			t.Errorf("rubric missing level %q", level)
		}
	}
}

// 5.4: opt-in live call. Runs only when ANTHROPIC_API_KEY is set.
func TestLiveAssessment(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set; skipping live API test")
	}
	desktop, mobile := liveScreenshots(t)
	rubric, err := LoadRubric("../../prompts/" + RubricFile)
	if err != nil {
		t.Fatal(err)
	}
	client := anthropic.NewClient()
	c := &Claude{API: &client.Beta.Messages, Model: "claude-opus-5-5", Rubric: rubric,
		Price: config.ModelPrice{InputPerMTok: 4, OutputPerMTok: 20}}
	a, err := c.Assess(context.Background(), Input{URL: "fixture://old2012", DesktopPNG: desktop, MobilePNG: mobile,
		Summary: "Title: Szabó Vízszerelés\nLayout tables: 3",
		Checks:  []report.CheckResult{{ID: "table_layout", Status: report.StatusFail, Explanation: "Layout built from tables."}}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Rating < 1 || a.Rating > 5 || len(a.Reasons) == 0 {
		t.Errorf("assessment = %+v", a)
	}
	t.Logf("rating %d (%s), $%.4f, reasons: %+v", a.Rating, a.Confidence, a.CostUSD, a.Reasons)
}

// Spec: Placeholder reason rejected. The recorded case is from a live agent run.
func TestDecodeRejectsPlaceholders(t *testing.T) {
	good := `{"subject":"hero image","detail":"Real photography, current look."}`
	cases := []struct {
		name, reasons, want string
	}{
		{"recorded placeholder", `{"subject":"x","detail":"placeholder"}`, `reason(s) given, at least 2`},
		{"one good reason", good, "1 reason(s) given, at least 2 needed"},
		{"short subject", good + `,{"subject":"x","detail":"Plain blue link lists."}`, `reason 2 (subject "x"`},
		{"placeholder subject", `{"subject":"TODO","detail":"Plain blue link lists."},` + good, `reason 1 (subject "TODO"`},
		{"one-word detail", good + `,{"subject":"navigation","detail":"placeholder"}`, `reason 2 (subject "navigation", detail "placeholder")`},
		{"two-word detail", good + `,{"subject":"navigation","detail":"Looks old."}`, "reason 2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Decode(json.RawMessage(`{"rating":2,"confidence":"medium","reasons":[` + c.reasons + `]}`))
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), `"check: <id>"`) {
				t.Errorf("err = %v, want one containing %q and saying what a reason needs", err, c.want)
			}
		})
	}
	a, err := Decode(json.RawMessage(`{"rating":2,"confidence":"medium","reasons":[` + good + `,{"subject":"check: html5_doctype","detail":"No doctype, quirks mode."}]}`))
	if err != nil || len(a.Reasons) != 2 {
		t.Errorf("a normal answer: a = %+v, err = %v", a, err)
	}
}
