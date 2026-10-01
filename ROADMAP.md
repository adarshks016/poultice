# Roadmap

Poultice ships a complete verification-gated healer: nine recipes across Go,
Java, Node, Python and Rust; ten parsers; deterministic fixers first and Claude
for the residue; and every change kept only if the recipe's verifier passes.
Otherwise the working tree and `HEAD` are rolled back byte-for-byte.

Phases 1–4 below are done. Phase 5 is what comes next.

## Phase 1 — Make the AI path real ✅

- [x] `internal/strategy/anthropic.go` — a `Patcher` backed by the Claude
      Messages API. Standard-library only.
- [x] Provider selection in `cmd/poultice`, auto-enabled from the environment
      and defaulting to `Disabled` so `--no-ai` and no-credential runs stay
      unchanged.
- [x] Provider tests over `httptest`, with no network access.
- [x] Engine-level golden tests using a fake `Patcher`: accept, rollback,
      bounded retry, and skip — against a real git repo.

## Phase 2 — Prove it on real repositories ✅

- [x] End-to-end suite (`internal/e2e`) that runs the shipped recipes,
      unmodified, against fixture repositories: Log4Shell in a Maven project
      (`HEALED`, and `UNVERIFIED` with a byte-for-byte rollback when the
      project's own tests reject the upgrade), an unused import (python-ruff),
      formatting on a green and a red suite, and an untidy `go.mod`.
- [x] More parsers — npm audit, govulncheck, semgrep, cargo audit,
      `go mod tidy -diff` — each with a table-driven test.

## Phase 3 — Shippable ✅

- [x] `goreleaser` config: cross-platform binaries and a GitHub release on tag.
- [x] `action.yml`: builds poultice from the pinned action ref, so a tag runs
      exactly that version and its recipes; `fail-on` threshold, job summary,
      injection-safe input handling.
- [x] Docs: a worked [first-recipe walkthrough](docs/first-recipe.md) and a
      [configuration reference](docs/configuration.md).

## Phase 4 — Nice to have ✅

- [x] More recipes: `gradle-snyk-cve`, `cargo-vuln`, `go-mod-tidy`.
- [x] `.poultice.yaml` config file, plus `poultice init`.
- [x] The recipe library is embedded in the binary, so `go install` works in
      any repository.

### Hardening found along the way

- [x] A checkpoint commits exactly the snapshot policy measured. Files a
      verifier writes are discarded instead of riding along unchecked.
- [x] Renames count against both their source and destination paths, so a
      file cannot be moved out of a denied directory.

## Phase 5 — Next

- [ ] `poultice pr` — push the branch and open the pull request directly, as a
      draft when the outcome is `UNVERIFIED`.
- [ ] Fingerprint state file, so a weekly schedule stops reopening an
      identical pull request.
- [ ] SARIF output for GitHub code scanning.
- [ ] Further `strategy.Patcher` providers: OpenAI-compatible endpoints, Ollama.
- [ ] GitLab CI template.
- [ ] Recipes: `eslint --fix`, `golangci-lint`, Docker base image bumps,
      `javax` → `jakarta`, CI hygiene (unpinned actions, missing timeouts).
- [ ] **Benchmark harness** — a fixture corpus of broken builds with a public
      scoreboard: fix rate, false-accept rate, token cost per fix, and the
      deterministic-vs-AI split.
