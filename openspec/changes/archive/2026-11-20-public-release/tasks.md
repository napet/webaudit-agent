# Tasks

`internal/agent/loop.go` is not touched by this change. Every task is [Claude] unless marked [You].

## 1. Private content

- [x] 1.1 Add `private/`, `.env*` and `.claude/settings.local.json` to `.gitignore`; move the idea assessment into `private/`.
- [x] 1.2 Write `private/ROADMAP.md` with the roadmap, build order, product framing and the "point the user back" triggers; cut CLAUDE.md to what is built, with a pointer to `private/ROADMAP.md`.
- [x] 1.3 Delete the live-run sections from the archived agent-loop-v2 design; add the no-audited-site-names rule to `openspec/config.yaml` and CLAUDE.md.
- [x] 1.4 Move fixture emails to `.example` domains and drop the "modelled on a real site" comment; regenerate golden files if they change.
- [x] 1.5 Remove `.claude/skills/grill-me/`, `.agents/` and `skills-lock.json`, and their CLAUDE.md note.

## 2. Public-repo basics

- [x] 2.1 Add `LICENSE` (MIT) and `THIRD_PARTY_NOTICES.md` (axe-core 4.10.2, MPL-2.0).
- [x] 2.2 Change the module path to `github.com/napet/webaudit-agent` and update all imports.
- [x] 2.3 Rewrite the README as a showcase: what it teaches, "How this was built", full code layout, Go 1.27.3, test flags.
- [x] 2.4 Add `.github/workflows/ci.yml`: vet, golangci-lint, `go test -short ./...`.

## 3. Lint

- [x] 3.1 Rebuild golangci-lint with Go 1.27.3 and add `.golangci.yml` (standard linters, errcheck, revive exported docs).
- [x] 3.2 Fix all findings outside `loop.go` (stop and ask at 50 or more).

## 4. Tidy-ups

- [x] 4.1 Fix stale docs: agent README and `loop_test.go` "not implemented", duplicated comment in `audit.go`, `--prompts` help text.
- [x] 4.2 Add the missing doc comments on exported identifiers.
- [x] 4.3 Stop hiding real failures: stdout write in `cmd/audit`, non-missing screenshot read errors in `snapshot`, `json.Marshal` in `assess`, the goquery parse in `agent/tools.go`.
- [x] 4.4 Move the shared text and HTML helpers (`squash`, truncation, goquery parsing) into one place.
- [x] 4.5 Add `assess.RubricFile` and one `MaxTokens`; build screenshot-size text from the `capture` constants.
- [x] 4.6 Remove the duplicated assessment step in `Pipeline.Run` and `Replay`.
- [x] 4.7 Use the `optional` helper consistently in `snapshot.Read`/`readAgent`; add snapshot round-trip tests.
- [x] 4.8 Split `render` and `listen` in `capture/chrome.go`.
- [x] 4.9 Call the non-agent pipeline "baseline" in flag help, docs and comments.

## 5. Verify

- [x] 5.1 `gofmt`, `go vet`, golangci-lint and `go test ./...` (with Chrome) pass.
- [x] 5.2 Grep the tree for every audited domain and the owner's personal identifiers (the list is in `private/ROADMAP.md`): no hits outside `private/`, `snapshots/` and the in-flight `agent-live-run-fixes`, which is deleted after its last task.

## Workflow follow-up

- [You] Run task 4.1 of `agent-live-run-fixes`; then archive it and delete its archived folder. Done on 2026-11-20: its specs are merged into `openspec/specs/`.
- Create the single-commit GitHub repo (ask before pushing); archive the GitLab repo. The branch is prepared in Phase 3; the push and the GitLab archive are done by the owner.
