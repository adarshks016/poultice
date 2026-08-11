// Package verify runs a recipe's verification steps.
//
// Verification is the trust boundary of the entire system. Nothing a strategy
// produces is believed until every step here exits zero.
package verify

import (
	"context"
	"time"

	"github.com/adarshks016/poultice/internal/exec"
	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/recipe"
)

// TailBytes is how much of a failing step's output is retained for reporting.
const TailBytes = 4000

// Runner executes verification steps.
type Runner struct {
	Exec *exec.Runner
}

// New returns a verify Runner backed by the given exec Runner.
func New(r *exec.Runner) *Runner { return &Runner{Exec: r} }

// Run executes every step in order and stops at the first failure. Steps are
// ordered cheapest-first by convention (compile before test), so stopping early
// gives the fastest possible signal.
func (r *Runner) Run(ctx context.Context, steps []recipe.Step) model.Verdict {
	verdict := model.Verdict{Passed: true}
	for _, step := range steps {
		timeout := time.Duration(step.TimeoutSeconds) * time.Second
		res, err := r.Exec.Run(ctx, step.Run, timeout)

		sr := model.StepResult{
			Name:       step.Name,
			ExitCode:   res.ExitCode,
			DurationMS: res.Duration.Milliseconds(),
		}
		switch {
		case err != nil:
			// A timeout or spawn failure is a verification failure, not a crash:
			// a build that hangs is a build that does not pass.
			sr.Passed = false
			sr.Output = res.Tail(TailBytes)
			if sr.Output == "" {
				sr.Output = err.Error()
			}
		case res.Success():
			sr.Passed = true
		default:
			sr.Passed = false
			sr.Output = res.Tail(TailBytes)
		}

		verdict.Steps = append(verdict.Steps, sr)
		if !sr.Passed {
			verdict.Passed = false
			return verdict
		}
	}
	return verdict
}
