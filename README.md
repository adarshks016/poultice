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

The recipe library is compiled into the binary, so this is all you need in any
repository. Pre-built binaries for Linux, macOS and Windows are attached to
each [GitHub release](https://github.com/adarshks016/poultice/releases).

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
  files:
    - "**/pom.xml"
  requires:
    - mvn
    - snyk

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
      allowPaths:                 # a dep CVE is fixed in a pom, never in sources
        - "**/pom.xml"
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

### Shipped recipes

| Recipe | Ecosystem | Diagnose | Fix |
|---|---|---|---|
| `go-formatting` | Go | `gofmt -l` | `gofmt -w` |
| `go-mod-tidy` | Go | `go mod tidy -diff` | `go mod tidy` |
| `go-vuln` | Go | `govulncheck` | `go get -u`, then AI |
| `maven-snyk-cve` | Java | Snyk | `snyk fix`, then AI (`pom.xml` only) |
| `gradle-snyk-cve` | Java | Snyk | AI (build files only) |
| `npm-vuln` | Node | `npm audit` | `npm audit fix`, then AI |
| `python-ruff` | Python | `ruff check` | `ruff --fix`, then AI |
| `cargo-vuln` | Rust | `cargo audit` | `cargo update`, then AI |
| `semgrep` | multi | `semgrep` | `semgrep --autofix`, then AI |

Each runs only where its `detect` block matches and its tools are installed;
`poultice recipes` explains why any recipe was skipped. To write your own, start
with the [first-recipe walkthrough](docs/first-recipe.md), then the
[reference](docs/writing-recipes.md).

## Configuration

Flags work everywhere, and a `.poultice.yaml` at the repository root saves
repeating them. `poultice init` writes a commented starter:

```yaml
severity: medium
skip:
  - semgrep
noAI: true
```

Flags always override the file. See [docs/configuration.md](docs/configuration.md).

## GitHub Action

```yaml
jobs:
  heal:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@v4
      - uses: adarshks016/poultice@main   # pin a release tag in production
        id: poultice
        with:
          severity: medium
          no-ai: true                       # or: anthropic-api-key: ${{ secrets.ANTHROPIC_API_KEY }}
          fail-on: unverified               # partial | unverified | failed | never
      - run: echo "outcome=${{ steps.poultice.outputs.outcome }}"
```

The action builds poultice from its own checkout, so a pinned tag runs exactly
that version and its recipes. Outputs: `outcome`, `exit-code`, `report` (path
to the JSON report) and `pr-body` (markdown, also written to the job summary).
Healed changes are committed locally on the runner; push them or open a pull
request with whatever tooling your workflow already uses.

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
[`internal/engine/engine_test.go`](internal/engine/engine_test.go) and the
end-to-end suite in [`internal/e2e`](internal/e2e/e2e_test.go), which runs the
shipped recipes against fixture repositories.

- **Nothing unverified survives.** Failed verification triggers `git reset --hard`
  to the last green commit plus `git clean -fd`.
- **A checkpoint contains exactly what policy checked.** The strategy's changes
  are staged, measured against policy, verified, and committed as that exact
  snapshot. Anything the verifier writes — caches, build output, logs — is
  discarded, never committed. Renames count against both their source and
  destination, so a file cannot be moved out of a denied path.
- **Your uncommitted work is never touched.** Poultice refuses to run on a dirty
  working tree rather than risk destroying changes it did not make.
- **The AI can never edit CI config or secrets.** `.github/**`, `Jenkinsfile`,
  `**/*.pem`, `**/.env`, `**/settings.xml` and friends are denied by default in
  every recipe, and a recipe cannot opt out.
- **Patches must apply cleanly or be discarded.** No whole-file overwrites, ever.
- **`--no-ai` is a real mode.** Every deterministic strategy works with no model,
  no key and no network, which also makes poultice safe to run on fork PRs.

## AI fixes

Set `ANTHROPIC_API_KEY` and `heal` asks Claude for a unified diff whenever
native strategies leave findings behind. Every generated patch still passes
`git apply --check`, policy, and verification before it can survive. Without a
key — or with `--no-ai` — the AI path reports `skipped: no AI provider
configured` and the deterministic half runs unchanged. Optional overrides:
`POULTICE_AI_MODEL` (default `claude-sonnet-5`) and `ANTHROPIC_BASE_URL`.

## Status

Every item on the original roadmap has shipped: the engine, nine recipes and
ten parsers, the Anthropic provider, the config file, the GitHub Action, release
automation, and an end-to-end suite over the shipped recipes. What comes next is
in [ROADMAP.md](ROADMAP.md).

## Contributing

New recipes are the most useful contribution, and they are data — no Go required
unless your tool needs a new parser. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
