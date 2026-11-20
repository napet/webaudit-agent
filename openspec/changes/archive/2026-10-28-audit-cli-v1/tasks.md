# Tasks

## 1. Scaffolding

- [x] 1.1 Initialize the Go module, create the directory layout from design.md, add `snapshots/` to `.gitignore`, and verify `go build ./...` succeeds
- [x] 1.2 Add dependencies (`chromedp`, `anthropic-sdk-go`, `errgroup`, a YAML parser) and verify `go mod tidy` and `go build ./...` succeed
- [x] 1.3 Add a GET/HEAD-only HTTP client for checks with a test proving it refuses POST, PUT and other methods; verify `go test ./internal/...` passes

## 2. Report contract and CLI skeleton

- [x] 2.1 Define the report types and schema version in `internal/report` (tier, score, sub-scores, flags, check results with evidence, legal gaps, assessment, notes, cost, duration) and verify a golden-file test of the JSON shape passes
- [x] 2.2 Implement `cmd/audit` flag parsing (URL argument, `--out`, `--locale`, `--model`, `--from-snapshot`, `--reassess`), scheme defaulting and usage errors; verify tests for no-argument, missing-API-key and unknown-locale exit codes pass
- [x] 2.3 Load `config/locales/hu.yaml` and `config/scoring.yaml` with validation; verify a test fails cleanly on a malformed file
- [x] 2.4 Document build prerequisites (Go, Chrome, environment variables) and basic usage in `README.md`, and verify the documented build command runs

## 3. Site capture

- [x] 3.1 Implement the chromedp page load with settle timeout, recording final URL, status, load time, console errors, cookies and request log; verify against an `httptest` fixture site, including a never-settling page that must not hang
- [x] 3.2 Implement desktop (1280×800) and mobile (390×844, emulated) viewport-only screenshots without interacting with the page; verify the PNG dimensions in a test
- [x] 3.3 Implement the trimmed DOM summary with its size limit; verify with a large fixture page
- [x] 3.4 Implement rule-based contact page and legal-information page discovery from the locale config (link match first, then guessed paths); verify the link, guessed-path and not-found scenarios against fixtures

## 4. Deterministic checks

- [x] 4.1 Define the CheckResult type (statuses `pass`, `fail`, `suspicious`, `not_applicable`, `error`, evidence, explanation) and verify that an erroring check contributes nothing to flags in a unit test
- [x] 4.2 Implement the HTTPS check (scheme, HTTP→HTTPS redirect, certificate validity, expiry and host match) and verify with `httptest` TLS servers, including an expired certificate
- [x] 4.3 Implement the site-error-or-slow check (5xx, unreachable, load time over 10 s) and verify with fixtures
- [x] 4.4 Implement the PSI client (mobile strategy, four categories, field data optional, timeout) and verify against recorded PSI JSON fixtures, including one without field data
- [x] 4.5 Implement the mobile usability check (viewport meta, horizontal overflow in the mobile render, PSI tap-target and font-size audits) and verify with fixtures
- [x] 4.6 Embed a pinned `axe.min.js`, run it in the loaded page and group violations by impact; verify against a fixture with known violations
- [x] 4.7 Implement staleness signals (copyright year, jQuery below 3.0, table layout, obsolete embeds, missing HTML5 doctype) and verify each with a fixture
- [x] 4.8 Implement the passive contact-form check (definitely broken, suspicious, working-looking, no form) and verify the 404-action, no-handler, suspicious and never-submitted scenarios; the never-submitted test asserts the fixture server received no POST
- [x] 4.9 Implement the `hu` legal-gap checks (provider details, privacy notice when a form exists, tracking before consent) worded as "could not find X" with the pages searched; verify with fixtures that they never raise hard flags
- [x] 4.10 Run the PSI request concurrently with the in-page checks and verify a PSI failure leaves the other results intact

## 5. Datedness assessment

- [x] 5.1 Write `prompts/datedness-rubric.v1.md` with anchor descriptions for ratings 1–5 and verify it loads with its version recorded
- [x] 5.2 Implement the assessment call with the strict `submit_assessment` tool (screenshots, DOM summary, compact check results), schema validation and one retry; verify with a fake client covering valid output, invalid-then-valid, no tool call, refusal, and invalid twice
- [x] 5.3 Record model ID, token counts and estimated cost from configured prices; verify in a unit test
- [x] 5.4 Run one opt-in live test against the real API (skipped without `ANTHROPIC_API_KEY`) on a fixture page and verify the rating is in range and reasons are present

## 6. Scoring

- [x] 6.1 Implement tier assignment from hard flags and verify one-flag and no-flag scenarios
- [x] 6.2 Implement sub-scores (0 good to 100 bad) for each area including datedness 1→0 and 5→100, and verify with table-driven tests
- [x] 6.3 Implement the weighted score with weights from config, renormalization for missing sub-scores and the missing list; verify that the same inputs always give the same output

## 7. Snapshots

- [x] 7.1 Write the snapshot directory after every live run and record its path in the report; verify the files listed in design.md exist after an end-to-end fixture run
- [x] 7.2 Implement `--from-snapshot` replay that makes no site or PSI request and reuses the cached assessment unless `--reassess` is given; verify by replaying with the network-facing clients replaced by fakes that fail on any call
- [x] 7.3 Verify that changing weights in config and replaying changes the score without an LLM call
- [x] 7.4 Document snapshot replay in `README.md` and verify the documented command runs against a fixture snapshot

## 8. Integration

- [x] 8.1 Run the full pipeline end to end against each fixture site with fake PSI and LLM clients and verify each report matches its expected tier and flags
- [x] 8.2 Audit two or three real Hungarian service-business sites by hand, read the reports for wrong or overstated findings, and note any rule changes needed in the locale config

## Workflow follow-up

- Run `/opsx:verify`, then `/opsx:archive` once review is done.
- Next change: the labeled eval set and `audit eval` command built on these snapshots.
