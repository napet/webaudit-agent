# Design

## Context

Greenfield repo: this change adds the first code. The tool is a single Go binary run by hand on one URL at a time. Chrome or Chromium is installed locally; API keys come from the environment (`ANTHROPIC_API_KEY`, `PSI_API_KEY`). Requirements are in `specs/`; motivation in proposal.md.

## Goals / Non-Goals

**Goals:**
- A pipeline whose stages (capture → checks → assess → score → report) have plain Go interfaces, so v2 can turn `inspect_page` and `check_form` into agent tools without rewriting them.
- Every input needed to recompute a report is on disk after a live run (snapshot), so later evals measure code and prompt changes, not website changes.
- Tests that run offline against local fixture sites.

**Non-Goals:**
- Concurrency across many URLs, rate limiting across runs, or a job queue.
- Any persistence beyond snapshot directories and the JSON report.
- Making the LLM call through an agent framework.

## Decisions

### Module layout
```
cmd/audit/            CLI entry point, flag parsing
internal/capture/     chromedp page load, screenshots, DOM summary, contact page discovery
internal/checks/      one file per check; each returns a CheckResult
internal/psi/         PageSpeed Insights client
internal/assess/      Anthropic call, rubric loading, schema validation
internal/score/       sub-scores, tiers, weights
internal/report/      report types (the JSON contract) and schema version
internal/snapshot/    write and replay snapshot directories
config/locales/hu.yaml, config/scoring.yaml, prompts/datedness-rubric.v1.md
testdata/sites/       static fixture sites served by httptest
```
`internal/` keeps the API private until something outside needs it. Alternative: one flat package; rejected because v2 needs the checks callable on their own.

### Pipeline shape
`main` runs capture, then starts the PSI request and the in-page checks concurrently (`errgroup`), then the assessment, then scoring. Each stage takes an explicit input struct and returns an output struct that is also what gets written to the snapshot. Replay loads those structs from disk and skips the stages that would touch the network. *Teaches:* designing stage boundaries so the same data feeds live runs, replays and (later) agent tool results.

### Headless browser: chromedp
`chromedp` drives a local Chrome over the DevTools protocol from Go: one browser, and each page load (desktop homepage, emulated mobile homepage, contact page, legal page) in its own fresh browser context, so cookies from one load can't leak into the next. It records console messages, cookies and the request log through DevTools events. Alternatives: Playwright (needs Node or a Go port with heavier setup) and Rod (similar to chromedp, smaller community). chromedp keeps the binary pure Go.

### axe-core injected, not run through Node
A pinned `axe.min.js` is embedded with `go:embed` and evaluated in the loaded page; `axe.run()` results are parsed from JSON. No Node toolchain. The pinned version is recorded in the report.

### Lighthouse through the PageSpeed Insights API
One GET to the PSI v5 `runPagespeed` endpoint with `strategy=mobile` and all four categories. It runs concurrently with the chromedp pass because it takes 15–30 s. Its tap-target and font-size audits feed the mobile usability flag; `loadingExperience` provides field data when present.

### LLM call: official Go SDK, a strict tool for structured output
The assessment uses `anthropic-sdk-go` for transport and types, with a single tool `submit_assessment` whose input schema is the assessment (rating 1–5, reasons, confidence), marked `strict: true` so its input always matches the schema. Current models (Opus 5.5, Sonnet 5.5) reject forced `tool_choice` with a 400, so `tool_choice` is `auto` with parallel calls disabled, and the prompt tells the model to finish by calling the tool. A reply without the tool call, or with input that fails Go-side validation, counts as invalid and is retried once. Default model `claude-opus-5-5` at explicit effort `medium` (thinking is always on for it), overridable by flag. Requests go through the beta Messages endpoint with the server-side refusal fallback (`fallbacks: "default"`), so a safety-classifier decline is re-served by another model instead of failing the assessment; a remaining `refusal` stop reason marks the assessment failed with its category. Per-model prices live in `config/scoring.yaml` so cost estimates are not hardcoded.
*Teaches:* tool schemas, `tool_use` blocks, stop reasons and validation are the exact pieces the v2 loop is built from, so v1 is a one-turn version of it. Alternative: structured outputs (`output_config.format`) would guarantee JSON with no retry path, but teaches nothing about tool calls that carries to v2. Alternative: free-text JSON in the reply; rejected as fragile. Alternative: raw `net/http` instead of the SDK; possible, but the learning is in the message and tool flow, not in HTTP plumbing.

