package agent

// These tests are the specification of Agent.Run, written before it. Each
// one scripts the model's replies, runs the loop over the agentsite fixture
// (fake browser, real tools), and checks the outcome and every request the
// loop sent.

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/napet/webaudit-agent/internal/assess"
	"github.com/napet/webaudit-agent/internal/config"
)

// Each scripted turn costs 3000 input and 400 output tokens: $0.05 at this price.
var testPrice = config.ModelPrice{InputPerMTok: 10, OutputPerMTok: 50}

const turnCost = 0.05

func newAgent(t *testing.T, replies ...any) (*Agent, *fakeModel, *Tools) {
	t.Helper()
	tools, _ := newTestTools(t)
	m := &fakeModel{t: t, replies: replies}
	in := assess.Input{URL: siteURL + "/", DesktopPNG: []byte("png-d"), MobilePNG: []byte("png-m"), Summary: "Title: Zöldkert"}
	a := &Agent{
		API:   m,
		Tools: tools,
		First: FirstMessage(in, tools),
		Config: Config{
			Model: "claude-opus-5-5", Price: testPrice,
			Prompt:  Prompt{Version: "v1", RubricVersion: "v1", Text: "AGENT PROMPT"},
			Budgets: config.AgentBudgets{MaxPages: 3, MaxScreenshots: 2, MaxTurns: 6, MaxCostUSD: 1},
		},
		Transcript: &Transcript{PromptVersion: "v1"},
	}
	return a, m, tools
}

func run(t *testing.T, a *Agent) *Outcome {
	t.Helper()
	out, err := a.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out == nil {
		t.Fatal("Run returned no outcome")
	}
	return out
}

func call(id, name, input string) string {
	return `{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}`
}

const goodAssessment = `{"rating":3,"confidence":"medium","reasons":[{"subject":"hero","detail":"Plain but tidy layout."},{"subject":"navigation","detail":"Short, clear top menu."}]}`

func inspectReply(t *testing.T, id, path string) *anthropic.BetaMessage {
	return reply(t, "tool_use", call(id, ToolInspectPage, `{"url":"`+siteURL+path+`","reason":"r"}`))
}

func submitReply(t *testing.T, id string) *anthropic.BetaMessage {
	return reply(t, "tool_use", call(id, ToolSubmit, goodAssessment))
}

func textReply(t *testing.T, text string) *anthropic.BetaMessage {
	return reply(t, "end_turn", `{"type":"text","text":"`+text+`"}`)
}

func wantStop(t *testing.T, out *Outcome, want StopReason, turns int) {
	t.Helper()
	if out.StoppedBy != want || out.Turns != turns {
		t.Errorf("stopped by %q after %d turns, want %q after %d (err: %v)", out.StoppedBy, out.Turns, want, turns, out.Err)
	}
	if (want == StopSubmit) != (out.Assessment != nil) {
		t.Errorf("assessment = %+v; it is set exactly when the agent submitted", out.Assessment)
	}
	if want == StopSubmit && out.Err != nil {
		t.Errorf("err = %v, want nil after a submission", out.Err)
	}
	if want != StopSubmit && out.Err == nil {
		t.Error("a run that did not submit needs an error saying why")
	}
}

// lastMessage returns the last message of the n-th request (1-based).
func lastMessage(t *testing.T, m *fakeModel, n int) sentMessage {
	t.Helper()
	if len(m.sent) < n {
		t.Fatalf("only %d requests sent, want at least %d", len(m.sent), n)
	}
	msgs := m.sent[n-1].Messages
	return msgs[len(msgs)-1]
}

func countText(m *fakeModel, s string) int {
	n := 0
	for _, r := range m.sent {
		for _, msg := range r.Messages {
			for _, t := range msg.texts() {
				n += strings.Count(t, s)
			}
		}
	}
	return n
}

func TestRunSubmitFirstTurn(t *testing.T) {
	a, m, _ := newAgent(t, submitReply(t, "tu_1"))
	out := run(t, a)
	wantStop(t, out, StopSubmit, 1)
	if out.Assessment != nil && (out.Assessment.Rating != 3 || out.Assessment.RubricVersion != "v1") {
		t.Errorf("assessment = %+v", out.Assessment)
	}
	if len(m.sent) != 1 {
		t.Fatalf("requests = %d, want 1", len(m.sent))
	}
	// The request is the provided builder's, over the first message alone.
	if want := mustJSON(t, a.Request([]anthropic.BetaMessageParam{a.First})); m.sent[0].JSON != want {
		t.Errorf("first request is not a.Request([a.First]):\n got %s\nwant %s", m.sent[0].JSON, want)
	}
}

