# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Anthropic (Claude) `strategy.Patcher` implementation
  (`internal/strategy/anthropic.go`): the AI path now proposes real unified
  diffs via the Claude Messages API when `ANTHROPIC_API_KEY` is set. Configurable
  with `POULTICE_AI_MODEL` (default `claude-sonnet-5`) and `ANTHROPIC_BASE_URL`.
  Standard-library only — no new dependencies. Every generated patch still passes
  `git apply --check`, policy, and verification before it can survive.
- `ROADMAP.md` describing the path to 1.0.

### Changed
- `poultice heal` selects the Anthropic provider automatically when a key is
  present; `--no-ai` and unauthenticated runs are unchanged.

## [0.0.1] - 2026-08-11

Initial release. The deterministic healing path is complete and verified
end-to-end; the AI path is plumbed through the engine but has no provider yet
and reports `skipped: no AI provider configured`.

### Added
- Verification-gated engine: changes are kept only when a recipe's verifier
  passes, otherwise the working tree and `HEAD` are rolled back byte-for-byte.
- CLI (`cmd/poultice`): `heal`, `diagnose`, `recipes`, and `validate`
  subcommands with `--dir`, `--recipes`, `--recipe`, `--severity`, `--no-ai`,
  `--dry-run`, `--json`, and `--pr-body` flags.
- Recipe schema and validator with blast-radius policy enforcement.
- Native (deterministic) fix strategy plus the `strategy.Patcher` interface for
  AI-backed strategies, defaulting to a `Disabled` patcher.
- Five finding parsers (Go, ruff, Snyk, and generic) and a findings model.
- Git checkpoint/rollback, a process-group-aware command runner, and terminal /
  JSON / PR-body reporters.
- Zero third-party dependencies, including an in-repo YAML decoder
  (`internal/yaml`) covering the subset of YAML recipes use.
- Three starter recipes: Go formatting, Maven Snyk CVE remediation, Python ruff.
- CI workflow with a dogfood job that runs poultice against its own source.

[Unreleased]: https://github.com/adarshks016/poultice/compare/v0.0.1...HEAD
[0.0.1]: https://github.com/adarshks016/poultice/releases/tag/v0.0.1
