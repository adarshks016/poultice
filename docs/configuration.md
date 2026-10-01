# Configuration

Poultice reads `.poultice.yaml` (or `.poultice.yml`) from the repository root,
or the file named by `--config`. Every key is optional.

```bash
poultice init      # writes a commented starter file; never overwrites one
```

## Precedence

```
command-line flag  >  environment  >  .poultice.yaml  >  built-in default
```

The file only fills in what the command line leaves unset. `--severity critical`
always wins over `severity: low`, and `--no-ai=false` overrides `noAI: true`.

## Keys

```yaml
severity: medium          # low | medium | high | critical   (default: high)

recipesDir: builtin       # "builtin", or a directory relative to this file
                          # (default: ./recipes if it exists, else builtin)

only:                     # run only these recipes (default: all that apply)
  - go-formatting
  - go-mod-tidy

skip:                     # never run these
  - semgrep

noAI: true                # deterministic strategies only (default: false)

prBody: pr-body.md        # write a pull request body after every heal,
                          # relative to this file

ai:
  model: claude-sonnet-5  # POULTICE_AI_MODEL, when set, takes precedence
```

| Key | Flag | Notes |
|---|---|---|
| `severity` | `--severity` | Findings below this are ignored entirely |
| `recipesDir` | `--recipes` | Relative paths resolve against the config file |
| `only` | `--recipe a,b` | Unknown names are an error, not a silent no-op |
| `skip` | `--skip a,b` | A name in both `only` and `skip` is rejected |
| `noAI` | `--no-ai` | |
| `prBody` | `--pr-body` | |
| `ai.model` | — | `POULTICE_AI_MODEL` overrides it |

Unknown keys are rejected with a line number. A typo such as `severty: low`
would otherwise silently mean "use the default".

## What is deliberately not configurable

**API keys.** `ANTHROPIC_API_KEY` is read from the environment only. The config
file lives in the repository, which is the wrong place for a secret.

**Verification.** Nothing in the config can skip, weaken or override a recipe's
`verify` block. To verify differently, write a recipe.

## Recipe sources

Poultice embeds its recipe library in the binary, so a binary from
`go install` or a release archive works in any repository:

1. `--recipes` / `recipesDir`, when given (`builtin` forces the embedded library)
2. otherwise `./recipes`, if the repository has one
3. otherwise the embedded library

`poultice recipes` prints which source it used and which config file it read.
