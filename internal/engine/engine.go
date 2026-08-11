// Package engine implements the healing state machine.
//
//	detect ─▶ diagnose ─▶ [findings?] ─▶ native fix ─▶ verify ─▶ ✅ checkpoint
//	                                          │                      │
//	                                          │                 ai patch (n ≤ N)
//	                                          │                      │
//	                                          ▼                    verify
//	                                       nothing                   │
//	                                     to attempt          ┌───────┴───────┐
//	                                                       pass            fail
//	                                                         │               │
//	                                                    checkpoint    rollback to
//	                                                                  last green
//
// Two invariants hold at every step:
//
//  1. No change survives that has not passed the recipe's verify block.
//  2. The repository is never left worse than the last verified-green commit.
//
// Everything else in this package is bookkeeping in service of those two.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/adarshks016/poultice/internal/exec"
	"github.com/adarshks016/poultice/internal/gitutil"
	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/parse"
	"github.com/adarshks016/poultice/internal/policy"
	"github.com/adarshks016/poultice/internal/recipe"
	"github.com/adarshks016/poultice/internal/strategy"
	"github.com/adarshks016/poultice/internal/verify"
)

// Options configures a heal run.
type Options struct {
	// RepoDir is the repository root.
	RepoDir string
	// Severity is the minimum severity acted upon.
	Severity model.Severity
	// DryRun diagnoses and reports but applies no fixes.
	DryRun bool
	// NoAI disables model-backed strategies regardless of recipe content.
	NoAI bool
	// Patcher supplies AI patches. Nil means strategy.Disabled.
	Patcher strategy.Patcher
	// Log receives progress lines. Nil discards them.
	Log func(format string, args ...any)
}

func (o *Options) logf(format string, args ...any) {
	if o.Log != nil {
		o.Log(format, args...)
	}
}

// Report is the full record of a heal run. It is the input to every output
// format: terminal summary, JSON, and pull request body.
type Report struct {
	Recipe    string         `json:"recipe"`
	Ecosystem string         `json:"ecosystem"`
	Outcome   model.Outcome  `json:"outcome"`
	Severity  model.Severity `json:"severity"`

	FindingsBefore model.Findings `json:"findingsBefore"`
	FindingsAfter  model.Findings `json:"findingsAfter"`
	Resolved       model.Findings `json:"resolved"`

	Attempts []Attempt `json:"attempts"`

	// Verdict is the final verification result, when one was reached.
	Verdict *model.Verdict `json:"verdict,omitempty"`
	// GreenSHA is the last commit that passed verification, when the repo is a
	// git working tree.
	GreenSHA string `json:"greenSha,omitempty"`
	// RolledBack is true when unverifiable work was discarded.
	RolledBack bool `json:"rolledBack"`
	// Skipped explains why a recipe did not run at all.
	Skipped string `json:"skipped,omitempty"`
	// DurationMS is total wall-clock time.
	DurationMS int64 `json:"durationMs"`
}

// Attempt records one strategy execution and its verification result.
type Attempt struct {
	Strategy string         `json:"strategy"`
	Kind     string         `json:"kind"`
	Note     string         `json:"note,omitempty"`
	Verdict  *model.Verdict `json:"verdict,omitempty"`
	// Accepted is true when the attempt's changes survived verification and
	// policy checks.
	Accepted bool `json:"accepted"`
	// Rejection explains why an attempt was discarded.
	Rejection string `json:"rejection,omitempty"`
}

// Engine runs recipes against a repository.
type Engine struct {
	opts   Options
	runner *exec.Runner
	repo   *gitutil.Repo
	verify *verify.Runner
}

// New constructs an Engine.
func New(opts Options) *Engine {
	if opts.Severity == "" {
		opts.Severity = recipe.SeverityThresholdDefault
	}
	if opts.Patcher == nil {
		opts.Patcher = strategy.Disabled{}
	}
	r := exec.New(opts.RepoDir)
	return &Engine{
		opts:   opts,
		runner: r,
		repo:   gitutil.Open(opts.RepoDir),
		verify: verify.New(r),
	}
}

// Applies reports whether a recipe's detect block matches this repository, and
// why not when it does not.
func (e *Engine) Applies(rc *recipe.Recipe) (bool, string) {
	matched := false
	for _, pattern := range rc.Detect.Files {
		hits, err := globRepo(e.opts.RepoDir, pattern)
		if err == nil && hits > 0 {
			matched = true
			break
		}
	}
	if !matched {
		return false, fmt.Sprintf("no files match detect.files %v", rc.Detect.Files)
	}
	if missing := exec.Look(rc.Detect.Requires); len(missing) > 0 {
		return false, fmt.Sprintf("required tools not on PATH: %v", missing)
	}
	return true, ""
}

