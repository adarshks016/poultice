// Package e2e runs the shipped recipes, unmodified, against fixture
// repositories and asserts the outcome a user would see.
//
// Go cases use the real toolchain. Scanner cases put small POSIX shell
// stand-ins for Snyk, Maven, ruff and Python on PATH: they emit the real tools'
// JSON shapes and make the same edits the real fixers make, so recipe, parser,
// engine, policy, verification and rollback all run exactly as in production —
// minus network access and credentials.
//
// Every fixture file ends in .tmpl, which is stripped on copy, so that neither
// gofmt nor poultice dogfooding this repository mistakes fixtures for sources.
package e2e

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/adarshks016/poultice/internal/engine"
	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/recipe"
)

// setup copies a fixture into a fresh git repository and, when stubs is set,
// puts that stub directory first on PATH.
func setup(t *testing.T, fixture, stubs string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("e2e fixtures rely on POSIX shell stand-ins")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// Hermetic Go: no network, no workspace, no surprise flags from the caller.
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "local")

	dir := t.TempDir()
	src := filepath.Join("testdata", "fixtures", fixture)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if !strings.HasSuffix(rel, ".tmpl") {
			t.Fatalf("fixture file %s must end in .tmpl", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, strings.TrimSuffix(rel, ".tmpl"))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}

	if stubs != "" {
		abs, err := filepath.Abs(filepath.Join("testdata", "stubs", stubs))
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", abs+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.name=fixture", "-c", "user.email=fixture@example.com",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "fixture")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// heal runs a recipe from the repository's recipes/ directory, exactly as
// shipped, with deterministic strategies only.
func heal(t *testing.T, dir, name string) *engine.Report {
	t.Helper()
	rc, err := recipe.Load(filepath.Join("..", "..", "recipes", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(engine.Options{RepoDir: dir, Severity: model.SeverityLow, NoAI: true})
	if ok, why := eng.Applies(rc); !ok {
		t.Fatalf("%s does not apply to the fixture: %s", name, why)
	}
	rep, err := eng.Heal(context.Background(), rc)
	if err != nil {
		t.Fatalf("Heal: %v", err)
	}
	return rep
}

// snapshot reads every file outside .git, for byte-for-byte comparisons.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func requireGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
}

// assertHealed checks the healed contract: the outcome, a single new verified
// commit on top of the fixture, and a clean tree.
func assertHealed(t *testing.T, dir, before string, rep *engine.Report) {
	t.Helper()
	if rep.Outcome != model.OutcomeHealed {
		t.Fatalf("outcome = %s, want healed\nattempts: %+v\nverdict: %+v", rep.Outcome, rep.Attempts, rep.Verdict)
	}
	if len(rep.FindingsAfter) != 0 || len(rep.Resolved) != len(rep.FindingsBefore) {
		t.Errorf("findings before=%d after=%d resolved=%d", len(rep.FindingsBefore), len(rep.FindingsAfter), len(rep.Resolved))
	}
	if got := git(t, dir, "rev-list", "--count", before+"..HEAD"); got != "1" {
		t.Errorf("want exactly one checkpoint commit, got %s", got)
	}
	if msg := git(t, dir, "log", "-1", "--format=%s"); !strings.HasSuffix(msg, "[poultice]") {
		t.Errorf("checkpoint subject = %q", msg)
	}
	if status := git(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("working tree not clean after heal:\n%s", status)
	}
}

// assertUnverified checks the rollback contract: nothing kept, HEAD unmoved,
// every byte of the working tree as it was.
func assertUnverified(t *testing.T, dir, before string, files map[string]string, rep *engine.Report, failedStep string) {
	t.Helper()
	if rep.Outcome != model.OutcomeUnverified {
		t.Fatalf("outcome = %s, want unverified\nattempts: %+v", rep.Outcome, rep.Attempts)
	}
	if !rep.RolledBack {
		t.Error("RolledBack = false")
	}
	if rep.Verdict == nil || rep.Verdict.Passed {
		t.Fatal("expected a failing verdict on the report")
	}
	if step := rep.Verdict.FailedStep(); step == nil || step.Name != failedStep {
		t.Errorf("failed step = %+v, want %q", step, failedStep)
	}
	if head := git(t, dir, "rev-parse", "HEAD"); head != before {
		t.Errorf("HEAD moved from %s to %s", before, head)
	}
	if after := snapshot(t, dir); !reflect.DeepEqual(after, files) {
		t.Errorf("working tree not restored byte-for-byte")
	}
	if len(rep.FindingsAfter) != len(rep.FindingsBefore) {
		t.Errorf("findings after = %d, want all %d still outstanding", len(rep.FindingsAfter), len(rep.FindingsBefore))
	}
}

func TestGoFormattingHeals(t *testing.T) {
	requireGo(t)
	dir := setup(t, "go-unformatted", "")
	before := git(t, dir, "rev-parse", "HEAD")

	rep := heal(t, dir, "go-formatting")

	assertHealed(t, dir, before, rep)
	if got := read(t, dir, "calc.go"); !strings.Contains(got, "func Add(a int, b int) int {\n\treturn a + b\n}") {
		t.Errorf("calc.go not gofmt-formatted:\n%s", got)
	}
}

// A red suite means no formatting change can be verified, however harmless.
func TestGoFormattingIsUnverifiedOnARedSuite(t *testing.T) {
	requireGo(t)
	dir := setup(t, "go-red-suite", "")
	before := git(t, dir, "rev-parse", "HEAD")
	files := snapshot(t, dir)

	rep := heal(t, dir, "go-formatting")

	assertUnverified(t, dir, before, files, rep, "test")
}

func TestGoModTidyHeals(t *testing.T) {
	requireGo(t)
	if out, _ := exec.Command("go", "help", "mod", "tidy").CombinedOutput(); !strings.Contains(string(out), "-diff") {
		t.Skip("go mod tidy -diff needs Go 1.23+")
	}
	dir := setup(t, "go-untidy", "")
	before := git(t, dir, "rev-parse", "HEAD")

	rep := heal(t, dir, "go-mod-tidy")

	assertHealed(t, dir, before, rep)
	if got := read(t, dir, "go.mod"); strings.Contains(got, "require example.com/dep") {
		t.Errorf("stale requirement survived tidy:\n%s", got)
	}
}

func TestPythonRuffHeals(t *testing.T) {
	dir := setup(t, "python-unused-import", "python")
	before := git(t, dir, "rev-parse", "HEAD")

	rep := heal(t, dir, "python-ruff")

	assertHealed(t, dir, before, rep)
	if len(rep.FindingsBefore) != 1 || rep.FindingsBefore[0].RuleID != "F401" {
		t.Errorf("findings before = %+v, want one F401", rep.FindingsBefore)
	}
	if got := read(t, dir, "app.py"); strings.Contains(got, "import os") {
		t.Errorf("unused import not removed:\n%s", got)
	}
}

// Log4Shell, end to end: Snyk finds it, `snyk fix` upgrades the pom, Maven
// compiles and tests it, and poultice keeps the upgrade.
func TestMavenSnykHealsLog4Shell(t *testing.T) {
	dir := setup(t, "maven-log4shell", "maven")
	before := git(t, dir, "rev-parse", "HEAD")

	rep := heal(t, dir, "maven-snyk-cve")

	assertHealed(t, dir, before, rep)
	if got := read(t, dir, "pom.xml"); !strings.Contains(got, "<version>2.17.1</version>") {
		t.Errorf("pom.xml not upgraded:\n%s", got)
	}
}

// The same correct upgrade, in a project whose own tests reject it. This is
// the property poultice exists for: the fix is discarded, not shipped.
func TestMavenSnykRollsBackWhenTheSuiteRejectsTheUpgrade(t *testing.T) {
	dir := setup(t, "maven-log4shell-pinned", "maven")
	before := git(t, dir, "rev-parse", "HEAD")
	files := snapshot(t, dir)

	rep := heal(t, dir, "maven-snyk-cve")

	assertUnverified(t, dir, before, files, rep, "test")
	if f := rep.FindingsAfter[0]; f.Severity != model.SeverityCritical || f.FixedIn != "2.17.1" {
		t.Errorf("outstanding finding = %+v", f)
	}
}
