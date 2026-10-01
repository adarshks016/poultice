# Your first recipe

This walkthrough builds a real recipe from nothing: `go-mod-tidy`, which keeps
`go.mod` and `go.sum` in step with the code. It is the recipe that ships in
[`recipes/go-mod-tidy.yaml`](../recipes/go-mod-tidy.yaml), so every step below
is known to work. Follow along in any Go module.

[writing-recipes.md](writing-recipes.md) is the reference; this is the tutorial.

## 1. Find the problem and the tool that sees it

A recipe starts with a question: *what command reports this problem, without
changing anything?* For module hygiene, Go 1.23 added exactly that:

```console
$ go mod tidy -diff
diff current/go.mod tidy/go.mod
--- current/go.mod
+++ tidy/go.mod
@@ -2,6 +2,4 @@
 
 go 1.22
 
-require example.com/dep v0.0.0
-
 replace example.com/dep => ./dep
$ echo $?
1
```

Three facts to note, because each one becomes a line of YAML:

- It writes nothing, so it is safe to run as a diagnosis.
- It exits **1** when it finds something. That is a result, not a failure.
- It also exits 1 when tidy itself breaks (a missing module, no network).
  Whatever reads this output must tell the two apart.

## 2. Start the file

Create `recipes/go-mod-tidy.yaml`. The header is fixed; the metadata is yours:

```yaml
apiVersion: poultice.dev/v1
kind: Recipe

metadata:
  name: go-mod-tidy
  ecosystem: go
  summary: >
    Keep go.mod and go.sum in step with the code.
```

`name` is how users select the recipe (`--recipe go-mod-tidy`), so make it
unique and kebab-case.

## 3. Detect: when does this apply?

```yaml
detect:
  files:
    - go.mod
  requires:
    - go
```

`files` are globs, and at least one must match for the recipe to run.
`requires` lists binaries that must be on `PATH`. If either check fails,
`poultice recipes` says which one and why, instead of failing mid-run.

Lists are always written in block style. The YAML decoder poultice ships
rejects inline lists like `files: [go.mod]` with a line number, on purpose.

## 4. Diagnose: turn output into findings

```yaml
diagnose:
  run: go mod tidy -diff
  parse: go-mod-tidy-diff
  expectNonZeroExit: true
  timeoutSeconds: 300
```

`expectNonZeroExit` encodes the first fact from step 1: exit 1 is a result.
Without it, poultice treats the non-zero exit as a broken tool and stops.

`parse` names a parser. Poultice already has parsers for gofmt, go vet, ruff,
Snyk, govulncheck, npm audit, semgrep and cargo audit (`poultice recipes` lists
them), so most recipes stop here. `go mod tidy -diff` had none, so this recipe
needed one.

### Writing the parser

A parser is one function that turns tool output into findings, registered by
name. This is the core of
[`internal/parse/gomodtidy.go`](../internal/parse/gomodtidy.go):

```go
func init() { Register("go-mod-tidy-diff", parseGoModTidyDiff) }

func parseGoModTidyDiff(in Input) (model.Findings, error) {
	// One finding per "diff current/<file> tidy/<file>" block, counting
	// the + and - lines beneath it.
	...
	if len(files) == 0 {
		if in.ExitCode == 0 {
			return nil, nil // genuinely tidy
		}
		// Exit 1 with no diff means tidy itself failed. Returning "no
		// findings" here would report a broken module as healthy.
		return nil, fmt.Errorf("go mod tidy -diff exited %d: %s", in.ExitCode, out)
	}
	...
}
```

The last branch handles the third fact from step 1. A parser that cannot read
its input should say so. Returning an empty list would turn every tool crash
into a clean bill of health.

Each finding should set:

| Field | Here | Why it matters |
|---|---|---|
| `RuleID` | `go-mod-tidy` | Part of the fingerprint that deduplicates across runs |
| `File` | `go.mod` / `go.sum` | Lets context collection prioritize the right files |
| `Severity` | `low` | Compared against `--severity`; housekeeping is low |
| `NativelyFixable` | `true` | Tells the engine a deterministic fix exists |

Add a table-driven test next to it. See
[`gomodtidy_test.go`](../internal/parse/gomodtidy_test.go), which covers an
untidy module, a tidy one, and both kinds of tidy failure.

## 5. Fix: deterministic first

```yaml
fix:
  - strategy: native
    name: go-mod-tidy
    run: go mod tidy
    timeoutSeconds: 300
```

A `native` strategy runs the tool's own fixer: free, deterministic, and right
far more often than a model. Always reach for one first. The loader enforces
this by rejecting any native strategy declared after an AI one.

This recipe stops there on purpose. If `go mod tidy` cannot resolve the module
graph, a model guessing at `go.mod` will not either, and a human needs to see
the error. Add an `ai` strategy only when there is a real residue a model can
fix within a narrow `policy.allowPaths`. `go-vuln.yaml` has an example.

## 6. Verify: the part that is not optional

```yaml
verify:
  - name: build
    run: go build ./...
    timeoutSeconds: 600
  - name: test
    run: go test ./...
    timeoutSeconds: 900
```

The loader rejects a recipe with no `verify` block. That rule is the reason the
project exists.

Ask what a bad fix would look like, and make sure some step catches it. Here a
bad tidy drops a requirement the code still needs: `go build` catches that in
seconds, and `go test` catches anything subtler. Order steps cheapest first,
because verification stops at the first failure.

Steps may write files (build output, caches, logs). Those are thrown away, not
committed. Only the fix's own changes, which policy checked, reach a commit.

## 7. Validate, dry-run, heal

```bash
poultice validate recipes/go-mod-tidy.yaml
poultice recipes                                   # applies here? if not, why?
poultice diagnose --recipe go-mod-tidy --severity low
poultice heal --recipe go-mod-tidy --severity low --no-ai
```

`--severity low` matters: the default threshold is `high`, and this recipe's
findings are `low`. Use `--severity low` while developing a recipe so nothing is
filtered out from under you.

A successful heal leaves one commit, `fix(go): go-mod-tidy [poultice]`. A failed
verify leaves the repository byte-for-byte as it was, with `UNVERIFIED` and the
failing step's output in the report.

## 8. Prove it end to end

Unit tests check the parser; an end-to-end test checks that the recipe, parser
and engine agree. [`internal/e2e`](../internal/e2e/e2e_test.go) runs every
shipped recipe, unmodified, against a fixture repository:

```go
func TestGoModTidyHeals(t *testing.T) {
	dir := setup(t, "go-untidy", "")  // testdata/fixtures/go-untidy
	before := git(t, dir, "rev-parse", "HEAD")

	rep := heal(t, dir, "go-mod-tidy")

	assertHealed(t, dir, before, rep)
}
```

Fixture files end in `.tmpl` so they never look like live source to gofmt, to
your editor, or to poultice healing its own repository. For tools that need
credentials or a network (Snyk, Maven), the suite puts small shell stand-ins
on `PATH` that emit the real tool's JSON. See `testdata/stubs/`.

## Checklist

- [ ] `poultice validate` passes
- [ ] `poultice recipes` shows it applying where it should, and explains why
      not where it should not
- [ ] The parser returns an error, not an empty list, when the tool fails
- [ ] A native strategy exists if the tool has any autofix
- [ ] The verify block would catch a bad fix
- [ ] Parser test and an e2e fixture