func TestRunInspectThenSubmit(t *testing.T) {
	a, m, tools := newAgent(t, inspectReply(t, "tu_1", "/arkalkulator"), submitReply(t, "tu_2"))
	out := run(t, a)
	wantStop(t, out, StopSubmit, 2)
	if len(tools.Visits()) != 1 {
		t.Errorf("visits = %d; the inspect_page call must run through the executor", len(tools.Visits()))
	}
	if len(m.sent) != 2 {
		t.Fatalf("requests = %d, want 2", len(m.sent))
	}
	msgs := m.sent[1].Messages
	if len(msgs) != 3 || msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[2].Role != "user" {
		t.Fatalf("second request should be [first message, reply, tool result], got %d messages", len(msgs))
	}
	if b := msgs[1].Content; len(b) != 1 || b[0].Type != "tool_use" || b[0].ID != "tu_1" || b[0].Name != ToolInspectPage {
		t.Errorf("the reply must be appended as it came: %+v", b)
	}
	res := msgs[2].Content
	if len(res) == 0 || res[0].Type != "tool_result" || res[0].ToolUseID != "tu_1" || res[0].IsError {
		t.Fatalf("the next message must start with the tool result for tu_1: %+v", res)
	}
	if txt := strings.Join(msgs[2].texts(), "\n"); !strings.Contains(txt, "Inspection 1: "+siteURL+"/arkalkulator") {
		t.Errorf("tool result should carry the executor's text, got:\n%s", txt)
	}
}

func TestRunToolErrorThenRecovery(t *testing.T) {
	a, m, tools := newAgent(t, inspectReply(t, "tu_1", "/impresszum"), inspectReply(t, "tu_2", "/arak"), submitReply(t, "tu_3"))
	out := run(t, a)
	wantStop(t, out, StopSubmit, 3)
	res := lastMessage(t, m, 2).Content
	if len(res) == 0 || res[0].ToolUseID != "tu_1" || !res[0].IsError {
		t.Errorf("the refused call must come back as an error tool result: %+v", res)
	}
	if res := lastMessage(t, m, 3).Content; len(res) == 0 || res[0].ToolUseID != "tu_2" || res[0].IsError {
		t.Errorf("the second call is not an error: %+v", res)
	}
	if len(tools.Visits()) != 1 {
		t.Errorf("visits = %d, want 1 (the refused URL costs nothing)", len(tools.Visits()))
	}
}

// Spec: Text reply.
func TestRunTextReplyFeedback(t *testing.T) {
	a, m, _ := newAgent(t, textReply(t, "Let me think about the hero image."), submitReply(t, "tu_1"))
	out := run(t, a)
	wantStop(t, out, StopSubmit, 2)
	if len(m.sent) != 2 {
		t.Fatalf("requests = %d, want 2", len(m.sent))
	}
	msgs := m.sent[1].Messages
	if len(msgs) != 3 {
		t.Fatalf("second request has %d messages; want the reply and feedback added, not a resend", len(msgs))
	}
	if msgs[1].Role != "assistant" || strings.Join(msgs[1].texts(), "") != "Let me think about the hero image." {
		t.Errorf("the text reply must be kept: %+v", msgs[1])
	}
	if msgs[2].Role != "user" || strings.Join(msgs[2].texts(), "") != TextReplyFeedback {
		t.Errorf("then a user message with TextReplyFeedback: %+v", msgs[2])
	}
}