### Images sent to the model
Both screenshots go as PNG image blocks with the DOM summary and a compact JSON of check results (statuses and short evidence, not raw axe or PSI dumps), to keep tokens and cost low.

### Locale and scoring config as YAML
`config/locales/hu.yaml` holds contact page paths (`/kapcsolat`, `/elerhetoseg`, `/impresszum`, …), link-text keywords, the provider-detail patterns (address, `Cg.` registration number, tax number formats) and privacy notice keywords. `config/scoring.yaml` holds sub-score weights and model prices. Adding a market means adding a file. Alternative: Go constants; rejected because weights are meant to be tuned between eval runs.

### Contact-form classification
Parse forms from the rendered DOM. Resolve the action URL; issue HEAD, then GET if HEAD is not allowed. 404/410/unresolvable host or no action plus no handler (checked through DevTools `getEventListeners`: a submit listener on the form or an ancestor, or a click listener on any button inside the form, since many forms send from `<input type="button" onclick>`) → definitely broken. A form with a text area is never treated as a search form. Console errors, failed form scripts, or deprecated services → suspicious. The HTTP client used by checks has a guard that refuses any method other than GET or HEAD and caps requests at 40 per audit, so a bug can't POST to a business. The guard and cap cover the tool's own requests: Chrome's page loads are a fixed handful, and requests made by the audited page's own scripts (analytics beacons, for example) are the site's behaviour, recorded as evidence but not blocked.

### Snapshot format
`snapshots/<host>/<UTC timestamp>/` containing `meta.json` (URL, locale, time), `page.html`, `desktop.png`, `mobile.png`, `capture.json` (status, timings, console, cookies, request log), `probes.json` (TLS, plain-HTTP and form-action responses), `psi.json`, `checks.json`, `assessment.json` and `report.json`. Directory is gitignored. Replay rebuilds stage outputs from these files.

### Testing
Unit tests for each check against HTML fixtures, plus fixture sites in `testdata/sites/` (a modern page, a table-layout 2012 page, a page with a 404 form action, a page that sets a GA cookie on load) served by `httptest` for end-to-end runs with PSI and the LLM replaced by fakes behind interfaces. One opt-in test hits the real APIs when keys are set.

## Risks / Trade-offs

- [Sites block or behave differently for headless Chrome] → realistic user agent string that still names the tool; record bot-wall detections as an `error` status instead of guessing.
- [PSI quota, latency or outage] → per-request timeout; partial results per the spec; snapshot replay avoids repeat calls.
- [Hungarian legal requirements summarized wrongly] → findings are worded "could not find X", never raise flags, and patterns live in config so they can be corrected; confirm against Ekertv. and GDPR text before relying on them.
- [False "definitely broken" contact form, e.g. action handled by JS frameworks] → only HTTP-status and no-handler evidence counts as definite; everything else is suspicious. The eval set will measure precision.
- [LLM rating noise between runs] → fixed rubric and fixed effort, rating stored with rubric version; eval set measures agreement. (The API no longer accepts `temperature`, so sampling can't be pinned.)
- [Cost drift] → token counts and estimated cost in every report.

## Findings from real-site audits

Auditing four real Hungarian sites (task 8.2) found no problems with the LLM ratings, but several false or missed findings in the deterministic checks, all fixed:

- Navigation built from `<button onclick="location.href=…">` hid every link; discovery now follows onclick navigation.
- Carousel tracks (owl, swiper) counted as horizontal overflow and raised false `not_mobile_usable` flags on two of four sites; content clipped by an `overflow` ancestor no longer counts, and overflow up to 20px is only suspicious.
- A quote form with id `ajanlatkeres` matched the "keres" search heuristic and was skipped; it also sends from `<input type="button" onclick>`, which would have made it look definitely broken. Forms with a text area are never search forms, and in-form button click handlers count as submit handlers.
- None of the sites had a separate impresszum page; provider details often live in the ÁSZF or privacy notice, which are now fallback legal pages. Link keywords are now ordered by preference, and contact keywords that name a form come first.
- Tracker request evidence carried multi-kilobyte query strings into the report; it is now host and path only. (The model never saw them: the assessment receives only each check's id, status and explanation.)

## Open Questions

- Exact Lighthouse thresholds for "tap targets/font sizes failing" (use the audit's own pass/fail at first, tune with the eval set).
- Initial sub-score weights (start equal, tune after the first eval run).
