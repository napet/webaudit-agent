// Package agent is the agent-mode assessment: a single agent that starts from a baseline
// audit, inspects extra pages of the site it chooses within budgets, and
// finishes by submitting the datedness assessment.
//
// The loop (loop.go) is hand-written by the user against loop_test.go.
// Everything it calls is provided here: the request builder, the tool
// executor, the feedback texts and the transcript recorder.
package agent

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/napet/webaudit-agent/internal/assess"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
)

// StopReason says why the loop ended.
type StopReason string

// The reasons a run can stop.
const (
	StopSubmit    StopReason = "submit"     // a valid assessment was submitted
	StopTurnLimit StopReason = "turn_limit" // turns ran out and the final request did not submit
	StopCostLimit StopReason = "cost_limit" // cost ran out and the final request did not submit
	StopRefusal   StopReason = "refusal"    // the model refused
	StopAPIError  StopReason = "api_error"  // a call failed after the SDK's retries
)

// Config is what a run is allowed to do and how it is priced.
type Config struct {
	Model   string
	Price   config.ModelPrice
	Prompt  Prompt
	Budgets config.AgentBudgets
}

// Executor runs one tool call. It never fails: problems the model can act on
// come back as an error outcome, which the loop passes on unchanged.
type Executor interface {
	Execute(ctx context.Context, name string, input json.RawMessage) ToolOutcome
}

// ToolOutcome is the result of one tool call.
type ToolOutcome struct {
	// Content is what the model sees: text, or an image and text.
	Content []anthropic.BetaToolResultBlockParamContentUnion
	// IsError marks the result as an error the model should act on.
	IsError bool
	// Submitted is the validated assessment when the call was a valid
	// submit_assessment; the loop stops when it is set.
	Submitted *report.Assessment

	// For the transcript: the result's text, and which screenshot was shown.
	Text  string
	Image string
}

// Block wraps the outcome as the tool_result block answering toolUseID.
func (o ToolOutcome) Block(toolUseID string) anthropic.BetaContentBlockParamUnion {
	b := anthropic.BetaToolResultBlockParam{ToolUseID: toolUseID, Content: o.Content}
	if o.IsError {
		b.IsError = anthropic.Bool(true)
	}
	return anthropic.BetaContentBlockParamUnion{OfToolResult: &b}
}

// Outcome is the result of a run. Every run returns one, whatever stopped it.
type Outcome struct {
	StoppedBy StopReason
	// Assessment is set only when StoppedBy is StopSubmit. Its token and cost
	// fields are left for the caller, which knows the totals.
	Assessment *report.Assessment
	// Err says why a run that did not submit failed: the refusal category,
	// the API error, or which limit was reached.
	Err error

	// Model is the model that wrote the last reply. It can differ from
	// Config.Model when the server-side fallback answered.
	Model string

	Turns        int // model calls that returned a reply
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
}

// Turn is one model call and what came of it.
type Turn struct {
	N          int             `json:"n"`
	StopReason string          `json:"stop_reason"` // the API's stop reason
	Text       string          `json:"text,omitempty"`
	Tool       string          `json:"tool,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Reason     string          `json:"reason,omitempty"` // the agent's stated reason, from Input
	Result     string          `json:"result,omitempty"`
	Image      string          `json:"image,omitempty"` // which screenshot was shown
	IsError    bool            `json:"is_error,omitempty"`

	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// Transcript is every turn of a run, in order.
type Transcript struct {
	PromptVersion string `json:"prompt_version"`
	Turns         []Turn `json:"turns"`
}

// Agent is one run. The caller builds it; Run (loop.go) drives it.
type Agent struct {
	API    assess.Messages
	Config Config
	Tools  Executor
	// First is the opening user message: the baseline result the agent starts from.
	First anthropic.BetaMessageParam
	// Transcript records every turn. Run must call Record once per reply.
	Transcript *Transcript
}
