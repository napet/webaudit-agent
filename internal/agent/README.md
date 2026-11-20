# internal/agent: the hand-written loop

`Agent.Run` in `loop.go` is the agent loop, written by hand as the core exercise of this project. Everything around it (tools, budgets, transcript, recording) was built first, and `loop_test.go` was written as its specification before the loop existed. This file is the contract the loop was written against; it still describes how the loop behaves.

## The contract

`Run(ctx) (*Outcome, error)` drives one conversation:

1. Start the conversation with `a.First`.
2. Before each turn, check the limits: `a.Config.Budgets.MaxTurns` (model calls so far) and `a.Config.Budgets.MaxCostUSD` (cost so far). When one is reached without a submission, add `FinalRequest(StopTurnLimit)` or `FinalRequest(StopCostLimit)` **once** and make one last call. If that reply does not submit, stop with that reason.
3. Each turn: call `a.API.New(ctx, a.Request(messages))`. Add the reply's usage to the totals, with cost `a.Config.Price.Cost(in, out)`. Record the turn with `a.Transcript.Record(msg, cost, outcome)`, where `outcome` is the tool's outcome or nil when no tool ran.
4. Branch on the reply:
   - **refusal**: stop with `StopRefusal`. Wrap `assess.ErrRefused` and name `msg.StopDetails.Category` in the error.
   - **tool call**: append `AssistantParam(msg)`. Run the tool with `a.Tools.Execute(ctx, name, input)` and append a user message holding `outcome.Block(toolUseID)`. If `outcome.Submitted != nil`, stop with `StopSubmit` and that assessment.
   - **text only** (`end_turn`), or **cut off** (`max_tokens`): append `AssistantParam(msg)`, then `TextReplyFeedback` or `CutOffFeedback` as a user message. A cut-off reply's tool call is never run.
5. An error from `New` stops the loop with `StopAPIError`. The SDK has already retried, so wrap the error and don't retry.

Every stop returns an `Outcome` and a nil error. Fill in `StoppedBy`, `Assessment` (only on submit), `Err` (why it did not submit), `Turns` (calls that returned a reply), `Model` (the last reply's `msg.Model`, which differs from the requested one when the fallback answered), `InputTokens`, `OutputTokens` and `CostUSD`. Leave the assessment's model and token fields alone: the pipeline fills them in.

The tests leave one thing open: whether a non-submit tool call in the reply to the final request is still run. Not running it is cheaper.

## Provided helpers

| Helper | Use |
|---|---|
| `a.Request(messages)` | the full request: system prompt, tools, tool choice, effort, fallback |
| `a.First` | the opening user message |
| `a.Tools.Execute(ctx, name, input)` | runs a tool; never fails; enforces page and screenshot budgets |
| `ToolOutcome.Block(id)` | the `tool_result` block, marked as an error when it is one |
| `AssistantParam(msg)` | the reply as a message to append; drops the tool call of a cut-off reply |
| `WithUserText(messages, text)` | adds text as a user message, or joins it to the last one when that is the user's (after a tool result) |
| `TextReplyFeedback`, `CutOffFeedback`, `FinalRequest(reason)` | the texts the loop adds |
| `a.Config.Price.Cost(in, out)` | USD cost of one call |
| `a.Transcript.Record(msg, cost, outcome)` | records a turn; safe when the transcript is nil |
| `assess.ErrRefused` | wrap it for refusals, as the baseline does |

`anthropic.NewBetaUserMessage(blocks...)` builds a user message from blocks.

## The order the tests were made to pass

1. `TestRunSubmitFirstTurn`: one call, find the tool call, execute it, stop on `Submitted`.
2. `TestRunInspectThenSubmit`, `TestRunToolErrorThenRecovery`: append the reply and the tool result, then loop.
3. `TestRunUsageSummed`, `TestRunTranscript`: totals and `Record`.
4. `TestRunTextReplyFeedback`, `TestRunCutOffReply`: the feedback branch.
5. `TestRunRefusal`, `TestRunAPIError`: the stops.
6. `TestRunTurnLimitIgnored`, `TestRunTurnLimitObeyed`, `TestRunCostLimit`: the final request.

The fake model fails a test as soon as the loop calls it more often than scripted, so a loop that doesn't stop fails fast instead of hanging.

## SDK pieces the loop uses

From `github.com/anthropics/anthropic-sdk-go` v1.84.0, Beta Messages API (see `betamessage.go` in the module cache, or https://github.com/anthropics/anthropic-sdk-go):

- `msg.StopReason`: compare with `anthropic.BetaStopReasonToolUse`, `BetaStopReasonEndTurn`, `BetaStopReasonMaxTokens`, `BetaStopReasonRefusal`.
- `msg.Content` blocks: `switch b := block.AsAny().(type)` with `case anthropic.BetaToolUseBlock:` (`b.ID`, `b.Name`, `b.Input`).
- `b.Input` is `any`. `json.Marshal(b.Input)` gives the `json.RawMessage` that `Execute` takes.
- `msg.Usage.InputTokens`, `msg.Usage.OutputTokens`.
- `msg.StopDetails.Category` for refusals.
- The baseline's `assess.Claude.Assess` is a one-turn version of this loop and worth reading first.

Background reading: the "How to implement tool use" page of the Claude API docs (https://platform.claude.com/docs/en/agents-and-tools/tool-use/implement-tool-use), especially handling `tool_use` and `tool_result` blocks and `max_tokens` stops.