// Heal runs one recipe end to end.
func (e *Engine) Heal(ctx context.Context, rc *recipe.Recipe) (*Report, error) {
	start := time.Now()
	rep := &Report{
		Recipe:    rc.Metadata.Name,
		Ecosystem: rc.Metadata.Ecosystem,
		Severity:  e.opts.Severity,
		Outcome:   model.OutcomeFailed,
	}
	defer func() { rep.DurationMS = time.Since(start).Milliseconds() }()

	if ok, why := e.Applies(rc); !ok {
		rep.Skipped = why
		rep.Outcome = model.OutcomeClean
		return rep, nil
	}

	isGit := e.repo.IsRepo(ctx)
	if isGit {
		clean, err := e.repo.IsClean(ctx)
		if err != nil {
			return rep, fmt.Errorf("inspect working tree: %w", err)
		}
		if !clean && !e.opts.DryRun {
			return rep, errors.New(
				"working tree has uncommitted changes; poultice needs a clean tree so it " +
					"can roll back its own work without destroying yours")
		}
		if sha, err := e.repo.Head(ctx); err == nil {
			rep.GreenSHA = sha
		}
	} else if !e.opts.DryRun {
		e.opts.logf("warning: %s is not a git repository; rollback is unavailable", e.opts.RepoDir)
	}

	// ── diagnose ───────────────────────────────────────────────────────────
	before, err := e.diagnose(ctx, rc)
	if err != nil {
		return rep, fmt.Errorf("diagnose: %w", err)
	}
	rep.FindingsBefore = before.Sorted()
	e.opts.logf("diagnose: %d finding(s) at severity >= %s", len(before), e.opts.Severity)

	if len(before) == 0 {
		rep.Outcome = model.OutcomeClean
		return rep, nil
	}
	if e.opts.DryRun {
		rep.FindingsAfter = rep.FindingsBefore
		rep.Outcome = model.OutcomePartial
		rep.Skipped = "dry run: no fixes applied"
		return rep, nil
	}

	// ── native strategies, cheapest first ──────────────────────────────────
	for _, s := range rc.NativeStrategies() {
		att := Attempt{Strategy: s.DisplayName(), Kind: string(s.Kind)}
		e.opts.logf("strategy %s: running", s.DisplayName())

		res, runErr := strategy.RunNative(ctx, e.runner, s)
		att.Note = res.Note
		if runErr != nil {
			att.Rejection = runErr.Error()
			rep.Attempts = append(rep.Attempts, att)
			e.rollback(ctx, rep, isGit)
			continue
		}

		accepted, rejection, verdict := e.settle(ctx, rc, s.Policy, isGit,
			fmt.Sprintf("fix(%s): %s [poultice]", rc.Metadata.Ecosystem, s.DisplayName()))
		att.Accepted, att.Rejection, att.Verdict = accepted, rejection, verdict
		rep.Attempts = append(rep.Attempts, att)
		rep.Verdict = verdict
		if accepted {
			if sha, err := e.repo.Head(ctx); err == nil && isGit {
				rep.GreenSHA = sha
			}
		} else {
			rep.RolledBack = rep.RolledBack || isGit
		}
	}

	// ── AI strategies, only for what native could not resolve ──────────────
	if !e.opts.NoAI {
		remaining, derr := e.diagnose(ctx, rc)
		if derr == nil && len(remaining) > 0 {
			for _, s := range rc.AIStrategies() {
				att := e.attemptAI(ctx, rc, s, remaining, isGit)
				rep.Attempts = append(rep.Attempts, att)
				if att.Verdict != nil {
					rep.Verdict = att.Verdict
				}
				switch {
				case att.Accepted:
					if sha, err := e.repo.Head(ctx); err == nil && isGit {
						rep.GreenSHA = sha
					}
				case att.Note == skippedNote:
					// No provider configured; nothing was attempted, nothing rolled back.
				case att.Rejection != "":
					rep.RolledBack = rep.RolledBack || isGit
				}
			}
		}
	}

	// ── final diagnose decides the outcome ─────────────────────────────────
	after, err := e.diagnose(ctx, rc)
	if err != nil {
		return rep, fmt.Errorf("final diagnose: %w", err)
	}
	rep.FindingsAfter = after.Sorted()
	rep.Resolved = before.Resolved(after)

	anyAccepted := false
	for _, a := range rep.Attempts {
		anyAccepted = anyAccepted || a.Accepted
	}

	switch {
	case !anyAccepted && rep.RolledBack:
		rep.Outcome = model.OutcomeUnverified
	case len(after) == 0:
		rep.Outcome = model.OutcomeHealed
	case len(rep.Resolved) > 0:
		rep.Outcome = model.OutcomePartial
	case rep.RolledBack:
		rep.Outcome = model.OutcomeUnverified
	default:
		rep.Outcome = model.OutcomePartial
	}
	return rep, nil
}

const skippedNote = "skipped: no AI provider configured"

