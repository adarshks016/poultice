# Architecture

## The one idea

Everything in poultice exists to support a single invariant:

> **No change survives that has not passed the recipe's verify block, and the
> repository is never left worse than the last verified-green commit.**

If you are reading the code and something seems over-engineered, check whether
it is protecting that invariant. It usually is.

## Package map

```
cmd/poultice          CLI: heal, diagnose, recipes, validate
internal/
  model               Finding, Verdict, Outcome — the shared vocabulary
  yaml                minimal YAML subset decoder (zero third-party deps)
  recipe              recipe loading, schema validation, defaults
  parse               tool output → model.Findings, via a registry
  exec                subprocess runner: timeouts, process groups, capped capture
  gitutil             checkpoint and rollback
  policy              blast-radius enforcement (globs, file/line caps)
  strategy            native and AI fix strategies; the Patcher interface
  verify              runs verify steps, produces a Verdict
  engine              the state machine that wires all of the above together
  report              terminal, JSON and pull-request rendering
recipes/              the built-in recipe library
```

Dependencies point inward: `engine` knows about everything, `model` knows about
nothing. `parse` and `strategy` are the two extension seams.

## The state machine

`engine.Heal` is the only place control flow lives.

```
                 ┌─────────────┐
                 │   detect    │  detect.files globs + required binaries on PATH
                 └──────┬──────┘
                        │ applies
                 ┌──────▼──────┐
                 │  diagnose   │  run tool → parse → normalize → dedupe → filter
                 └──────┬──────┘
              no findings│  findings
          ┌──────────────┴───────────────┐
          ▼                              ▼
       CLEAN                    ┌─────────────────┐
                                │ native strategy │  for each, in order
                                └────────┬────────┘
                                         ▼
                                ┌─────────────────┐
                                │     settle      │  ◀── the trust boundary
                                └────────┬────────┘
                                         │
                          ┌──────────────┴──────────────┐
                       accepted                      rejected
                          │                              │
                    git checkpoint              git reset --hard HEAD
                                                        + clean -fd
                                         ▼
                                ┌─────────────────┐
                                │  AI strategy    │  only for residual findings,
                                └────────┬────────┘  bounded by maxAttempts
                                         ▼
                                    settle (again)
                                         ▼
                                ┌─────────────────┐
                                │ final diagnose  │  decides the Outcome
                                └─────────────────┘
```

### `settle` is the whole design

Every strategy — native or AI — funnels through `engine.settle`, which does four
things in this order:

1. **Did anything change?** No changes means the strategy did nothing; nothing
   to verify, nothing to commit.
2. **Policy check.** Changed paths against allow/deny globs, file count and line
   count against caps. A violation rolls back immediately and never runs the
   (expensive) verifier.
3. **Verify.** Every step in order, stopping at the first failure. Steps are
   ordered cheapest-first by convention, so a broken compile fails in seconds
   instead of after the full test suite.
4. **Checkpoint or roll back.** Pass → an ordinary git commit, so the branch
   reads as reviewable history. Fail → `git reset --hard HEAD` and `git clean -fd`.

Because rollback always targets `HEAD`, and `HEAD` only advances on a passing
verdict, `HEAD` *is* the last verified-green commit by construction. There is no
separate bookkeeping to get out of sync.

## Key type decisions

### `model.Finding` is toolchain-agnostic

A CVE, a lint error, and a compile failure are all just `Finding`. The engine
never branches on which kind it is. That is what lets one binary heal Maven,
pip and Go modules without knowing anything about them.

### Fingerprints exclude message and line

```go
key := source + ruleID + file + package
```

Messages get reworded between tool versions and line numbers shift when code
above them changes. Including either would make a scheduled run reopen the same
pull request every week. This is the bug that makes most homegrown automation
unusable after a month.

### `Outcome` is named for the decision, not the state

`ShouldOpenPR()` and `DraftPR()` live on it. A caller should never have to
reconstruct "so does this mean I open a PR or not" from a status enum.

### Patches are diffs, never whole files

`engine.applyPatch` requires a unified diff and runs `git apply --check` before
`git apply`. A truncated or hallucinated hunk fails the check and never reaches
the tree.

This is deliberately a response to the most common failure in AI-fix tooling: a
model asked to return a whole file, capped at some `max_tokens`, returns a
plausible-looking prefix. Whole-file replacement cannot detect that. A diff can.

### Native before AI is enforced by the schema

`recipe.Validate` rejects a native strategy declared after an AI one. Ordering
is not a suggestion — a model call that a free deterministic fixer would have
made unnecessary is pure waste, and every wasted call is also a chance to
introduce a wrong fix.

## Extension points

**A new tool** → write a `parse.Parser` (one function, ~30 lines), register it in
`init()`, then write a recipe that names it. No engine changes.

**A new model provider** → implement `strategy.Patcher`:

```go
type Patcher interface {
    Name() string
    Propose(ctx context.Context, req PatchRequest) (string, error)
}
```

It returns a unified diff and must never write to the filesystem. The engine
validates and applies; the provider only suggests. This split is why a buggy
provider cannot corrupt a repository.

**A new CI system** → poultice is a plain binary with meaningful exit codes.
The GitHub Action is a wrapper, not the product.

## Testing strategy

The engine tests build real throwaway git repositories in `t.TempDir()`, run
real `gofmt` and real `go build` against them, and assert on the resulting git
state. They are integration tests wearing unit test clothing, and that is
intentional: the invariant this project sells is about what ends up in git, so
the tests have to check git.

The single most important test is `TestFailedVerificationRollsBack`. If that
ever goes red, the project is lying about its core claim.
