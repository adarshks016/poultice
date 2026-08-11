# Writing recipes

A recipe teaches poultice to heal one class of problem in one ecosystem. Most
are 30–50 lines of YAML and need no Go code at all.

Validate as you go:

```bash
poultice validate recipes/my-recipe.yaml
poultice recipes                      # does it apply to this repo, and why not?
poultice heal --recipe my-recipe --dry-run --severity low
```

## Skeleton

```yaml
apiVersion: poultice.dev/v1
kind: Recipe

metadata:
  name: my-recipe          # unique, kebab-case
  ecosystem: python        # java | python | go | node | …
  summary: >
    One or two sentences. Shown in listings.

detect:
  files: ["**/*.py"]       # at least one glob must match
  requires: [ruff]         # binaries that must be on PATH

diagnose:
  run: ruff check --output-format json .
  parse: ruff-json
  expectNonZeroExit: true  # the tool exits 1 when it finds things

fix:
  - strategy: native
    name: ruff-safe-fixes
    run: ruff check --fix .

verify:
  - name: test
    run: python -m pytest -q
```

## The verify block

This is the only required section with no default, and the loader rejects a
recipe without it.

Write the strongest verifier you can afford. Poultice's guarantee is exactly as
good as this block: if your verify step is `true`, poultice will happily commit
anything. Some guidance:

- **Order cheapest first.** Verification stops at the first failure, so
  `compile` before `test` turns a five-minute failure into a five-second one.
- **A compile check alone is weak** for anything a model touched. It proves the
  code parses, not that it behaves.
- **For generated tests, use mutation testing** (`pitest`, `mutmut`) rather than
  "the tests pass" — otherwise an assertion-free test suite verifies itself.
- **Prefer deterministic steps.** A flaky verifier makes poultice roll back good
  fixes, which is annoying, and occasionally accept bad ones, which is worse.

## Strategies

### Native

Runs the tool's own fixer. Free, deterministic, and correct far more often than
a model. Always prefer one of these if the tool has any autofix at all.

```yaml
  - strategy: native
    name: snyk-fix
    run: snyk fix --all-projects
    timeoutSeconds: 1800
```

A non-zero exit is **not** treated as failure — fixers routinely exit non-zero
meaning "fixed some, others remain". The re-diagnose pass decides what actually
improved.

### AI

Runs only after every native strategy, and only against findings that survived
them. Requires `policy.allowPaths`; the loader rejects an unbounded AI strategy.

```yaml
  - strategy: ai
    name: residual-fixes
    maxAttempts: 2
    context:
      include: ["**/*.py"]
      maxBytes: 40000
    policy:
      allowPaths: ["**/*.py"]
      denyPaths: ["**/migrations/**"]
      maxChangedFiles: 5
      maxChangedLines: 200
```

Think hard about `allowPaths`. It is the difference between "the model may bump
a dependency version" and "the model may rewrite my application". For a
dependency CVE the answer is always `["**/pom.xml"]` or equivalent — the fix
belongs in the manifest, and a patch touching sources is out of scope by
definition and should be rejected.

`context.include` bounds what the model *reads*; `policy.allowPaths` bounds what
it may *change*. They are frequently different: reading a source file to
understand a breakage is fine even when only the manifest may be edited.

### Always-denied paths

Every AI strategy inherits these, and a recipe cannot opt out:

```
.github/**   .gitlab-ci.yml   Jenkinsfile   .git/**
**/*.pem     **/*.key         **/id_rsa*
**/.env      **/.env.*        **/.npmrc     **/settings.xml
```

A healer that can rewrite its own CI configuration is not a healer.

## Parsers

`diagnose.parse` names a registered parser. Built in today:

| Parser | Reads |
|---|---|
| `gofmt-list` | `gofmt -l` output |
| `go-vet` | `go vet` diagnostics |
| `go-build` | Go compiler errors |
| `ruff-json` | `ruff check --output-format json` |
| `snyk-json` | `snyk test --json`, both object and array shapes |

`poultice recipes` prints the current list.

### `$POULTICE_OUT`

Tools that insist on writing JSON to a file instead of stdout get a temp path in
`$POULTICE_OUT`; the parser reads it automatically if the file exists.

```yaml
diagnose:
  run: snyk test --all-projects --json-file-output=$POULTICE_OUT
  parse: snyk-json
```

### Adding a parser

One function in `internal/parse/`, registered in `init()`:

```go
func init() { Register("my-tool", parseMyTool) }

func parseMyTool(in Input) (model.Findings, error) {
    // in.Output, in.ExitCode, in.RepoDir, in.ArtifactPath
    return findings, nil
}
```

Set `NativelyFixable` honestly — the engine uses it to decide whether spending
model tokens is worth it at all.

## The YAML subset

Poultice ships its own decoder to avoid a third-party dependency in a process
that holds repository write access. It supports mappings, sequences, sequences
of mappings, block scalars (`|` and `>`), quoted scalars and comments.

It does **not** support anchors and aliases, multiple documents, or inline flow
collections other than `[]` and `{}`. These are rejected with a line number
rather than silently misread. If you hit that limit, the recipe is probably
trying to be a program — which is what strategies are for.

## Checklist

- [ ] `poultice validate` passes
- [ ] `poultice recipes` shows it applying in a repo where it should
- [ ] The verify block would actually catch a bad fix
- [ ] A native strategy exists if the tool has any autofix
- [ ] Any AI strategy has the tightest `allowPaths` that can still work
- [ ] Timeouts are set for anything slower than a few seconds
