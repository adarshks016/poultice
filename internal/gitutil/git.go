// Package gitutil provides the small slice of git that the engine needs to
// checkpoint progress and roll back unverifiable work.
//
// The engine never leaves a repository in a state worse than the last verified
// green commit. That guarantee is implemented here.
package gitutil

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adarshks016/poultice/internal/exec"
)

const gitTimeout = 2 * time.Minute

// Repo is a git working tree.
type Repo struct {
	runner *exec.Runner
	dir    string
}

// Open returns a Repo rooted at dir. It does not validate that dir is a
// repository; call IsRepo for that.
func Open(dir string) *Repo {
	return &Repo{runner: exec.New(dir), dir: dir}
}

// Dir returns the working tree root.
func (r *Repo) Dir() string { return r.dir }

func (r *Repo) git(ctx context.Context, args string) (exec.Result, error) {
	return r.runner.Run(ctx, "git "+args, gitTimeout)
}

// IsRepo reports whether dir is inside a git working tree.
func (r *Repo) IsRepo(ctx context.Context) bool {
	res, err := r.git(ctx, "rev-parse --is-inside-work-tree")
	return err == nil && res.Success() && strings.TrimSpace(res.Output) == "true"
}

// Head returns the current commit SHA.
func (r *Repo) Head(ctx context.Context) (string, error) {
	res, err := r.git(ctx, "rev-parse HEAD")
	if err != nil {
		return "", err
	}
	if !res.Success() {
		return "", fmt.Errorf("git rev-parse HEAD: %s", strings.TrimSpace(res.Output))
	}
	return strings.TrimSpace(res.Output), nil
}

// IsClean reports whether the working tree has no changes, tracked or not.
func (r *Repo) IsClean(ctx context.Context) (bool, error) {
	res, err := r.git(ctx, "status --porcelain")
	if err != nil {
		return false, err
	}
	if !res.Success() {
		return false, fmt.Errorf("git status: %s", strings.TrimSpace(res.Output))
	}
	return strings.TrimSpace(res.Output) == "", nil
}

// ChangedFiles lists paths modified relative to HEAD, including untracked ones.
func (r *Repo) ChangedFiles(ctx context.Context) ([]string, error) {
	res, err := r.git(ctx, "status --porcelain=v1 --untracked-files=all")
	if err != nil {
		return nil, err
	}
	if !res.Success() {
		return nil, fmt.Errorf("git status: %s", strings.TrimSpace(res.Output))
	}
	var out []string
	for _, line := range strings.Split(res.Output, "\n") {
		if len(line) < 4 {
			continue
		}
		// Porcelain v1 format: XY<space>path, with renames as "old -> new".
		path := strings.TrimSpace(line[3:])
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		path = strings.Trim(path, `"`)
		if path != "" {
			out = append(out, path)
		}
	}
	return out, nil
}

// DiffStat returns the number of changed files and changed lines against HEAD,
// counting staged, unstaged and untracked content.
func (r *Repo) DiffStat(ctx context.Context) (files, lines int, err error) {
	// Staging everything is the only way to make untracked files visible to
	// `git diff --numstat`. The index is a scratch space here; the engine always
	// either commits or resets afterwards.
	if _, err := r.git(ctx, "add --all"); err != nil {
		return 0, 0, err
	}
	res, err := r.git(ctx, "diff --cached --numstat")
	if err != nil {
		return 0, 0, err
	}
	if !res.Success() {
		return 0, 0, fmt.Errorf("git diff --numstat: %s", strings.TrimSpace(res.Output))
	}
	for _, line := range strings.Split(strings.TrimSpace(res.Output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		files++
		// Binary files report "-" for both counts.
		lines += atoiSafe(fields[0]) + atoiSafe(fields[1])
	}
	return files, lines, nil
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// Checkpoint commits the current working tree state and returns the new SHA.
// Checkpoints are ordinary commits so that the resulting branch reads as a
// reviewable history rather than one opaque squash.
func (r *Repo) Checkpoint(ctx context.Context, message string) (string, error) {
	if _, err := r.git(ctx, "add --all"); err != nil {
		return "", err
	}
	staged, err := r.git(ctx, "diff --cached --quiet")
	if err != nil {
		return "", err
	}
	if staged.Success() {
		// Nothing staged; the checkpoint is a no-op.
		return r.Head(ctx)
	}
	// -c flags keep the commit working on runners with no configured identity,
	// without mutating the user's global git config.
	res, err := r.git(ctx, fmt.Sprintf(
		"-c user.name=poultice -c user.email=poultice@localhost commit --no-verify -m %s",
		shellQuote(message)))
	if err != nil {
		return "", err
	}
	if !res.Success() {
		return "", fmt.Errorf("git commit: %s", strings.TrimSpace(res.Output))
	}
	return r.Head(ctx)
}

// ResetTo discards all working tree and index changes, returning the repository
// to the given commit. This is the rollback half of the safety guarantee.
func (r *Repo) ResetTo(ctx context.Context, sha string) error {
	res, err := r.git(ctx, "reset --hard "+shellQuote(sha))
	if err != nil {
		return err
	}
	if !res.Success() {
		return fmt.Errorf("git reset --hard %s: %s", sha, strings.TrimSpace(res.Output))
	}
	// Untracked files survive a hard reset and would otherwise leak a rejected
	// patch's new files into the next attempt.
	if res, err := r.git(ctx, "clean -fd"); err != nil {
		return err
	} else if !res.Success() {
		return fmt.Errorf("git clean: %s", strings.TrimSpace(res.Output))
	}
	return nil
}

// CreateBranch creates and switches to a new branch at HEAD.
func (r *Repo) CreateBranch(ctx context.Context, name string) error {
	res, err := r.git(ctx, "checkout -b "+shellQuote(name))
	if err != nil {
		return err
	}
	if !res.Success() {
		return fmt.Errorf("git checkout -b %s: %s", name, strings.TrimSpace(res.Output))
	}
	return nil
}

// CurrentBranch returns the checked-out branch name, or "HEAD" when detached.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	res, err := r.git(ctx, "rev-parse --abbrev-ref HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Output), nil
}

// shellQuote wraps a value in single quotes for safe interpolation into the
// `sh -c` command line built by the runner.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
