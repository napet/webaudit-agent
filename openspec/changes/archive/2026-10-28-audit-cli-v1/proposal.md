# Proposal

## Why

The project has no code yet, and every later piece (eval set, agent loop, dashboard, owner reports) needs one thing first: a repeatable way to turn a URL into a structured, evidence-backed judgment of how outdated a small-business site is. A single `audit <url>` CLI producing a JSON report is the smallest slice that exercises the whole idea end to end, and it gives the v2 agent loop a baseline to beat.

## What Changes

- New Go command-line tool `audit <url>` that writes a JSON audit report to stdout (or a file).
- Loads the homepage in headless Chrome, takes above-the-fold screenshots at a desktop and a mobile viewport, and builds a trimmed DOM summary.
- Finds the contact page with rule-based guessing (Hungarian URL patterns and link text, from a per-locale config). No LLM-driven exploration in v1.
- Runs deterministic checks: HTTPS/certificate, PageSpeed Insights (Lighthouse lab scores plus real-user field data), axe-core accessibility (injected through `chromedp`), mobile usability, staleness signals (copyright year, old jQuery, table layouts), passive contact-form checks, and Hungarian "possible legal gaps" (impresszum, privacy notice, cookie consent).
- Makes one structured LLM call (Anthropic API, hand-written client code in Go) that rates visual datedness 1–5 against a written rubric, with reasons, given the screenshots, DOM summary and check results.
- Combines results into a two-tier ranking: any hard-failure flag puts the site in the top tier; a weighted score computed in Go ranks sites within each tier.
- Caches each run's inputs (HTML, screenshots, PSI response, check results) as a snapshot so a later run can re-score from the snapshot without touching the live site.

Out of scope for this change: the `audit eval` command and the labeled eval set (next change; snapshots are built here so it can use them), the agent loop (v2), the Hungarian owner-facing report, storage, the dashboard, lead sourcing, webshop checks (ÁSZF), full-page screenshots, and any outreach.

## Capabilities

### New Capabilities
- `audit-cli`: the `audit <url>` command, its flags, the JSON report contract, and snapshot write/replay.
- `site-capture`: loading a site in headless Chrome, screenshots, DOM summary, and rule-based contact page discovery driven by locale config.
- `site-checks`: the deterministic checks, including hard-failure flags, passive contact-form detection, and possible legal gaps, each with evidence.
- `datedness-assessment`: the single LLM judgment of visual datedness on a 1–5 rubric via structured output.
- `audit-scoring`: sub-scores, hard-flag tiering, and the weighted within-tier score.

### Modified Capabilities

None (no existing specs).

## Impact

- New Go module in the repo (first code). Dependencies: `chromedp`, the Anthropic Go SDK, a YAML parser; Chrome or Chromium must be installed locally.
- External services: Google PageSpeed Insights API (free API key) and the Anthropic API (paid, about a few cents per audit). Keys come from environment variables.
- Network behavior toward audited sites: GET/HEAD requests and a headless page load only. Never submits forms or POSTs to a site.
- Legal-gap findings are worded as "could not find X" with evidence and never raise a hard flag; the Hungarian requirements behind them must be confirmed against the statute text before being relied on.
