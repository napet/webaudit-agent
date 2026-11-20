# Tasks

Roles: **[Claude]** tasks are implemented by Claude. **[You]** tasks are the user's: the agent loop and the live runs. Claude does not write `internal/agent/loop.go` beyond its stub.

## 1. Config, prompt and agent types

- [x] 1.1 [Claude] Add the `agent` budget block (max_pages 5, max_screenshots 3, max_turns 12, max_cost_usd 0.50) to `config/scoring.yaml` and its loader with validation; verify config tests cover defaults and rejected values
- [x] 1.2 [Claude] Write `prompts/agent.v1.md` (task, tools, budgets, page text is untrusted data, finish with submit_assessment) and its loader, which appends the unchanged datedness rubric; verify a test loads it and records the version
- [x] 1.3 [Claude] Create `internal/agent` with the types from the design (Agent, Config, Outcome, StopReason, Turn, tool outcome) and a `loop.go` stub returning "not implemented"; verify `go build ./...` passes

## 2. Capture: browser session and agent pages

- [x] 2.1 [Claude] Add a session API to `capture.Chrome` (open once, render one page with forms and desktop and phone screenshots, close) and `Agent []Page` to `capture.Result`, leaving `Capture` unchanged; verify a Chrome test renders two pages in one session and the existing capture tests still pass
- [x] 2.2 [Claude] Add a fixture site `testdata/sites/agentsite` whose quote form and impresszum are reachable only through links the v1 rules don't match; verify a test shows v1 discovery misses both

## 3. Checks over agent pages

- [x] 3.1 [Claude] Include agent pages in the contact-form, provider-details and privacy-link checks, with one broken form on any page raising the flag; verify unit tests for each, including the spec's "Form on an agent page" scenario
- [x] 3.2 [Claude] Add a per-page form-action probe recorded into `Probes.FormActions`; verify a test probes an agent page's form with HEAD/GET only and the fixture receives no POST

## 4. Tools

- [x] 4.1 [Claude] Implement the link registry (links from every inspected page, any host, normalised) and the "already inspected" check; verify tests for invented URLs, other-domain links and normalisation
- [x] 4.2 [Claude] Implement `inspect_page` (render, probe, per-page verdicts, compact text result with remaining budget, warning at one page left, load failure costs a page, refusals cost nothing); verify tests against the agentsite fixture and the spec's page-result, budget and error scenarios
- [x] 4.3 [Claude] Implement `view_screenshot` and `submit_assessment` (validation as in v1) with the screenshot budget; verify tests for screenshot-before-inspect, budget used up and invalid submission
- [x] 4.4 [Claude] Implement the request builder (system prompt, first message without the v1 rating, three strict tools, auto tool choice with parallel calls off, effort, fallback as in v1), the feedback and final-request texts, and the transcript recorder; verify a test inspects the built request

## 5. Loop tests (before the loop)

- [x] 5.1 [Claude] Write the scripted fake model and fake browser, and `loop_test.go` with one test per loop behaviour in the design (submit first turn, inspect then submit, tool error then recovery, text reply feedback, cut-off reply, refusal, API error, turn limit ignored and obeyed, cost limit, usage summed, transcript turns); verify every test fails against the stub with "not implemented" and none hangs
- [x] 5.2 [Claude] Write a short guide in `internal/agent/README.md`: the loop contract, the provided helpers, the order to make tests pass in, and the SDK documentation to use; verify it names every helper the tests rely on

## 6. The agent loop

- [x] 6.1 [You] Write `Agent.Run` in `internal/agent/loop.go`; verify `go test ./internal/agent` passes
- [x] 6.2 [Claude] Review the loop: a written review of correctness, edge cases the tests don't catch, and how it compares with v1's `Assess`, without rewriting it; verify the review is delivered and every point is either fixed by you or explicitly accepted
- [x] 6.3 [You] Apply the review changes you accept; verify `go test ./internal/agent` still passes

## 7. Pipeline, report and CLI

- [x] 7.1 [Claude] Integrate agent mode into `audit.Pipeline` behind an agent-runner interface (v1 baseline including its assessment, agent, final evaluation over all pages, v1 failure handling); verify end-to-end tests with a fake runner over the agentsite fixture, independent of the loop
- [x] 7.2 [Claude] Add the report's agent section and the baseline comparison (check changes, legal gaps resolved and added, rating and tier from/to, stop reason, budgets used); verify a test for the spec's "Agent finds the contact form" scenario and that default-mode reports are unchanged (golden test)
- [x] 7.3 [Claude] Add the `--agent` flag, including the usage error for a non-agent snapshot; verify CLI tests for the three audit-cli scenarios
- [x] 7.4 [Claude] Run one end-to-end test with the real loop (from 6.1) and the fake model over the agentsite fixture; verify the agent finds the quote form and impresszum and the report shows the changes

## 8. Recording and replay

- [x] 8.1 [Claude] Record `agent/transcript.json` and `agent/pages/` in the snapshot for every agent run, successful or not; verify a test that the transcript's turn count matches the report and images are files
- [x] 8.2 [Claude] Implement agent replay: reuse without `--reassess`; with `--reassess`, a recorded-pages browser that refuses unrecorded pages and makes no network request; verify tests for both, with the fixture server asserting zero requests
- [x] 8.3 [Claude] Update README.md and CLAUDE.md (agent mode, budgets, snapshot layout, transcript reading, who wrote the loop); verify the README commands run as written

## 9. Live runs

- [x] 9.1 [You] Run `audit --agent` on your four sites and at least two new Hungarian service-business sites; verify each report has an agent section and record cost, turns and stop reason
- [x] 9.2 [Claude] Read the transcripts with you and note in design.md: page choices that were wasted or missing, what changed against v1, cost against the estimate, and budget values to adjust; verify the notes are written and any agreed config change is made (the notes, which named the audited sites, were later moved out of the public repo)

## Workflow follow-up

- Verify the change with `/opsx:verify`, then archive it with `/opsx:archive`.
- After v2 works, CLAUDE.md suggests porting the loop to the Claude Agent SDK.