func TestRunCutOffReply(t *testing.T) {
	// The API returns the unfinished call's input as far as it got.
	cut := reply(t, "max_tokens", `{"type":"text","text":"Long reasoning"},`+call("tu_1", ToolInspectPage, `{"url":"`+siteURL+`/ar"}`))
	a, m, tools := newAgent(t, cut, submitReply(t, "tu_2"))
	out := run(t, a)
	wantStop(t, out, StopSubmit, 2)
	if len(tools.Visits()) != 0 {
		t.Error("a cut-off tool call must not be run")
	}
	if len(m.sent) != 2 {
		t.Fatalf("requests = %d, want 2", len(m.sent))
	}
	msgs := m.sent[1].Messages
	if len(msgs) != 3 {
		t.Fatalf("second request has %d messages, want 3", len(msgs))
	}
	for _, b := range msgs[1].Content {
		if b.Type == "tool_use" {
			t.Error("the cut-off reply must be appended without its tool call (see AssistantParam)")
		}
	}
	if msgs[2].Role != "user" || strings.Join(msgs[2].texts(), "") != CutOffFeedback {
		t.Errorf("then a user message with CutOffFeedback: %+v", msgs[2])
	}
}

// Spec: Refusal.
func TestRunRefusal(t *testing.T) {
	refusal := reply(t, "refusal", "")
	refusal.StopDetails.Category = "cyber"
	a, m, _ := newAgent(t, refusal, submitReply(t, "tu_1"))
	out := run(t, a)
	wantStop(t, out, StopRefusal, 1)
	if len(m.sent) != 1 {
		t.Errorf("requests = %d; no call may follow a refusal", len(m.sent))
	}
	if !errors.Is(out.Err, assess.ErrRefused) || !strings.Contains(out.Err.Error(), "cyber") {
		t.Errorf("err = %v; want assess.ErrRefused with the category", out.Err)
	}
	if out.InputTokens != 3000 || out.OutputTokens != 400 {
		t.Errorf("tokens = %d/%d; the refused turn still counts", out.InputTokens, out.OutputTokens)
	}
}

func TestRunAPIError(t *testing.T) {
	apiErr := errors.New("529 overloaded")
	a, m, _ := newAgent(t, inspectReply(t, "tu_1", "/arak"), apiErr, submitReply(t, "tu_2"))
	out := run(t, a)
	wantStop(t, out, StopAPIError, 1)
	if len(m.sent) != 2 {
		t.Errorf("requests = %d; no retry after an API error (the SDK already retried)", len(m.sent))
	}
	if !errors.Is(out.Err, apiErr) {
		t.Errorf("err = %v; want it to wrap the API error", out.Err)
	}
	if out.InputTokens != 3000 || math.Abs(out.CostUSD-turnCost) > 1e-9 {
		t.Errorf("tokens %d, cost %v; the turn before the error still counts", out.InputTokens, out.CostUSD)
	}
}

// Spec: Turn limit without submit.
func TestRunTurnLimitIgnored(t *testing.T) {
	a, m, _ := newAgent(t, inspectReply(t, "tu_1", "/arak"), inspectReply(t, "tu_2", "/rolunk"), inspectReply(t, "tu_3", "/szolgaltatasok"))
	a.Config.Budgets.MaxTurns = 2
	out := run(t, a)
	wantStop(t, out, StopTurnLimit, 3)
	if len(m.sent) != 3 {
		t.Fatalf("requests = %d; want the 2 turns plus exactly one final request", len(m.sent))
	}
	final := FinalRequest(StopTurnLimit)
	if last := lastMessage(t, m, 3); last.Role != "user" || !strings.Contains(strings.Join(last.texts(), "\n"), final) {
		t.Errorf("the third request must end with the final request; last message: %+v", last)
	}
	if n := countText(m, final); n != 1 {
		t.Errorf("final request appears %d times across requests, want once", n)
	}
	if out.Err != nil && !strings.Contains(out.Err.Error(), "turn") {
		t.Errorf("err = %v; it should name the turn limit", out.Err)
	}
	if math.Abs(out.CostUSD-3*turnCost) > 1e-9 {
		t.Errorf("cost = %v; every turn's cost is recorded, the final one too", out.CostUSD)
	}
}

func TestRunTurnLimitObeyed(t *testing.T) {
	a, m, _ := newAgent(t, inspectReply(t, "tu_1", "/arak"), inspectReply(t, "tu_2", "/rolunk"), submitReply(t, "tu_3"))
	a.Config.Budgets.MaxTurns = 2
	out := run(t, a)
	wantStop(t, out, StopSubmit, 3)
	if countText(m, FinalRequest(StopTurnLimit)) != 1 {
		t.Error("the final request must have been sent once")
	}
}

