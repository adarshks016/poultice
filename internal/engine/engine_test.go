package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/recipe"
)

// unformatted is valid Go that gofmt will rewrite.
const unformatted = "package demo\n\nfunc Add( a int,b int ) int {\nreturn a+b\n}\n"

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"add", "-A"},
		{"-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// newRepo creates a throwaway Go module containing one unformatted file.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.22\n")
	write(t, dir, "demo.go", unformatted)
	gitInit(t, dir)
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func mustRecipe(t *testing.T, src string) *recipe.Recipe {
	t.Helper()
	r, err := recipe.Parse([]byte(src), "test.yaml")
	if err != nil {
		t.Fatalf("recipe: %v", err)
	}
	return r
}

const gofmtRecipe = `
apiVersion: poultice.dev/v1
kind: Recipe
metadata:
  name: go-formatting
  ecosystem: go
detect:
  files:
    - "**/*.go"
diagnose:
  run: gofmt -l .
  parse: gofmt-list
fix:
  - strategy: native
    name: gofmt-write
    run: gofmt -w .
verify:
  - name: build
    run: go build ./...
`

// A verifier that always fails, used to prove the rollback guarantee.
const gofmtRecipeFailingVerify = `
apiVersion: poultice.dev/v1
kind: Recipe
metadata:
  name: go-formatting-broken-gate
  ecosystem: go
detect:
  files:
    - "**/*.go"
diagnose:
  run: gofmt -l .
  parse: gofmt-list
fix:
  - strategy: native
    name: gofmt-write
    run: gofmt -w .
verify:
  - name: impossible
    run: exit 1
`

func TestHealAppliesAndVerifiesFix(t *testing.T) {
	dir := newRepo(t)
	eng := New(Options{RepoDir: dir, Severity: model.SeverityLow, NoAI: true})

	rep, err := eng.Heal(context.Background(), mustRecipe(t, gofmtRecipe))
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}

	if rep.Outcome != model.OutcomeHealed {
		t.Errorf("outcome = %s, want healed (attempts: %+v)", rep.Outcome, rep.Attempts)
	}
	if len(rep.FindingsBefore) != 1 {
		t.Errorf("findings before = %d, want 1", len(rep.FindingsBefore))
	}
	if len(rep.FindingsAfter) != 0 {
		t.Errorf("findings after = %d, want 0", len(rep.FindingsAfter))
	}
	if got := read(t, dir, "demo.go"); !strings.Contains(got, "func Add(a int, b int) int {") {
		t.Errorf("file was not reformatted:\n%s", got)
	}
	if rep.RolledBack {
		t.Error("a passing run should not roll back")
	}
}

// This is the property the whole project exists to provide: when verification
// fails, the fix is discarded rather than shipped.
func TestFailedVerificationRollsBack(t *testing.T) {
	dir := newRepo(t)
	before := headSHA(t, dir)

	eng := New(Options{RepoDir: dir, Severity: model.SeverityLow, NoAI: true})
	rep, err := eng.Heal(context.Background(), mustRecipe(t, gofmtRecipeFailingVerify))
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}

	if rep.Outcome != model.OutcomeUnverified {
		t.Errorf("outcome = %s, want unverified", rep.Outcome)
	}
	if !rep.RolledBack {
		t.Error("expected RolledBack to be true")
	}
	if got := read(t, dir, "demo.go"); got != unformatted {
		t.Errorf("working tree was not restored:\n%s", got)
	}
	if after := headSHA(t, dir); after != before {
		t.Errorf("HEAD moved from %s to %s; unverified work must not be committed", before, after)
	}
	if rep.Verdict == nil || rep.Verdict.Passed {
		t.Error("expected a failing verdict to be recorded")
	}
}

func TestCleanRepoProducesNoWork(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.22\n")
	write(t, dir, "demo.go", "package demo\n\nfunc Add(a, b int) int { return a + b }\n")
	gitInit(t, dir)

	eng := New(Options{RepoDir: dir, Severity: model.SeverityLow, NoAI: true})
	rep, err := eng.Heal(context.Background(), mustRecipe(t, gofmtRecipe))
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}
	if rep.Outcome != model.OutcomeClean {
		t.Errorf("outcome = %s, want clean", rep.Outcome)
	}
	if len(rep.Attempts) != 0 {
		t.Errorf("a clean repo should trigger no strategies, got %+v", rep.Attempts)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	dir := newRepo(t)
	eng := New(Options{RepoDir: dir, Severity: model.SeverityLow, NoAI: true, DryRun: true})

	rep, err := eng.Heal(context.Background(), mustRecipe(t, gofmtRecipe))
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}
	if len(rep.FindingsBefore) != 1 {
		t.Errorf("dry run should still diagnose, got %d findings", len(rep.FindingsBefore))
	}
	if got := read(t, dir, "demo.go"); got != unformatted {
		t.Error("dry run modified the working tree")
	}
}

func TestDirtyWorkingTreeIsRefused(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "scratch.txt", "uncommitted work\n")

	eng := New(Options{RepoDir: dir, Severity: model.SeverityLow, NoAI: true})
	_, err := eng.Heal(context.Background(), mustRecipe(t, gofmtRecipe))
	if err == nil {
		t.Fatal("expected a dirty working tree to be refused")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unexpected error: %v", err)
	}
	if read(t, dir, "scratch.txt") != "uncommitted work\n" {
		t.Error("poultice must never destroy pre-existing uncommitted work")
	}
}

func TestAppliesReportsMissingTooling(t *testing.T) {
	dir := newRepo(t)
	eng := New(Options{RepoDir: dir})

	src := strings.Replace(gofmtRecipe, "  files:\n    - \"**/*.go\"",
		"  files:\n    - \"**/*.go\"\n  requires:\n    - definitely-not-a-real-binary", 1)
	ok, why := eng.Applies(mustRecipe(t, src))
	if ok {
		t.Fatal("expected recipe to be inapplicable")
	}
	if !strings.Contains(why, "definitely-not-a-real-binary") {
		t.Errorf("reason should name the missing tool, got %q", why)
	}
}

func TestAppliesReportsNoMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "README.md", "# nothing to heal\n")
	gitInit(t, dir)

	eng := New(Options{RepoDir: dir})
	ok, why := eng.Applies(mustRecipe(t, gofmtRecipe))
	if ok {
		t.Fatal("expected recipe to be inapplicable")
	}
	if !strings.Contains(why, "detect.files") {
		t.Errorf("reason should mention detect.files, got %q", why)
	}
}
