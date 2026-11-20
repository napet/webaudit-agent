## Why

The repo is going public on GitHub as `github.com/napet/webaudit-agent`, a showcase of building an agent by hand. Today it names and judges real businesses in its docs, carries a private business plan, and lacks a license, CI and working lint.

## What Changes

- Move the idea assessment, the roadmap and the build order into a gitignored `private/` folder; cut the public docs to what is built.
- Delete the live-run sections that name and rate real sites, and add a rule that docs, commits and fixtures never name an audited site.
- Move fixture emails to `.example` domains; remove the vendored `grill-me` skill and `skills-lock.json`.
- Add an MIT `LICENSE`, `THIRD_PARTY_NOTICES.md` (axe-core, MPL-2.0), GitHub Actions CI and a golangci-lint config.
- **BREAKING (import path):** the module path becomes `github.com/napet/webaudit-agent`.
- Behaviour-preserving tidy-ups (shared helpers, doc comments, one assessment step, split long capture functions, snapshot tests), plus error handling that stops hiding real failures (stdout write, unreadable screenshots).
- Call the non-agent pipeline "baseline" in help text, docs and comments. JSON fields and the snapshot format do not change.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None: no spec-level behaviour changes (`skip_specs: true`).

## Impact

Every Go file's imports, README, CLAUDE.md, `openspec/config.yaml`, `.gitignore`, fixtures and golden files under `testdata/`. `internal/agent/loop.go` is not touched. Moving scoring thresholds into YAML is out of scope.
