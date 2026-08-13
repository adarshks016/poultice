# poultice

**Automated repository healing that only keeps what it can prove.**

Poultice finds problems in a repository, tries to fix them, and then — this is
the entire point — *verifies the fix and throws it away if it does not hold up*.
A change that cannot pass the recipe's own verification never reaches a branch.

```
detect ─▶ diagnose ─▶ native fix ─▶ verify ─┬─ pass ─▶ commit checkpoint
                                            │
                                            └─ fail ─▶ AI patch (n ≤ N)
                                                         │
                                                       verify
                                                         │
                                              ┌──────────┴──────────┐
                                            pass                  fail
                                              │                     │
                                        commit checkpoint    roll back to
                                                             last green commit
```

Most "AI fixes your CI" tools pipe an error log into a model and commit whatever
comes back. Poultice inverts the trust model: **the model is a suggestion engine
and the build is the judge.** Everything else here follows from that.

---

## Why this exists

The prototype this grew out of was a GitHub Actions workflow that ran Snyk,
asked GPT-4o to fix whatever Snyk could not, and pushed the result. It had the
failure modes that approach always has:

| Prototype behaviour | What poultice does instead |
|---|---|
| Overwrote whole files with model output capped at 3000 tokens, silently truncating anything longer | Only accepts **unified diffs**, validated with `git apply --check` before touching the tree |
| Committed AI edits even when the build stayed broken | **Rolls back** to the last verified-green commit |
| `find . -name "*.java" \| xargs git add` | **Blast-radius policy**: allow/deny globs, file and line caps, enforced on every patch |
| Called a model before trying the tool's own fixer | **Deterministic strategies always run first**; the model only sees the residue |
| Re-derived findings with `grep -oP` on Maven output | **Structured parsers** normalized to one `Finding` type |
| Secrets pasted into `env:` | Credentials only ever come from the environment; see [SECURITY.md](SECURITY.md) |

## Install

```bash
go install github.com/adarshks016/poultice/cmd/poultice@latest
```

Or build from source — poultice has **zero third-party dependencies**, so this
works offline with nothing but a Go toolchain:

```bash
git clone https://github.com/adarshks016/poultice
cd poultice && make build      # ./bin/poultice
```

## Try it in 30 seconds

```bash
# What would poultice do here? Changes nothing.
poultice diagnose --severity low

# Which recipes apply to this repo, and why not?
poultice recipes

# Heal, using deterministic fixers only — no model, no API key, no network.
poultice heal --severity low --no-ai
```

Running it on this repository:

```
▸ go-formatting (go)
  diagnose: 1 finding(s) at severity >= low
  strategy gofmt-write: running
  verify: running 2 step(s)
  verify: passed

  HEALED      go-formatting
  findings: 1 before → 0 after (1 resolved)
  · gofmt-write             accepted — gofmt-write completed with exit code 0
  green: 4f2a9c11
  took 1.2s
```

## Recipes

A recipe is data, not a script. It declares how to detect that it applies, how
to diagnose problems, which strategies may fix them, and how to prove the fix
worked.

```yaml
apiVersion: poultice.dev/v1
kind: Recipe

metadata:
  name: maven-snyk-cve
  ecosystem: java

detect:
  files: ["**/pom.xml"]
  requires: [mvn, snyk]

diagnose:
  run: snyk test --all-projects --json-file-output=$POULTICE_OUT
  parse: snyk-json
  expectNonZeroExit: true

fix:
  - strategy: native            # free, deterministic, tried first
    name: snyk-fix
    run: snyk fix --all-projects

  - strategy: ai                # only sees what native could not fix
    name: unfixable-dependency-bumps
    policy:
      allowPaths: ["**/pom.xml"]    # a dep CVE is fixed in a pom, never in sources
      maxChangedFiles: 10
      maxChangedLines: 200

verify:                         # REQUIRED — a recipe without this is rejected
  - name: compile
    run: mvn -B -q clean install -DskipTests
  - name: test
    run: mvn -B test
```

Two rules are enforced by the loader, not by convention:

1. **No verify block, no recipe.** `poultice validate` fails it. An unverifiable
   fix is not a fix.
2. **Native strategies must precede AI strategies.** Cheap and deterministic
   before expensive and probabilistic.

Shipped recipes live in [`recipes/`](recipes/): `go-formatting`, `python-ruff`,
`maven-snyk-cve`. Writing your own is [documented here](docs/writing-recipes.md).

## Exit codes

Poultice is built to be a CI gate, so the exit code is the contract:

| Code | Meaning |
|------|---------|
| `0` | Clean, or fully healed and verified |
| `2` | Partially healed — verified, but findings remain |
| `3` | Unverified — fixes failed verification and were rolled back |
| `1` | The run itself failed |

## Safety properties

These are tested, not aspirational — see
[`internal/engine/engine_test.go`](internal/engine/engine_test.go).

- **Nothing unverified survives.** Failed verification triggers `git reset --hard`
  to the last green commit plus `git clean -fd`.
- **Your uncommitted work is never touched.** Poultice refuses to run on a dirty
  working tree rather than risk destroying changes it did not make.
- **The AI can never edit CI config or secrets.** `.github/**`, `Jenkinsfile`,
  `**/*.pem`, `**/.env`, `**/settings.xml` and friends are denied by default in
  every recipe, and a recipe cannot opt out.
- **Patches must apply cleanly or be discarded.** No whole-file overwrites, ever.
- **`--no-ai` is a real mode.** Every deterministic strategy works with no model,
  no key and no network, which also makes poultice safe to run on fork PRs.

## Status

**v0.0.1.** The engine, recipe schema, policy enforcement, parsers, rollback and
reporting are implemented and tested. The AI strategy path is fully wired through
the engine — context collection, patch validation, policy checks, bounded
retries — behind the `strategy.Patcher` interface.

The first provider, **Anthropic (Claude)**, is now implemented. Set
`ANTHROPIC_API_KEY` and `heal` will ask the model for a unified diff when native
strategies leave findings behind; every generated patch still passes through
`git apply --check`, policy, and verification before it can survive. Without a
key — or with `--no-ai` — the AI path reports `skipped: no AI provider configured`
and the deterministic half runs unchanged. Optional overrides:
`POULTICE_AI_MODEL` (default `claude-sonnet-5`) and `ANTHROPIC_BASE_URL`.

### Roadmap

- [x] `strategy.Patcher` implementation: Anthropic (Claude)
- [ ] Further `strategy.Patcher` implementations: OpenAI-compatible, Ollama
- [ ] `poultice pr` — open the pull request directly, draft when unverified
- [ ] SARIF output for GitHub code scanning ingestion
- [ ] GitHub Action wrapper (`action.yml`) and a GitLab CI template
- [ ] Finding-fingerprint state file, so a weekly schedule stops reopening the
      same pull request
- [ ] More recipes: flaky-test quarantine, `javax`→`jakarta` migration, Docker
      base image bumps, CI hygiene
- [ ] **Benchmark harness** — a fixture corpus of broken builds with a public
      scoreboard: fix rate, false-positive rate, token cost per fix,
      deterministic-vs-AI split

## Contributing

New recipes are the most useful contribution, and they are data — no Go required
unless your tool needs a new parser. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
