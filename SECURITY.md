# Security

Poultice runs inside CI with write access to the repository it is healing, and
often with credentials for a vulnerability scanner and a model provider. That is
a privileged position, and the design takes it seriously.

## Reporting a vulnerability

Please do not open a public issue. Use GitHub's
[private vulnerability reporting](https://github.com/adarshks016/poultice/security/advisories/new)
instead. Expect an acknowledgement within 72 hours.

## Threat model

### What poultice defends against

**A model that proposes a harmful patch.** Every patch is constrained by an
allow/deny path policy and file/line caps, must apply cleanly as a unified diff,
and must pass verification. `.github/**`, `Jenkinsfile`, `**/*.pem`, `**/.env`,
`**/settings.xml` and similar are denied in every recipe and cannot be
re-enabled. A model cannot rewrite the workflow that runs it, and cannot add a
step that exfiltrates a token.

**A fix that silently breaks the build.** Verification gates every change;
failures roll back to the last green commit.

**Destroying work that is not poultice's.** It refuses to run on a dirty working
tree rather than risk `git reset --hard` over your uncommitted changes.

**Supply chain.** Zero third-party Go dependencies. The only code in the binary
is this repository plus the Go standard library.

### What poultice does *not* defend against

**A malicious recipe.** Recipes execute shell commands by design — that is what
`diagnose.run`, `fix.run` and `verify.run` are. A recipe is executable content
with exactly the trust level of a CI config file.

> **Never run a recipe you have not read.** In particular, do not run recipes
> from a pull request in a workflow that has repository secrets. Treat
> `recipes/` as protected: require review on changes to it.

**A compromised tool.** If `snyk` or `ruff` on the runner is backdoored,
poultice will run it. Pin your tool versions.

**Prompt injection through source content.** A model reading a repository can be
influenced by content in that repository. Poultice's mitigation is structural
rather than textual: the patch is constrained by policy and gated by
verification regardless of what the model was persuaded to attempt. A prompt
injection that tries to add a credential-exfiltrating workflow step fails the
deny-path check; one that tries to weaken a test fails no check, so **review
`fix(ai):` commits line by line.** That is why they carry a distinct prefix.

## Credentials

Poultice reads credentials from the environment only. It never writes them to
disk, never logs them, and never includes them in a report or pull request body.

In CI, pass them as secrets:

```yaml
- run: poultice heal --severity high
  env:
    SNYK_TOKEN: ${{ secrets.SNYK_TOKEN }}
```

**Never put a token in a workflow's `env:` block as a literal.** Anything there
is readable by anyone who can read the repository, and tokens committed to a
public repository are scraped within minutes. If you have done this, revoking
and rotating the token is the only fix — rewriting the file does not help,
because git history keeps the old value.

## Hardening in CI

- Run with `--no-ai` on pull requests from forks. Every deterministic strategy
  still works, with no model and no network egress.
- Give the job the narrowest `permissions:` that lets it work, usually
  `contents: write` and `pull-requests: write` and nothing else.
- Pin actions to a commit SHA rather than a tag.
- Set `timeout-minutes` on the job. Poultice bounds its own subprocesses, but
  defence in depth is cheap.
- Require human review on the resulting pull request. Poultice opens draft PRs
  when it could not verify its own work; do not configure auto-merge on those.
