# Contributing

Thanks for looking. Poultice is early, so the highest-value contributions are
recipes and parsers rather than engine changes.

## Getting set up

```bash
git clone https://github.com/adarshks016/poultice
cd poultice
make check      # fmt + vet + test
make build      # ./bin/poultice
```

There are no third-party dependencies and none should be added without a
discussion first — see the reasoning in [SECURITY.md](SECURITY.md). A Go
toolchain and git are the whole setup.

## What to work on

**Recipes** are the best first contribution and usually need no Go at all. See
[docs/writing-recipes.md](docs/writing-recipes.md). Good candidates:

- flaky-test quarantine (any ecosystem)
- `javax` → `jakarta` migration
- `golangci-lint`, `spotless`, `eslint --fix`
- Docker base image bumps
- CI hygiene: unpinned actions, missing timeouts, over-broad permissions

**Parsers** are one function plus a registration. See `internal/parse/`.

**Providers** implement `strategy.Patcher`. If you want to build one, open an
issue first so we do not duplicate work — the interface may still move.

## The one rule that is not negotiable

**Nothing may bypass verification.** If a change makes it possible for poultice
to keep a modification that did not pass a recipe's verify block, it will not be
merged regardless of how useful it is. The project's entire value is that this
cannot happen. `TestFailedVerificationRollsBack` guards it; do not weaken it.

## Style

- Standard `gofmt`. `make check` enforces it.
- Comments explain **why**, not what. The code already says what.
- Exported identifiers get doc comments.
- Errors get context: `fmt.Errorf("read recipe: %w", err)`.
- Prefer explicit code over reflection or clever generics. The recipe decoder is
  hand-written on purpose so every field is greppable.

## Tests

Every behavioural change needs a test. Engine tests build real git repositories
in `t.TempDir()` and assert on real git state — follow that pattern rather than
mocking git, because the guarantee being tested is about what ends up in a
commit.

```bash
go test ./...
go test ./internal/engine/ -run TestFailedVerificationRollsBack -v
```

## Commits and pull requests

Conventional commits: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`.

In the pull request, say what breaks if the change is wrong. If it touches the
engine, say explicitly how the verification invariant is preserved.

## Code of conduct

Be decent. Assume good faith, critique the work rather than the person, and
leave the project friendlier than you found it. Behaviour that makes people not
want to contribute will be moderated.
