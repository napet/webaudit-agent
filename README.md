# webaudit-agent

A command-line tool that audits one small-business website and prints a JSON report: how outdated it looks, what is objectively broken, and the evidence for each finding. With `--agent`, an AI agent then decides which further pages of the site to inspect before it gives its rating.

This is a hands-on project for learning agentic AI development in Go, with **no agent framework**: the model calls tools in a loop that was written by hand against the Anthropic API. It is a learning project, not a product.

Deterministic checks (headless Chrome, PageSpeed Insights, axe-core, heuristics) do everything they can. The LLM rates only what they can't: how visually dated the site looks, on a 1–5 rubric. Flags and scores always come from deterministic code, never from the model.

## What it shows

- **A hand-written agent loop** (`internal/agent/loop.go`): one model, three tools, budgets for pages, screenshots, turns and cost, and explicit stop reasons. See [internal/agent/README.md](internal/agent/README.md) for its contract.
- **Tool design and context engineering**: tools return short summaries shaped for the next decision instead of raw HTML; only links the agent has seen can be opened; page text arrives as untrusted data.
- **A fixed workflow first**: the baseline pipeline runs before the agent, so every agent report can be compared with the baseline result of the same run.
- **Replayable runs**: every live audit is recorded as a snapshot, so reports, scoring changes and even the agent can be re-run offline without contacting the site.
- **Spec-driven development**: every feature was planned in [OpenSpec](https://github.com/Fission-AI/OpenSpec) before it was built; the specs are in `openspec/specs/` and the planning history in `openspec/changes/archive/`.

## How this was built

The agent loop, `Agent.Run` in `internal/agent/loop.go`, was written by hand as the core exercise, against tests written first. Everything else (capture, checks, tools, budgets, recording, replay, report) was built with [Claude Code](https://claude.com/claude-code) through the OpenSpec workflow: proposal, design, delta specs and tasks for each change, then implementation and review. [CLAUDE.md](CLAUDE.md) is the guide Claude Code works from, and commits it helped with carry a `Co-Authored-By` line.

## Prerequisites

- Go 1.27.3 or newer
- Google Chrome or Chromium installed locally (found automatically, or pass `--chrome <path>`)
- For live audits, two API keys in the environment:
  - `ANTHROPIC_API_KEY`: Anthropic API key for the datedness assessment (a few cents per audit)
  - `PSI_API_KEY`: Google PageSpeed Insights API key (free; create one in the Google Cloud console)

Replaying a snapshot needs no keys unless you pass `--reassess`.

## Build

```bash
go build ./cmd/audit
```

This produces `audit` (or `audit.exe` on Windows) in the current directory. Run it from the repository root, where it finds `config/` and `prompts/`, or point it there with `--config` and `--prompts`.

## Usage

```bash
go run ./cmd/audit https://example.hu
```

A URL without a scheme is treated as `https://`. The report goes to stdout (progress goes to stderr); `--out report.json` writes it to a file instead.

| Flag | Default | Meaning |
|---|---|---|
| `--out` | stdout | Write the report to this file |
| `--locale` | `hu` | Locale rules from `config/locales/<locale>.yaml` |
| `--model` | from `config/scoring.yaml` | Claude model for the assessment |
| `--config` | `config` | Directory with `scoring.yaml` and `locales/` |
| `--prompts` | `prompts` | Directory with the datedness rubric and the agent prompt |
| `--snapshots` | `snapshots` | Where live runs store their snapshots |
| `--from-snapshot` | | Recompute the report from a snapshot directory |
| `--reassess` | off | With `--from-snapshot`: run the LLM assessment again |
| `--agent` | off | After the baseline audit, let the agent inspect more pages and give the rating |
| `--chrome` | auto | Path to the Chrome binary |

Exit codes: `0` a report was produced (also when the site itself is down: that is a finding), `1` the tool failed (for example Chrome could not start), `2` usage or configuration error (including a missing API key).

### What the report contains

- `tier`: `1` when any hard flag is raised, otherwise `2`. Flags are objective: `no_https`, `site_error_or_slow`, `not_mobile_usable`, `broken_contact_form`.
- `score`: within-tier ranking, 0 (good) to 100 (bad), a weighted average of `sub_scores` using the weights in `config/scoring.yaml`.
- `checks`: every check with its status (`pass`, `fail`, `suspicious`, `not_applicable`, `error`), an explanation and machine-readable evidence.
- `legal_gaps`: Hungarian legal items that could not be found (impresszum, privacy notice, tracking before consent). They are worded as "could not find X" and never raise a flag. Confirm the requirements against the statute text before relying on them.
- `assessment`: the LLM's datedness rating with reasons, model, tokens and cost.
- `components`: which parts succeeded; a failed PageSpeed call or LLM call leaves the rest of the report intact. A failed LLM call still shows what it cost under `components.assessment.usage`, and that cost is included in `cost_usd`.

The tool only sends GET and HEAD requests to audited sites. It never submits forms, clicks cookie banners or POSTs anything.

## Snapshots and replay

Every live audit stores its inputs under `snapshots/<host>/<timestamp>/`: the rendered HTML, both screenshots, network observations, the PageSpeed response, the check results, the assessment and the report. Replaying a snapshot recomputes the report without contacting the site or PageSpeed Insights, and reuses the stored assessment, so it costs nothing:

```bash
go run ./cmd/audit --from-snapshot testdata/snapshots/example
```

(`testdata/snapshots/example` is a committed snapshot of the broken-contact-form fixture site.)

Use replay to:

- tune `config/scoring.yaml` weights and see the new scores immediately;
- test changes to the deterministic checks against recorded sites;
- re-run only the LLM assessment on recorded inputs, for example after editing the rubric: add `--reassess` (needs `ANTHROPIC_API_KEY`).

Snapshots are never modified by replays.

## Agent mode

```bash
go run ./cmd/audit --agent https://example.hu
```

The baseline audit runs first, unchanged, assessment included. Its rules open at most the homepage, one contact page and one legal page, so they miss a quote form behind an unusual menu label or company details inside the terms. Then an agent continues from the baseline result: a model that calls three tools in a loop.

- `inspect_page` opens a page and returns a short summary: status, title, headings, forms with the contact-form check result, provider details, the privacy-notice link and the page's new links. Only URLs that appeared as links on an inspected page can be opened.
  - Link lists (here and in the first message) hold at most 30 links. They put first the links that match the locale's contact, legal or privacy keywords, then links found on more than one inspected page (menus and footers), then the rest in page order.
  - A block of more than 6 such other links, like a list of district pages, shows as its first 3 and "(and N more links like these)". Shortened links can still be inspected.
- `view_screenshot` shows the desktop or phone screenshot of an inspected page.
- `submit_assessment` submits the datedness rating (same rubric and schema as the baseline) and ends the run.

The agent never raises flags or produces scores. After it stops, the deterministic checks run again over every page, the agent's included, and scoring works as before. The agent's rating replaces the baseline's; when the agent does not submit one (a limit, a refusal, an API error), the assessment is marked failed as in the baseline and the score leaves datedness out.

Budgets are in `config/scoring.yaml` under `agent`: extra pages (`max_pages`, default 5), screenshots (`max_screenshots`, 3), model calls (`max_turns`, 12) and cost (`max_cost_usd`, $0.50). When the turn or cost budget runs out, the agent gets one last request to submit. Expect an agent audit to cost several times a baseline audit; the report shows the exact figure.

The report gains an `agent` section: the inspected pages with the agent's reason for each, screenshots viewed, budgets, turns, tokens, cost, `stopped_by` (`submit`, `turn_limit`, `cost_limit`, `refusal`, `api_error`), and `changed`, which compares the result with the baseline result of the same run: check status changes, legal gaps resolved and added, and the rating and tier before and after. `cost_usd` covers the baseline call and every agent turn.

### Agent snapshots and transcripts

An agent run adds to its snapshot:

```
agent/run.json                  what the agent did, and the baseline assessment
agent/transcript.json           every turn: tool, input with the agent's reason, result, tokens, cost
agent/pages/NN/capture.json     each inspected page
agent/pages/NN/desktop.png, mobile.png
```

Read the transcript to see why the agent went where it did; a turn that showed a screenshot names it as `pages/NN/desktop.png`, relative to `agent/`. `testdata/snapshots/agent-example` is a recorded agent run on the agentsite fixture (with a scripted model). Replaying an agent snapshot reuses the recorded run, with or without `--agent`:

```bash
go run ./cmd/audit --from-snapshot testdata/snapshots/agent-example
```

With `--reassess` (needs `ANTHROPIC_API_KEY`), the agent runs again from the recorded baseline result. Its browser serves only the pages the snapshot recorded; any other page is refused, so a re-run never contacts the site. The recorded baseline assessment is kept as it was. Re-running shows how stable the agent's choices are.

## Development

```bash
go test ./...
```

Tests run offline against fixture sites in `testdata/sites/`. Tests that drive Chrome skip when it is not installed, and `go test -short ./...` skips them. One test calls the real Anthropic API (and needs Chrome); it runs only when `ANTHROPIC_API_KEY` is set. Lint with `golangci-lint run`; CI runs `go vet`, golangci-lint and `go test -short ./...`.

Regenerating recorded test data:

- example snapshots: `go test ./internal/audit -run WriteExample -write-example-snapshot` and `go test ./internal/audit -run WriteAgentExample -write-agent-example-snapshot`;
- the baseline-mode golden report: `go test ./internal/audit -run TestDefaultModeReportUnchanged -update-golden`;
- the report contract golden files: `go test ./internal/report -update`.

Code layout:

| Package | Role |
|---|---|
| `cmd/audit` | CLI |
| `internal/audit` | the pipeline: baseline, agent mode, replay |
| `internal/capture` | headless Chrome capture, screenshots, page summary |
| `internal/checks` | deterministic checks, split into `Probe` (network) and `Evaluate` (pure) |
| `internal/psi` | PageSpeed Insights client |
| `internal/assess` | the LLM datedness assessment |
| `internal/agent` | the agent loop and its tools |
| `internal/score` | tiers and scores |
| `internal/report` | the JSON report contract |
| `internal/snapshot` | recording and reading snapshots |
| `internal/config` | `config/` YAML loading |
| `internal/safehttp` | the only HTTP client for audited sites; refuses anything but GET and HEAD |
| `internal/htmltext` | small text and HTML helpers shared by capture, checks and agent |
| `internal/testsite` | test helper serving the fixture sites |

## License

MIT, see [LICENSE](LICENSE). axe-core is included under the MPL-2.0, see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
