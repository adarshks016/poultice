package gitutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestParsePorcelainZ(t *testing.T) {
	out := " M a.go\x00?? new dir/b.go\x00R  dst.yml\x00.github/workflows/ci.yml\x00 D gone.txt\x00"
	got := parsePorcelainZ(out)
	want := []string{"a.go", "new dir/b.go", "dst.yml", ".github/workflows/ci.yml", "gone.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newRepo(t *testing.T, files map[string]string) (string, *Repo) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "add", "-A")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial")
	return dir, Open(dir)
}

// A staged rename must report its source path, or moving a file out of a
// denied directory would slip past the policy check.
func TestChangedFilesReportsBothSidesOfARename(t *testing.T) {
	dir, repo := newRepo(t, map[string]string{".github/workflows/ci.yml": "on: push\n"})
	run(t, dir, "mv", ".github/workflows/ci.yml", "ci.yml")

	ctx := context.Background()
	if err := repo.StageAll(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ChangedFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{".github/workflows/ci.yml", "ci.yml"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCheckpointCommitsOnlyTheIndex(t *testing.T) {
	dir, repo := newRepo(t, map[string]string{"a.txt": "one\n"})
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repo.StageAll(ctx); err != nil {
		t.Fatal(err)
	}
	// Written after staging, the way a verifier's byproducts are.
	if err := os.WriteFile(filepath.Join(dir, "late.log"), []byte("noise\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Checkpoint(ctx, "checkpoint"); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "show", "--name-only", "--format=", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "a.txt\n" {
		t.Errorf("checkpoint committed %q, want only a.txt", out)
	}
}