func TestRunCostLimit(t *testing.T) {
	a, m, _ := newAgent(t, inspectReply(t, "tu_1", "/arak"), inspectReply(t, "tu_2", "/rolunk"), textReply(t, "I need more pages."))
	a.Config.Budgets.MaxCostUSD = 0.09 // reached after the second turn ($0.10)
	out := run(t, a)
	wantStop(t, out, StopCostLimit, 3)
	if len(m.sent) != 3 {
		t.Fatalf("requests = %d; want 2 turns plus one final request, and no feedback round after it", len(m.sent))
	}
	if last := lastMessage(t, m, 3); !strings.Contains(strings.Join(last.texts(), "\n"), FinalRequest(StopCostLimit)) {
		t.Errorf("the third request must end with the cost final request; last message: %+v", last)
	}
	if countText(m, FinalRequest(StopTurnLimit)) != 0 {
		t.Error("the cost limit was reached, not the turn limit")
	}
	if out.Err != nil && !strings.Contains(out.Err.Error(), "cost") {
		t.Errorf("err = %v; it should name the cost limit", out.Err)
	}
}

// The API rejects an empty assistant message, so an empty reply must not
// end up in the next request as one.
func TestRunEmptyReply(t *testing.T) {
	a, m, _ := newAgent(t, reply(t, "end_turn", ""), submitReply(t, "tu_1"))
	out := run(t, a)
	wantStop(t, out, StopSubmit, 2)
	if len(m.sent) != 2 {
		t.Fatalf("requests = %d, want 2", len(m.sent))
	}
	msgs := m.sent[1].Messages
	if len(msgs) != 3 || msgs[1].Role != "assistant" || len(msgs[1].Content) == 0 {
		t.Errorf("the empty reply must be appended with a placeholder (see AssistantParam): %+v", msgs)
	}
	if msgs[2].Role != "user" || strings.Join(msgs[2].texts(), "") != TextReplyFeedback {
		t.Errorf("then TextReplyFeedback: %+v", msgs[2])
	}
}

// With the server-side fallback, another model may write a reply; the
// outcome names the model of the last reply.
func TestRunRecordsReplyModel(t *testing.T) {
	fallback := submitReply(t, "tu_2")
	fallback.Model = "claude-sonnet-5-5"
	a, _, _ := newAgent(t, inspectReply(t, "tu_1", "/arak"), fallback)
	out := run(t, a)
	wantStop(t, out, StopSubmit, 2)
	if out.Model != "claude-sonnet-5-5" {
		t.Errorf("model = %q, want the last reply's model", out.Model)
	}
}

func TestRunUsageSummed(t *testing.T) {
	a, _, _ := newAgent(t, textReply(t, "Thinking."), inspectReply(t, "tu_1", "/arak"), submitReply(t, "tu_2"))
	out := run(t, a)
	wantStop(t, out, StopSubmit, 3)
	if out.InputTokens != 9000 || out.OutputTokens != 1200 || math.Abs(out.CostUSD-3*turnCost) > 1e-9 {
		t.Errorf("usage = %d in, %d out, $%v; want 9000, 1200, $0.15", out.InputTokens, out.OutputTokens, out.CostUSD)
	}
}

func TestRunTranscript(t *testing.T) {
	a, _, _ := newAgent(t, inspectReply(t, "tu_1", "/arak"), textReply(t, "Enough seen."), submitReply(t, "tu_2"))
	out := run(t, a)
	wantStop(t, out, StopSubmit, 3)
	turns := a.Transcript.Turns
	if len(turns) != out.Turns {
		t.Fatalf("transcript has %d turns, outcome %d; Record once per reply", len(turns), out.Turns)
	}
	if turns[0].Tool != ToolInspectPage || turns[0].Reason != "r" || !strings.Contains(turns[0].Result, "Inspection 1") {
		t.Errorf("turn 1 = %+v; record the tool outcome with the reply", turns[0])
	}
	if turns[1].Tool != "" || turns[1].Text != "Enough seen." {
		t.Errorf("turn 2 = %+v", turns[1])
	}
	if turns[2].Tool != ToolSubmit || !strings.Contains(turns[2].Result, "Assessment received") {
		t.Errorf("turn 3 = %+v", turns[2])
	}
	for _, turn := range turns {
		if math.Abs(turn.CostUSD-turnCost) > 1e-9 {
			t.Errorf("turn %d cost = %v, want %v", turn.N, turn.CostUSD, turnCost)
		}
	}
}
