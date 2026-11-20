# audit-cli Specification

## Purpose

Provides the `audit <url>` command that turns one website URL into a single machine-readable JSON audit report, and lets a run be replayed later from a cached snapshot of its inputs.

## Requirements

### Requirement: Audit a single URL
The tool SHALL accept exactly one URL argument, audit that site, and write one JSON report to stdout, or to the file given by an output flag. A URL without a scheme SHALL be treated as `https://`.

#### Scenario: Successful audit
- **WHEN** the user runs `audit https://example.hu`
- **THEN** the tool writes a JSON report for that site to stdout and exits with code 0

#### Scenario: URL without scheme
- **WHEN** the user runs `audit example.hu`
- **THEN** the tool audits `https://example.hu`

#### Scenario: Output to file
- **WHEN** the user runs `audit https://example.hu --out report.json`
- **THEN** the report is written to `report.json` and nothing but progress or error messages goes to stdout or stderr

### Requirement: Report contract
The JSON report SHALL contain: the audited URL and final URL after redirects, timestamp, locale, report schema version, the tier, the within-tier score, every sub-score, the hard flags raised, all check results with their evidence, the possible legal gaps, the datedness assessment, notes, and per-run cost and duration.

#### Scenario: Report fields present
- **WHEN** an audit completes
- **THEN** the report includes each field listed above, and the schema version identifies the report format

#### Scenario: Evidence is attached to findings
- **WHEN** a check produces a finding
- **THEN** the finding in the report includes machine-readable evidence (for example a URL and the HTTP status it returned, or a cookie name and the request that set it)

### Requirement: Partial results on component failure
A failure in one component (PageSpeed Insights, the LLM call, or a single check) SHALL NOT abort the audit. The report SHALL mark that component as failed with the error, and the score SHALL be computed from the components that succeeded.

#### Scenario: PageSpeed Insights unavailable
- **WHEN** the PageSpeed Insights request fails or times out
- **THEN** the report contains the other check results, marks the PSI-based results as failed with the error, and the tool exits with code 0

#### Scenario: Site unreachable
- **WHEN** the site cannot be loaded at all (DNS failure, connection refused, or HTTP 5xx on the homepage)
- **THEN** the report raises the site-error hard flag, skips the checks that need a loaded page, and the tool exits with code 0

### Requirement: Usage errors
The tool SHALL exit with a non-zero code and a message on stderr when it is invoked incorrectly or required configuration is missing.

#### Scenario: Missing URL argument
- **WHEN** the user runs `audit` with no argument
- **THEN** the tool prints usage to stderr and exits with a non-zero code

#### Scenario: Missing API key
- **WHEN** the Anthropic or PageSpeed Insights API key is not set in the environment and the run is not a snapshot replay
- **THEN** the tool names the missing environment variable on stderr and exits with a non-zero code without contacting the site

### Requirement: Snapshot capture
Every live audit SHALL save a snapshot of its inputs: fetched HTML, screenshots, the PageSpeed Insights response, raw check results, and the HTTP responses gathered by checks, in a directory identified by site and timestamp.

#### Scenario: Snapshot written
- **WHEN** a live audit of `https://example.hu` completes
- **THEN** a snapshot directory for that site and run exists containing the inputs listed above, and the report records its path

### Requirement: Snapshot replay
The tool SHALL re-audit from a snapshot when given a snapshot path, without making any request to the audited site or to PageSpeed Insights.

#### Scenario: Replay from snapshot
- **WHEN** the user runs `audit --from-snapshot <dir>`
- **THEN** the tool produces a report from the snapshot's inputs and makes no request to the site or to PageSpeed Insights

#### Scenario: Replay reuses the cached assessment
- **WHEN** the user replays a snapshot without asking to re-run the assessment
- **THEN** the cached datedness assessment is reused and no LLM call is made

#### Scenario: Replay with fresh assessment
- **WHEN** the user replays a snapshot with the flag to re-run the assessment
- **THEN** the tool makes a new LLM call from the cached inputs and reports the new assessment

### Requirement: Locale selection
The tool SHALL load locale-specific rules (contact page patterns, keywords, legal-gap checks) from a locale config, defaulting to Hungarian (`hu`). An unknown locale SHALL be a usage error.

#### Scenario: Default locale
- **WHEN** the user runs an audit without a locale flag
- **THEN** the `hu` locale config is used and the report records `hu`

#### Scenario: Unknown locale
- **WHEN** the user passes a locale with no config file
- **THEN** the tool exits with a non-zero code naming the missing locale

### Requirement: Opt-in agent mode
The tool SHALL run in agent mode only when given the agent flag. Without the flag, a live audit and a replay SHALL behave exactly as before this change. Replaying an agent snapshot SHALL use agent replay whether or not the flag is given.

#### Scenario: Default mode unchanged
- **WHEN** the user runs `audit https://example.hu` without the agent flag
- **THEN** the audit makes one assessment call, inspects no pages beyond the rule-based ones, and the report has no agent section

#### Scenario: Agent mode
- **WHEN** the user runs `audit --agent https://example.hu`
- **THEN** the audit runs the v1 pipeline followed by the agent, and the report contains the agent section

#### Scenario: Agent flag with a snapshot
- **WHEN** the user replays a snapshot that was not recorded in agent mode and passes the agent flag
- **THEN** the tool exits with a usage error explaining that the snapshot has no agent recording
