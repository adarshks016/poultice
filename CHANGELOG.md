# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `.poultice.yaml` configuration (`severity`, `recipesDir`, `only`, `skip`,
  `noAI`, `prBody`, `ai.model`), discovered at the repository root or named
  with `--config`. Flags always win; unknown keys are rejected with a line
  number. `poultice init` writes a commented starter file and never overwrites.
- `--recipe` accepts a comma-separated list; new `--skip` excludes recipes.
  Naming a recipe that does not exist is an error.
- The recipe library is embedded in the binary. Without `--recipes` or a
  repository `recipes/` directory, poultice uses the built-in library, so a
  `go install`ed binary works anywhere. `--recipes builtin` forces it.
- `go-mod-tidy` recipe and `go-mod-tidy-diff` parser (`go mod tidy -diff`,
  Go 1.23+). Tidy failures are surfaced as errors rather than read as clean.
- `gradle-snyk-cve` recipe: Snyk diagnosis, AI-only fix limited to build files
  and the version catalog, verified through the project's Gradle wrapper.
- End-to-end suite (`internal/e2e`) running the shipped recipes, unmodified,
  against fixture repositories — real Go toolchain for Go recipes, shell
  stand-ins emitting real JSON for Snyk, Maven, ruff and Python.
- GitHub Action: `skip`, `working-directory` and `fail-on` inputs; `exit-code`
  and `report` outputs; the PR body is written to the job summary.
- `goreleaser` config and a tag-triggered release workflow.
- Docs: [first-recipe walkthrough](docs/first-recipe.md) and
  [configuration reference](docs/configuration.md).
- Engine-level golden tests for the AI path (`internal/engine/engine_ai_test.go`):
  accept on verify-pass, rollback on verify-fail, bounded retry (fail then
  succeed), and not-configured skip — all exercised against a real git repo in a
  temp directory with a `fakePatcher` standing in for any live model.
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
- The GitHub Action builds poultice from its own checkout instead of
  `go install …@latest`, so a pinned tag runs exactly that version and its
  recipes. It reuses the runner's Go when present.

### Fixed
- Files written by a verify step (bytecode caches, build output, logs) were
  committed in the checkpoint without ever passing policy. The engine now
  stages the strategy's changes before the policy check and commits exactly
  that snapshot; verifier byproducts are discarded.
- A staged rename was reported by its destination only, so moving a file out
  of a denied path such as `.github/**` escaped the policy check. Both paths
  are now checked.
- A failed checkpoint commit left unverified changes in the working tree; it
  now rolls back.
- The GitHub Action interpolated inputs directly into its shell script; they
  now pass through the environment.
- Recipe examples in the README and docs used inline lists (`files: [a]`),
  which the bundled YAML decoder rejects. They now use block style.

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
