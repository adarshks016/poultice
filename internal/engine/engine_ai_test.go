package engine

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/strategy"
)

// fakePatcher is a deterministic stand-in for a real AI provider. It returns a
// scripted response per attempt so the engine's apply → verify → rollback path
// can be exercised without a network call. This is the whole point of the
// strategy.Patcher seam: the risky, non-deterministic half is mockable, and the
// guarantees around it are testable.
type fakePatcher struct {
	name  string
	steps []patchStep
	calls int
}

type patchStep struct {
	diff string
	err  error
}

func (f *fakePatcher) Name() string {
	if f.name != "" {
		return f.name
	}
	return "fake"
}

func (f *fakePatcher) Propose(context.Context, strategy.PatchRequest) (string, error) {
	i := f.calls
	f.calls++
	if i >= len(f.steps) {
		i = len(f.steps) - 1
	}
	return f.steps[i].diff, f.steps[i].err
}

// A Go module with a single formatting defect gofmt will report but that only a
// patch can fix here, because the recipe offers no native strategy.
const aiDemoSrc = "package demo\n\nfunc Add(a, b int) int { return a+b }\n"

// goodDiff corrects the spacing; the result is gofmt-clean and builds.
const goodDiff = `diff --git a/demo.go b/demo.go
--- a/demo.go
+++ b/demo.go
@@ -1,3 +1,3 @@
 package demo

-func Add(a, b int) int { return a+b }
+func Add(a, b int) int { return a + b }
`

// badDiff applies cleanly and is valid syntax, but references an undefined
// identifier, so `go build` — the verify step — fails.
const badDiff = `diff --git a/demo.go b/demo.go
--- a/demo.go
+++ b/demo.go
@@ -1,3 +1,3 @@
 package demo

-func Add(a, b int) int { return a+b }
+func Add(a, b int) int { return notdefined }
`

const aiRecipe = `
apiVersion: poultice.dev/v1
kind: Recipe
metadata:
  name: go-ai-fix
  ecosystem: go
detect:
  files:
    - "**/*.go"
diagnose:
  run: gofmt -l .
  parse: gofmt-list
fix:
  - strategy: ai
    name: llm-format
    maxAttempts: 2
    context:
      include:
        - "**/*.go"
    policy:
      allowPaths:
        - "**/*.go"
      maxChangedFiles: 1
      maxChangedLines: 5
verify:
  - name: build
    run: go build ./...
`

func newAIRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.22\n")
	write(t, dir, "demo.go", aiDemoSrc)
	gitInit(t, dir)
	return dir
}

func lastCommitMsg(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "-1", "--pretty=%B")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// The happy path: a verified AI patch is applied and committed.
func TestAIPatchAcceptedWhenVerified(t *testing.T) {
	dir := newAIRepo(t)
	before := headSHA(t, dir)

	patcher := &fakePatcher{name: "unit/echo", steps: []patchStep{{diff: goodDiff}}}
	eng := New(Options{RepoDir: dir, Severity: model.SeverityLow, Patcher: patcher})

	rep, err := eng.Heal(context.Background(), mustRecipe(t, aiRecipe))
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}

	if rep.Outcome != model.OutcomeHealed {
		t.Errorf("outcome = %s, want healed (attempts: %+v)", rep.Outcome, rep.Attempts)
	}
	if rep.RolledBack {
		t.Error("a verified AI patch must not roll back")
	}
	if got := read(t, dir, "demo.go"); !strings.Contains(got, "return a + b") {
		t.Errorf("patch not applied:\n%s", got)
	}
	if after := headSHA(t, dir); after == before {
		t.Error("a healed run should advance HEAD with a checkpoint commit")
	}
	if msg := lastCommitMsg(t, dir); !strings.Contains(msg, "unit/echo") {
		t.Errorf("commit should credit the provider, got:\n%s", msg)
	}
	if patcher.calls != 1 {
		t.Errorf("expected exactly 1 provider call, got %d", patcher.calls)
	}
	// Exactly one AI attempt, accepted.
	var ai int
	for _, a := range rep.Attempts {
		if a.Kind == "ai" {
			ai++
			if !a.Accepted {
				t.Errorf("ai attempt not accepted: %+v", a)
			}
		}
	}
	if ai != 1 {
		t.Errorf("want 1 ai attempt, got %d", ai)
	}
}

// The core guarantee, on the AI path: an unverifiable patch is discarded and the
// repository is left byte-identical to before.
func TestAIPatchRolledBackWhenVerifyFails(t *testing.T) {
	dir := newAIRepo(t)
	before := headSHA(t, dir)

	// Both attempts return a build-breaking patch, so every try is rejected.
	patcher := &fakePatcher{steps: []patchStep{{diff: badDiff}}}
	eng := New(Options{RepoDir: dir, Severity: model.SeverityLow, Patcher: patcher})

	rep, err := eng.Heal(context.Background(), mustRecipe(t, aiRecipe))
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}

	if rep.Outcome != model.OutcomeUnverified {
		t.Errorf("outcome = %s, want unverified", rep.Outcome)
	}
	if !rep.RolledBack {
		t.Error("expected RolledBack to be true")
	}
	if got := read(t, dir, "demo.go"); got != aiDemoSrc {
		t.Errorf("working tree not restored:\n%s", got)
	}
	if after := headSHA(t, dir); after != before {
		t.Errorf("HEAD moved; unverifiable AI work must not be committed")
	}
	if patcher.calls != 2 {
		t.Errorf("expected maxAttempts=2 provider calls, got %d", patcher.calls)
	}
}

// Bounded retry: a first bad patch is rolled back, and a second good one is then
// accepted against the restored tree.
func TestAIRetrySucceedsOnSecondAttempt(t *testing.T) {
	dir := newAIRepo(t)

	patcher := &fakePatcher{steps: []patchStep{{diff: badDiff}, {diff: goodDiff}}}
	eng := New(Options{RepoDir: dir, Severity: model.SeverityLow, Patcher: patcher})

	rep, err := eng.Heal(context.Background(), mustRecipe(t, aiRecipe))
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}

	if rep.Outcome != model.OutcomeHealed {
		t.Errorf("outcome = %s, want healed (attempts: %+v)", rep.Outcome, rep.Attempts)
	}
	if patcher.calls != 2 {
		t.Errorf("expected 2 attempts (1 fail, 1 success), got %d", patcher.calls)
	}
	if got := read(t, dir, "demo.go"); !strings.Contains(got, "return a + b") {
		t.Errorf("second-attempt patch not applied:\n%s", got)
	}
}

// A provider that declines (no credentials) leaves the tree untouched and is
// reported as skipped, never as a failure.
func TestAINotConfiguredIsSkipped(t *testing.T) {
	dir := newAIRepo(t)
	before := headSHA(t, dir)

	eng := New(Options{RepoDir: dir, Severity: model.SeverityLow, Patcher: strategy.Disabled{}})
	rep, err := eng.Heal(context.Background(), mustRecipe(t, aiRecipe))
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}

	if rep.RolledBack {
		t.Error("a skipped AI path should not roll back anything")
	}
	if got := read(t, dir, "demo.go"); got != aiDemoSrc {
		t.Error("skipped AI path modified the working tree")
	}
	if after := headSHA(t, dir); after != before {
		t.Error("skipped AI path must not move HEAD")
	}
	var sawSkip bool
	for _, a := range rep.Attempts {
		if a.Kind == "ai" && a.Note == skippedNote {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Errorf("expected a skipped ai attempt, got %+v", rep.Attempts)
	}
}
