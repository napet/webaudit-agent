// Package assess makes the one LLM judgment the checks can't: how visually
// dated a site looks, rated 1–5 against a written rubric.
//
// The call is a one-turn version of the agent loop: the model gets a
// strict tool, submit_assessment, and finishes by calling it. The Go code
// reads the tool_use block, validates it, and retries once if it is missing
// or invalid.
package assess

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/config"
	"github.com/napet/webaudit-agent/internal/report"
)

// ToolName is the tool the model must call to submit its rating.
const ToolName = "submit_assessment"

// RubricFile is the rubric in the prompts directory that ratings follow.
const RubricFile = "datedness-rubric.v1.md"

// MaxTokens is the output limit of an assessment call.
const MaxTokens = 16000

// Input is what the model sees.
type Input struct {
	URL        string
	DesktopPNG []byte
	MobilePNG  []byte
	Summary    string
	Checks     []report.CheckResult
}

// Assessor produces a datedness assessment. The pipeline depends on this
// interface so tests and snapshot replays can run without the API.
type Assessor interface {
	Assess(ctx context.Context, in Input) (*report.Assessment, error)
}

// Messages is the part of the SDK client the assessor uses
// (client.Beta.Messages satisfies it).
type Messages interface {
	New(ctx context.Context, body anthropic.BetaMessageNewParams, opts ...option.RequestOption) (*anthropic.BetaMessage, error)
}

// Rubric is the versioned rating guide given to the model.
type Rubric struct {
	Version string
	Text    string
}

// LoadRubric reads a rubric file named like datedness-rubric.v1.md; the
// version is the part between the last two dots.
func LoadRubric(path string) (Rubric, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Rubric{}, err
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	i := strings.LastIndex(base, ".")
	if i < 0 || i == len(base)-1 {
		return Rubric{}, fmt.Errorf("%s: name must end in .<version>.md", path)
	}
	return Rubric{Version: base[i+1:], Text: string(b)}, nil
}

// ErrRefused is returned when the model declined the request.
var ErrRefused = errors.New("assessment refused")

// CallError is a failed assessment that still made billed API calls. It
// carries their usage so the report can show what the failure cost.
type CallError struct {
	Err   error
	Usage report.Usage
}

func (e *CallError) Error() string { return e.Err.Error() }
func (e *CallError) Unwrap() error { return e.Err }

// Claude is the live assessor.
type Claude struct {
	API    Messages
	Model  string
	Price  config.ModelPrice
	Rubric Rubric
}

// Models that accept the server-side refusal fallback.
var fallbackModels = map[string]bool{
	"claude-fable-5-1": true, "claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true,
}

const systemPrompt = `You assess how visually dated small-business websites look, for an audit that will be explained to the business owner. You see the homepage's first screen on desktop and on a phone, a short summary of the page, and the results of automated checks.

Rate visual datedness with the rubric below, then finish by calling the submit_assessment tool exactly once. Do not reply with text instead of the tool call.

`

// SupportsFallback reports whether a model accepts the server-side refusal
// fallback.
func SupportsFallback(model string) bool { return fallbackModels[model] }

// ToolSchema is the input schema of submit_assessment. The agent uses the
// same one.
func ToolSchema() anthropic.BetaToolInputSchemaParam {
	return anthropic.BetaToolInputSchemaParam{
		Properties: map[string]any{
			"rating": map[string]any{
				"type": "integer", "enum": []int{1, 2, 3, 4, 5},
				"description": "1 = looks current, 5 = very outdated, per the rubric levels.",
			},
			"confidence": map[string]any{
				"type": "string", "enum": []string{"low", "medium", "high"},
				"description": "Low when the screenshots are mostly a banner, pop-up, loading screen or error.",
			},
			"reasons": map[string]any{
				"type":        "array",
				"description": "Two to six short reasons. Each names what it is based on.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"subject": map[string]any{"type": "string", "description": `The visible element ("hero image", "navigation", "mobile layout") or "check: <id>".`},
						"detail":  map[string]any{"type": "string", "description": "One sentence on what looks dated or current about it."},
					},
					"required":             []string{"subject", "detail"},
					"additionalProperties": false,
				},
			},
		},
		Required:    []string{"rating", "confidence", "reasons"},
		ExtraFields: map[string]any{"additionalProperties": false},
	}
}

