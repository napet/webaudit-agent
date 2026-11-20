# Proposal

## Why

v1 looks at a fixed set of pages: the homepage, plus one contact page and one legal page found by keyword rules. Auditing real Hungarian sites showed where that breaks: menus built from buttons, a quote form on another domain, company details hidden in the privacy notice. Each gap needed a new rule, and the next site will have a gap no rule foresaw. Choosing which pages to look at is the one step in the audit whose steps can't be known in advance, so it is where a model should get control. It is also the next step in the project's build order, and the first real agent loop.

## What Changes

- New opt-in agent mode, `audit --agent <url>`. Without the flag the tool behaves exactly as v1.
- In agent mode the v1 pipeline runs first, unchanged. A single agent then starts from that result, inspects extra pages of the site that it chooses, within budgets, and finishes by submitting the datedness assessment (same rubric and schema as v1).
- Three tools: `inspect_page` (load a page and return a compact text summary with per-page check results and its links), `view_screenshot` (an inspected page's screenshot, on request) and `submit_assessment` (as v1, ends the loop). The agent can only open URLs that appeared as links on pages it has already inspected.
- Four budgets, in config: extra pages, screenshots, turns (model calls) and cost per audit. Tool results show what is left; exhausted budgets lead to a final "submit now" message and then to v1's failure handling.
- Errors the model can act on are returned to it as error tool results that say what to do next; a reply without a tool call gets a feedback message instead of v1's blind retry.
- Pages the agent inspects are added to the pages the deterministic checks evaluate (contact form, provider details, privacy link). The agent never reports findings, flags or scores itself.
- Every agent run records a transcript (each turn's tool call, reason, result, tokens and cost) and the inspected pages in the snapshot. Replays reuse the result; `--reassess` re-runs the agent against the recorded pages only, without contacting the site.
- The report gains an `agent` section: inspected pages with the agent's reasons, turns, cost, why the loop stopped, and what changed against the v1 result of the same run.
- The agent loop itself is written by the user, as a learning exercise, against tests written first; tools, recording, replay and reporting are written by Claude.
- No prompt caching and no parallel tool calls in this version: both are left as measured follow-ups.

## Capabilities

### New Capabilities
- `agent-assessment`: the agent loop that extends a v1 audit: its tools, budgets, error feedback, how inspected pages feed the checks, transcript recording and replay, and the report's agent section.

### Modified Capabilities
- `datedness-assessment`: the "exactly one LLM call per audit" requirement applies to the default mode only; agent mode makes one call per turn.
- `site-capture`: the limit of one contact page and one legal page applies to rule-based discovery; agent mode adds pages the agent chooses.
- `site-checks`: the passive contact-form check also covers forms on pages the agent inspected.
- `audit-cli`: a new `--agent` flag selects agent mode.

## Impact

- New package `internal/agent` (loop, tools, budgets, transcript, fakes for tests); the loop file is the user's.
- `internal/audit`: an agent stage after the v1 pipeline, a final evaluation over all pages, and agent-aware replay.
- `internal/capture`: rendering a single page on demand for `inspect_page`; `capture.Result` gains the agent's pages.
- `internal/checks`: contact-form, provider-details and privacy-link checks evaluate agent pages; form-action probes for agent pages.
- `internal/report`: new `agent` section (additive; schema version stays 1). `internal/snapshot`: `agent/` directory.
- `cmd/audit`: `--agent` flag. `config/scoring.yaml`: agent budgets. New prompt file for the agent's instructions.
- Cost: an agent audit makes several model calls, expected about 6–8× a v1 audit with Opus, capped by the cost budget.
