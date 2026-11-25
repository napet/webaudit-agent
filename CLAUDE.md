# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

The baseline `audit <url>` CLI exists (Go module `github.com/napet/webaudit-agent`). The agent loop (`audit --agent`) is built too. Their specs are in `openspec/specs/` (audit-cli, site-capture, site-checks, datedness-assessment, audit-scoring, agent-assessment); the planning history is in `openspec/changes/archive/` (`audit-cli-v1`, then `agent-loop-v2`, then `public-release`). If `private/ROADMAP.md` exists (it is gitignored), read it for planned work. Update this file with real commands and architecture as they land; don't assume tooling that isn't in the repo.

## Commands

- Build: `go build ./cmd/audit` (run from the repo root so it finds `config/` and `prompts/`)
- Audit a site: `go run ./cmd/audit <url>` (needs `ANTHROPIC_API_KEY`, `PSI_API_KEY` and a local Chrome)
- Agent mode: `go run ./cmd/audit --agent <url>` (budgets under `agent` in `config/scoring.yaml`)
- Replay a snapshot without network or LLM cost: `go run ./cmd/audit --from-snapshot <dir>` (`--reassess` re-runs only the LLM call; for an agent snapshot it re-runs the agent on the recorded pages)
- Test: `go test ./...` (offline, against `testdata/sites/` fixtures; `testsite.RunOffline` fails a package's tests if they look up any host name, so point probes at `testsite.LocalOnly`; Chrome tests skip without Chrome or with `-short`; the live API test runs only with `ANTHROPIC_API_KEY`)
- Lint: `golangci-lint run` (config in `.golangci.yml`; CI runs vet, lint and `go test -short ./...`)

## Code layout

The pipeline is capture → checks (PageSpeed Insights in parallel) → assessment → scoring → report, wired in `internal/audit`. `internal/checks` is split into `Probe` (the only network part, recorded in snapshots) and `Evaluate` (pure), so replays recompute everything offline; keep new checks on that split. Agent mode (`internal/audit/agentmode.go`) runs the baseline pipeline first, then `internal/agent`, then the checks again over all pages; the agent's tools are in `internal/agent/tools.go` and its snapshot record in `agent/` inside the snapshot. The JSON report contract is `internal/report`; bump `SchemaVersion` on breaking changes. All requests to audited sites go through `internal/safehttp`, which refuses anything but GET and HEAD. Locale rules, scoring weights and agent budgets are YAML in `config/`; the rubric is `prompts/datedness-rubric.v1.md` (`assess.RubricFile`) and the agent prompt `prompts/agent.v2.md` (`agent.PromptFile`; older versions stay for recorded runs).

"Baseline" means the non-agent pipeline; "v1"/"v2" in file names are prompt, rubric and change versions.

`internal/agent/loop.go` (`Agent.Run`) is the user's hand-written loop, the core exercise of the project. Don't rewrite it unasked: review it, and change it only when the user asks. Its contract and tests are in `internal/agent/README.md` and `loop_test.go`.

## Purpose

A hands-on project for learning agentic AI development. The primary goal is learning. When choosing between approaches, prefer the one that teaches more about building agents over the one that is merely faster to ship, and explain the trade-off.

The tool audits small-business websites: it scores how outdated a site is and produces a JSON report an owner could act on.

## Architecture

- **All Go**, including a hand-written agent loop against the Anthropic API (no agent framework).
- **Crawler / deterministic checks**: headless Chrome via `chromedp` (above-the-fold desktop and mobile screenshots only), Lighthouse via the PageSpeed Insights API, axe-core injected into the page, and heuristics like stale copyright year, no HTTPS, non-responsive layout, old jQuery, table layouts. Contact forms are checked passively: never submit a form or POST to an audited site.
- **LLM assessment**: rates only visual datedness (1–5 rubric, structured output) from screenshots, a trimmed DOM summary and the check results. The baseline makes one call; agent mode turns it into a loop that chooses which pages to inspect, using the checks as tools under a page budget.
- **Scoring**: two tiers. Any objective hard flag (no HTTPS/bad cert, site error or >10s load, not mobile-usable, definitely broken contact form) means tier 1; a weighted sub-score average computed in Go ranks within each tier.
- **Locale**: Hungary first, service businesses only; locale rules live in per-locale config. Hungarian legal checks (impresszum, privacy notice, tracking before consent) are reported as "could not find X" and never raise hard flags.

## Agent development practices (as of mid-2026)

The prevailing approach, to measure designs against. The field moves fast: re-check before relying on it.

- **Single agent, tools, loop.** One capable model calls tools in a loop until done or out of budget. Prefer this over multi-agent setups or fixed agent graphs, which are harder to debug and rarely better. Start with a fixed workflow and give the model control only where the steps can't be known ahead of time.
- **Context engineering.** What the model sees at each step matters most: compact, focused tool results instead of raw dumps; summarize or trim long histories; keep state in files or structs rather than the conversation; load instructions only when needed.
- **Tool design.** Clear names, descriptions and schemas; useful error messages the model can recover from; results shaped for the next decision. Most "the agent is dumb" problems are tool or context problems.
- **MCP** is the standard interface for exposing tools across apps.
- **Subagents** only for contained subtasks that benefit from a fresh context and return a summary; not free-form agent "teams".
- **Evals are the core discipline.** Labeled cases, LLM-as-judge for subjective output, and tracking cost and turns per run.
- **Frameworks vs. hand-written.** SDKs (Claude Agent SDK, OpenAI Agents SDK) package the loop, tools, permissions and context management; a hand-written loop is ~100 lines.

## Constraints

- Never name an audited real site in tracked files: docs, OpenSpec artifacts, commit messages, fixtures or test names. Fixtures use `example.hu` or `.example` domains. Notes that need real names go in the gitignored `private/` folder; run output stays in the gitignored `snapshots/`.
- Contacting site owners has EU legal limits (GDPR, consent rules for direct email marketing). Do not build automated mass-email sending; any outreach is manual.
- European Accessibility Act claims must not be overstated — microenterprises are largely exempt for services.

## Spec-driven workflow (OpenSpec)

Features are planned with OpenSpec (CLI `openspec`, v1.16, schema `spec-driven`) before code is written. `openspec/specs/` holds the current source-of-truth specs; `openspec/changes/<name>/` holds in-flight changes (proposal, design, delta specs, tasks), moved to `changes/archive/` when done. Project-wide constraints for generated artifacts live in `openspec/config.yaml`.

Typical cycle: `/opsx:explore` (think) → `/opsx:propose <name>` (all artifacts at once) or `/opsx:new` + `/opsx:continue`/`/opsx:ff` → `/opsx:apply` (implement tasks) → `/opsx:verify` → `/opsx:archive`. The `.claude/commands/opsx/` and `.claude/skills/openspec-*` files are generated: change them with `openspec update`, not by hand.