type compactCheck struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Explanation string `json:"explanation,omitempty"`
}

// UserContent is the user message content: the screenshots, the page summary
// and the compact check results, followed by closing, which says what to do
// with them. The agent's first message uses it with its own closing.
func UserContent(in Input, closing string) []anthropic.BetaContentBlockParamUnion {
	var checks []compactCheck
	for _, r := range in.Checks {
		cc := compactCheck{ID: r.ID, Status: string(r.Status)}
		if r.Status != report.StatusPass {
			cc.Explanation = r.Explanation
		}
		checks = append(checks, cc)
	}
	checksJSON, _ := json.Marshal(checks) // cannot fail: only string fields

	content := []anthropic.BetaContentBlockParamUnion{
		anthropic.NewBetaTextBlock("Site: " + in.URL + "\n\nDesktop screenshot (" + capture.DesktopSize + ", first screen):"),
	}
	if len(in.DesktopPNG) > 0 {
		content = append(content, anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{
			Data: base64.StdEncoding.EncodeToString(in.DesktopPNG), MediaType: anthropic.BetaBase64ImageSourceMediaTypeImagePNG,
		}))
	}
	content = append(content, anthropic.NewBetaTextBlock("Phone screenshot ("+capture.MobileSize+", first screen):"))
	if len(in.MobilePNG) > 0 {
		content = append(content, anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{
			Data: base64.StdEncoding.EncodeToString(in.MobilePNG), MediaType: anthropic.BetaBase64ImageSourceMediaTypeImagePNG,
		}))
	}
	return append(content, anthropic.NewBetaTextBlock(
		"Page summary:\n"+in.Summary+"\n\nAutomated check results:\n"+string(checksJSON)+"\n\n"+closing))
}

// Request builds the API request. Exported so tests can inspect it.
func (c *Claude) Request(in Input) anthropic.BetaMessageNewParams {
	content := UserContent(in, "Rate the visual datedness and call "+ToolName+".")

	p := anthropic.BetaMessageNewParams{
		Model:     c.Model,
		MaxTokens: MaxTokens,
		System:    []anthropic.BetaTextBlockParam{{Text: systemPrompt + c.Rubric.Text}},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(content...)},
		Tools: []anthropic.BetaToolUnionParam{{OfTool: &anthropic.BetaToolParam{
			Name:        ToolName,
			Description: anthropic.String("Submit the visual datedness assessment. Call exactly once, as the final step."),
			InputSchema: ToolSchema(),
			Strict:      anthropic.Bool(true),
		}}},
		// Current models reject a forced tool_choice, so the prompt asks for
		// the call and a missing call is handled as an invalid answer.
		ToolChoice:   anthropic.BetaToolChoiceUnionParam{OfAuto: &anthropic.BetaToolChoiceAutoParam{DisableParallelToolUse: anthropic.Bool(true)}},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortMedium},
	}
	if SupportsFallback(c.Model) {
		p.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		p.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}
	return p
}

type toolInput struct {
	Rating     int             `json:"rating"`
	Confidence string          `json:"confidence"`
	Reasons    []report.Reason `json:"reasons"`
}

func (t toolInput) validate() error {
	if t.Rating < 1 || t.Rating > 5 {
		return fmt.Errorf("rating %d out of range", t.Rating)
	}
	switch t.Confidence {
	case "low", "medium", "high":
	default:
		return fmt.Errorf("confidence %q invalid", t.Confidence)
	}
	if len(t.Reasons) < MinReasons {
		return fmt.Errorf("%d reason(s) given, at least %d needed; %s", len(t.Reasons), MinReasons, reasonRule)
	}
	for i, r := range t.Reasons {
		if placeholder(r) {
			return fmt.Errorf("reason %d (subject %q, detail %q) is empty or a placeholder; %s", i+1, r.Subject, r.Detail, reasonRule)
		}
	}
	return nil
}

