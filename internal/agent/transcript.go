package agent

import (
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// Record adds one turn: the reply, its cost, and the outcome of the tool it
// called (nil when it called none, or the tool was not run). It is safe on a
// nil transcript.
func (t *Transcript) Record(msg *anthropic.BetaMessage, cost float64, out *ToolOutcome) {
	if t == nil || msg == nil {
		return
	}
	turn := Turn{
		N:            len(t.Turns) + 1,
		StopReason:   string(msg.StopReason),
		InputTokens:  msg.Usage.InputTokens,
		OutputTokens: msg.Usage.OutputTokens,
		CostUSD:      cost,
	}
	var texts []string
	for _, block := range msg.Content {
		switch b := block.AsAny().(type) {
		case anthropic.BetaTextBlock:
			texts = append(texts, b.Text)
		case anthropic.BetaToolUseBlock:
			turn.Tool = b.Name
			if raw, err := json.Marshal(b.Input); err == nil {
				turn.Input = raw
				var r struct {
					Reason string `json:"reason"`
				}
				if json.Unmarshal(raw, &r) == nil {
					turn.Reason = r.Reason
				}
			}
		}
	}
	turn.Text = strings.Join(texts, "\n")
	if msg.StopReason == anthropic.BetaStopReasonRefusal && turn.Text == "" {
		turn.Text = "refusal (category " + string(msg.StopDetails.Category) + ")"
	}
	if out != nil {
		turn.Result, turn.Image, turn.IsError = out.Text, out.Image, out.IsError
	}
	t.Turns = append(t.Turns, turn)
}
