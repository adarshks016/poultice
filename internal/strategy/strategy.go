// Package strategy defines how a fix gets proposed.
//
// There are exactly two kinds and the distinction is load-bearing: a native
// strategy asks the upstream tool to fix its own findings, which is free,
// deterministic, and usually right; an AI strategy asks a language model, which
// is none of those things and is therefore always tried last and always gated
// behind verification.
package strategy

import (
	"context"
	"errors"
	"time"

	"github.com/adarshks016/poultice/internal/exec"
	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/recipe"
)

// ErrNotConfigured is returned by a Patcher that has no backing provider, which
// is the normal state when poultice runs with --no-ai or without credentials.
var ErrNotConfigured = errors.New("no AI provider configured")

// Result describes what a strategy did. It deliberately does not say whether
// the fix was correct — only verification decides that.
type Result struct {
	// Attempted is false when the strategy declined to act, for example an AI
	// strategy with no configured provider.
	Attempted bool
	// Output is the captured tool output, for the run log.
	Output string
	// Note is a short human-readable summary for the report.
	Note string
}

// RunNative executes a deterministic fixer.
//
// A non-zero exit is not treated as failure. Fixers routinely exit non-zero to
// mean "I fixed what I could and problems remain", and the re-diagnose pass is
// what actually decides whether anything improved.
func RunNative(ctx context.Context, r *exec.Runner, s recipe.Strategy) (Result, error) {
	timeout := time.Duration(s.TimeoutSeconds) * time.Second
	res, err := r.Run(ctx, s.Run, timeout)
	if err != nil {
		return Result{Attempted: true, Output: res.Tail(4000)}, err
	}
	return Result{
		Attempted: true,
		Output:    res.Tail(4000),
		Note:      s.DisplayName() + " completed with exit code " + itoa(res.ExitCode),
	}, nil
}

// Patcher produces a unified diff addressing the given findings.
//
// Implementations must return a patch in unified diff format and must never
// apply it themselves; the engine validates the diff against policy and applies
// it with `git apply --check` first. Returning ErrNotConfigured is the correct
// behaviour when no provider is available.
type Patcher interface {
	// Name identifies the provider in logs and commit trailers.
	Name() string
	// Propose returns a unified diff, or ErrNotConfigured.
	Propose(ctx context.Context, req PatchRequest) (string, error)
}

// PatchRequest is everything a Patcher is given. It carries no repository
// handle by design: a Patcher may read, but never write.
type PatchRequest struct {
	// Findings are the problems still outstanding after native strategies ran.
	Findings model.Findings
	// FailureOutput is the tail of the failing verify step, when the engine is
	// recovering from a broken build rather than fixing findings directly.
	FailureOutput string
	// Files maps repo-relative paths to their current contents, already trimmed
	// to the strategy's context budget.
	Files map[string]string
	// Policy is the contract the returned patch must satisfy. Implementations
	// should include it in the prompt; the engine enforces it regardless.
	Policy recipe.Policy
	// Attempt is 1-indexed and increases on each retry.
	Attempt int
}

// Disabled is a Patcher that always declines. It is the default so that the
// engine's AI path is exercised in tests and in --no-ai runs without any
// network access or credentials.
type Disabled struct{}

// Name implements Patcher.
func (Disabled) Name() string { return "disabled" }

// Propose implements Patcher.
func (Disabled) Propose(context.Context, PatchRequest) (string, error) {
	return "", ErrNotConfigured
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