// MinReasons is the fewest reasons an assessment may give.
const MinReasons = 2

// reasonRule ends a validation error, so the model knows how to fix it.
const reasonRule = `each reason names a visible element ("hero image", "navigation") or "check: <id>" and says in one sentence what looks dated or current about it`

// placeholderWords are subjects or details that carry no evidence.
var placeholderWords = map[string]bool{
	"placeholder": true, "todo": true, "tbd": true, "n/a": true, "na": true,
	"none": true, "test": true, "lorem ipsum": true, "...": true,
}

// placeholder reports a reason that names nothing or says nothing: a subject
// under three characters or a placeholder word, or a detail under three words.
// The rules are loose on purpose: they catch answers with no evidence, not
// terse ones.
func placeholder(r report.Reason) bool {
	subject := strings.ToLower(strings.TrimSpace(r.Subject))
	detail := strings.ToLower(strings.Trim(strings.TrimSpace(r.Detail), ".!"))
	return utf8.RuneCountInString(subject) < 3 || placeholderWords[subject] ||
		len(strings.Fields(detail)) < 3 || placeholderWords[detail]
}

// Decode reads and validates a submit_assessment input. Only the rating,
// confidence and reasons are set; the caller fills in the rest.
func Decode(raw json.RawMessage) (*report.Assessment, error) {
	var in toolInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if len(in.Reasons) > 8 {
		in.Reasons = in.Reasons[:8]
	}
	return &report.Assessment{Rating: in.Rating, Confidence: in.Confidence, Reasons: in.Reasons}, nil
}

// parse extracts and validates the tool call. It returns an error for any
// answer the caller should retry.
func parse(msg *anthropic.BetaMessage) (*report.Assessment, error) {
	if msg.StopReason == anthropic.BetaStopReasonMaxTokens {
		return nil, errors.New("hit max_tokens before calling the tool")
	}
	for _, block := range msg.Content {
		tu, ok := block.AsAny().(anthropic.BetaToolUseBlock)
		if !ok || tu.Name != ToolName {
			continue
		}
		raw, err := json.Marshal(tu.Input)
		if err != nil {
			return nil, err
		}
		a, err := Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("tool input: %w", err)
		}
		return a, nil
	}
	return nil, fmt.Errorf("no %s call in the reply (stop reason %q)", ToolName, msg.StopReason)
}

// Assess makes the call, retrying once on a missing or invalid tool call.
// Token usage and cost cover every attempt; when the assessment fails after
// a call went through, the error is a *CallError carrying that usage.
func (c *Claude) Assess(ctx context.Context, in Input) (*report.Assessment, error) {
	req := c.Request(in)
	var inTok, outTok int64
	var lastErr error
	fail := func(err error) error {
		if inTok == 0 && outTok == 0 {
			return err
		}
		return &CallError{Err: err, Usage: report.Usage{Model: c.Model, InputTokens: inTok, OutputTokens: outTok, CostUSD: c.Price.Cost(inTok, outTok)}}
	}
	for attempt := 0; attempt < 2; attempt++ {
		msg, err := c.API.New(ctx, req)
		if err != nil {
			return nil, fail(fmt.Errorf("assessment call: %w", err))
		}
		inTok += msg.Usage.InputTokens
		outTok += msg.Usage.OutputTokens
		if msg.StopReason == anthropic.BetaStopReasonRefusal {
			cat := string(msg.StopDetails.Category)
			if cat == "" {
				cat = "uncategorized"
			}
			return nil, fail(fmt.Errorf("%w (category %s)", ErrRefused, cat))
		}
		a, err := parse(msg)
		if err != nil {
			lastErr = err
			continue
		}
		model := msg.Model
		if model == "" {
			model = c.Model
		}
		a.RubricVersion, a.Model = c.Rubric.Version, model
		a.InputTokens, a.OutputTokens, a.CostUSD = inTok, outTok, c.Price.Cost(inTok, outTok)
		return a, nil
	}
	return nil, fail(fmt.Errorf("invalid assessment after retry: %w", lastErr))
}
