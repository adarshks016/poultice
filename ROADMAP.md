# Roadmap

Poultice v0.0.1 ships a complete **deterministic** healing path: recipes run
native fixers, findings are re-diagnosed, and changes are kept only when a
verifier passes — otherwise the working tree and `HEAD` are rolled back
byte-for-byte. The **AI** path is fully plumbed through the engine (the
`strategy.Patcher` interface, context collection, `git apply --check`, policy
enforcement, bounded retries). Its first provider — Anthropic (Claude) — is now
implemented; without a key, or with `--no-ai`, it still reports
`skipped: no AI provider configured`.

This document tracks what comes next.

## Phase 1 — Make the AI path real

The headline feature. Everything else is polish on top of a working tool.

- [x] `internal/strategy/anthropic.go` — a `Patcher` backed by the Claude
      Messages API. Reads `ANTHROPIC_API_KEY`, sends a `PatchRequest`
      (outstanding findings + trimmed file contents + policy) and parses a
      unified diff back. Standard-library only.
- [x] Provider selection in `cmd/poultice`, auto-enabled from the environment
      and defaulting to `Disabled` so `--no-ai` and no-credential runs stay
      unchanged.
- [x] Provider tests over `httptest` — request shape, diff extraction, API-error
      surfacing, and the env fallback — with no network access.
- [x] Engine-level golden tests using a fake `Patcher`: accept on verify-pass,
      rollback on verify-fail, bounded retry (fail→succeed), and skip when not
      configured — all against a real git repo, no network.

## Phase 2 — Prove it on real repositories

- [ ] End-to-end fixtures: a repo with a real CVE (maven-snyk-cve) and one with
      a lint break (python-ruff), asserting `HEALED` vs `UNVERIFIED` outcomes.
- [ ] More parsers — npm audit, govulncheck, semgrep — each a small
      `internal/parse/*.go` plus a table-driven test.

## Phase 3 — Shippable

- [ ] `goreleaser` config: cross-platform binaries and a GitHub release on tag.
- [ ] `action.yml` wrapper so poultice drops into any project's CI.
- [ ] Docs pass: a worked "write your first recipe" walkthrough.

## Phase 4 — Nice to have

- [ ] More recipes: gradle, cargo, `go mod tidy`.
- [ ] Config file (`.poultice.yaml`) so flags need not live on the command line.