// attemptAI runs one AI strategy with bounded retries.
func (e *Engine) attemptAI(
	ctx context.Context,
	rc *recipe.Recipe,
	s recipe.Strategy,
	findings model.Findings,
	isGit bool,
) Attempt {
	att := Attempt{Strategy: s.DisplayName(), Kind: string(s.Kind)}

	files, err := collectContext(e.opts.RepoDir, s.Context, findings)
	if err != nil {
		att.Rejection = "collect context: " + err.Error()
		return att
	}

	for attempt := 1; attempt <= s.MaxAttempts; attempt++ {
		req := strategy.PatchRequest{
			Findings: findings,
			Files:    files,
			Policy:   s.Policy,
			Attempt:  attempt,
		}
		diff, err := e.opts.Patcher.Propose(ctx, req)
		if errors.Is(err, strategy.ErrNotConfigured) {
			att.Note = skippedNote
			return att
		}
		if err != nil {
			att.Rejection = fmt.Sprintf("attempt %d: provider error: %v", attempt, err)
			continue
		}
		if err := applyPatch(ctx, e.runner, diff); err != nil {
			att.Rejection = fmt.Sprintf("attempt %d: patch rejected: %v", attempt, err)
			continue
		}

		accepted, rejection, verdict := e.settle(ctx, rc, s.Policy, isGit,
			fmt.Sprintf("fix(ai): %s attempt %d [poultice]\n\nProvider: %s",
				s.DisplayName(), attempt, e.opts.Patcher.Name()))
		att.Accepted, att.Rejection, att.Verdict = accepted, rejection, verdict
		if accepted {
			return att
		}
	}
	return att
}

// settle is the trust boundary: it checks policy, verifies, and then either
// checkpoints the work or rolls it back. Nothing reaches a branch without
// passing through here.
func (e *Engine) settle(
	ctx context.Context,
	rc *recipe.Recipe,
	pol recipe.Policy,
	isGit bool,
	commitMsg string,
) (accepted bool, rejection string, verdict *model.Verdict) {
	if isGit {
		changed, err := e.repo.ChangedFiles(ctx)
		if err != nil {
			return false, "inspect changes: " + err.Error(), nil
		}
		if len(changed) == 0 {
			return false, "strategy produced no changes", nil
		}
		_, nLines, err := e.repo.DiffStat(ctx)
		if err != nil {
			return false, "measure diff: " + err.Error(), nil
		}
		if err := policy.Check(pol, changed, nLines); err != nil {
			e.opts.logf("policy: %v", err)
			e.rollbackTo(ctx, isGit)
			return false, err.Error(), nil
		}
	}

	e.opts.logf("verify: running %d step(s)", len(rc.Verify))
	v := e.verify.Run(ctx, rc.Verify)
	verdict = &v

	if !v.Passed {
		failed := v.FailedStep()
		name := "unknown"
		if failed != nil {
			name = failed.Name
		}
		e.opts.logf("verify: FAILED at step %q — rolling back", name)
		e.rollbackTo(ctx, isGit)
		return false, "verification failed at step " + name, verdict
	}

	e.opts.logf("verify: passed")
	if isGit {
		if _, err := e.repo.Checkpoint(ctx, commitMsg); err != nil {
			return false, "checkpoint: " + err.Error(), verdict
		}
	}
	return true, "", verdict
}

// rollbackTo discards everything back to HEAD, which is by construction the
// last verified-green commit.
func (e *Engine) rollbackTo(ctx context.Context, isGit bool) {
	if !isGit {
		e.opts.logf("warning: cannot roll back, not a git repository; " +
			"unverified changes remain in the working tree")
		return
	}
	if err := e.repo.ResetTo(ctx, "HEAD"); err != nil {
		e.opts.logf("error: rollback failed: %v", err)
	}
}

func (e *Engine) rollback(ctx context.Context, rep *Report, isGit bool) {
	e.rollbackTo(ctx, isGit)
	rep.RolledBack = rep.RolledBack || isGit
}

// diagnose runs the recipe's diagnose command and parses its output.
func (e *Engine) diagnose(ctx context.Context, rc *recipe.Recipe) (model.Findings, error) {
	parser, err := parse.Get(rc.Diagnose.Parse)
	if err != nil {
		return nil, err
	}

	in := parse.Input{RepoDir: e.opts.RepoDir}

	if rc.Diagnose.Run != "" {
		// $POULTICE_OUT gives tools that insist on writing JSON to disk a
		// well-known destination the parser can find.
		artifact := filepath.Join(os.TempDir(), fmt.Sprintf("poultice-%s.json", rc.Metadata.Name))
		_ = os.Remove(artifact)

		runner := *e.runner
		runner.Env = append(runner.Env, "POULTICE_OUT="+artifact)

		timeout := time.Duration(rc.Diagnose.TimeoutSeconds) * time.Second
		res, err := runner.Run(ctx, rc.Diagnose.Run, timeout)
		if err != nil {
			return nil, err
		}
		if !res.Success() && !rc.Diagnose.ExpectNonZeroExit {
			return nil, fmt.Errorf("diagnose command exited %d: %s", res.ExitCode, res.Tail(2000))
		}
		in.Output = res.Output
		in.ExitCode = res.ExitCode
		if _, statErr := os.Stat(artifact); statErr == nil {
			in.ArtifactPath = artifact
		}
	}

	findings, err := parser(in)
	if err != nil {
		return nil, err
	}
	return findings.Dedupe().AtLeast(e.opts.Severity), nil
}
