package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/napet/webaudit-agent/internal/assess"
)

// Run drives the agent until it submits an assessment or a limit, a refusal
// or an API error stops it. See README.md for the contract and loop_test.go
// for the behaviour it must have.
func (a *Agent) Run(ctx context.Context) (*Outcome, error) {
	if a.API == nil || a.Tools == nil {
		return nil, errors.New("agent needs an API and tools")
	}
	out := &Outcome{}
	messages := []anthropic.BetaMessageParam{a.First}
	var final StopReason // the limit that was reached, once the final request is sent

	for {
		if final == "" {
			if r := a.limitReached(out); r != "" {
				final = r
				messages = WithUserText(messages, FinalRequest(r))
			}
		}

		msg, err := a.API.New(ctx, a.Request(messages))
		if err != nil {
			out.StoppedBy, out.Err = StopAPIError, fmt.Errorf("agent call: %w", err)
			return out, nil
		}
		cost := a.Config.Price.Cost(msg.Usage.InputTokens, msg.Usage.OutputTokens)
		out.Turns++
		out.Model = string(msg.Model)
		out.InputTokens += msg.Usage.InputTokens
		out.OutputTokens += msg.Usage.OutputTokens
		out.CostUSD += cost

		if msg.StopReason == anthropic.BetaStopReasonRefusal {
			a.Transcript.Record(msg, cost, nil)
			cat := string(msg.StopDetails.Category)
			if cat == "" {
				cat = "uncategorized"
			}
			out.StoppedBy, out.Err = StopRefusal, fmt.Errorf("%w (category %s)", assess.ErrRefused, cat)
			return out, nil
		}

		tu, ok := toolCall(msg)
		cutOff := msg.StopReason == anthropic.BetaStopReasonMaxTokens
		if !ok || cutOff {
			// Text only, or cut off: nothing to run.
			a.Transcript.Record(msg, cost, nil)
			if final != "" {
				return limitStop(out, final), nil
			}
			feedback := TextReplyFeedback
			if cutOff {
				feedback = CutOffFeedback
			}
			messages = WithUserText(append(messages, AssistantParam(msg)), feedback)
			continue
		}

		if final != "" && tu.Name != ToolSubmit {
			// Only a submission counts after the final request; don't pay
			// for a page load whose result nobody will read.
			a.Transcript.Record(msg, cost, nil)
			return limitStop(out, final), nil
		}

		input, _ := json.Marshal(tu.Input) // Input came from JSON, so this can't fail
		res := a.Tools.Execute(ctx, tu.Name, input)
		a.Transcript.Record(msg, cost, &res)
		if res.Submitted != nil {
			out.StoppedBy, out.Assessment = StopSubmit, res.Submitted
			return out, nil
		}
		if final != "" {
			return limitStop(out, final), nil // the final submission was invalid
		}
		messages = append(messages, AssistantParam(msg), anthropic.NewBetaUserMessage(res.Block(tu.ID)))
	}
}

// limitReached returns the turn or cost limit that has been reached, or "".
func (a *Agent) limitReached(out *Outcome) StopReason {
	switch {
	case out.Turns >= a.Config.Budgets.MaxTurns:
		return StopTurnLimit
	case out.CostUSD >= a.Config.Budgets.MaxCostUSD:
		return StopCostLimit
	}
	return ""
}

func limitStop(out *Outcome, reason StopReason) *Outcome {
	what := "turn"
	if reason == StopCostLimit {
		what = "cost"
	}
	out.StoppedBy, out.Err = reason, fmt.Errorf("%s limit reached without a submitted assessment", what)
	return out
}

// toolCall returns the reply's tool call. Parallel calls are off, so there
// is at most one.
func toolCall(msg *anthropic.BetaMessage) (anthropic.BetaToolUseBlock, bool) {
	for _, block := range msg.Content {
		if tu, ok := block.AsAny().(anthropic.BetaToolUseBlock); ok {
			return tu, true
		}
	}
	return anthropic.BetaToolUseBlock{}, false
}
