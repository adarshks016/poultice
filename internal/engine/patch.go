package engine

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adarshks016/poultice/internal/exec"
	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/policy"
	"github.com/adarshks016/poultice/internal/recipe"
)

// applyPatch validates a unified diff and applies it.
//
// The two-phase check-then-apply is the point of this function. A language
// model that emits a truncated or hallucinated hunk fails `git apply --check`
// and never touches the working tree. Whole-file replacement — the approach
// most naive AI-fix scripts take — cannot fail this way, which is precisely why
// it silently corrupts files.
func applyPatch(ctx context.Context, r *exec.Runner, diff string) error {
	diff = strings.TrimSpace(diff)
	if diff == "" {
		return fmt.Errorf("empty patch")
	}
	if !strings.Contains(diff, "@@") {
		return fmt.Errorf("patch is not a unified diff (no hunk header found)")
	}

	tmp, err := os.CreateTemp("", "poultice-*.patch")
	if err != nil {
		return fmt.Errorf("create patch file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(diff + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write patch file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close patch file: %w", err)
	}

	quoted := "'" + strings.ReplaceAll(tmp.Name(), "'", `'\''`) + "'"

	check, err := r.Run(ctx, "git apply --check --verbose "+quoted, time.Minute)
	if err != nil {
		return fmt.Errorf("git apply --check: %w", err)
	}
	if !check.Success() {
		return fmt.Errorf("patch does not apply cleanly: %s", check.Tail(1500))
	}

	res, err := r.Run(ctx, "git apply "+quoted, time.Minute)
	if err != nil {
		return fmt.Errorf("git apply: %w", err)
	}
	if !res.Success() {
		return fmt.Errorf("git apply failed: %s", res.Tail(1500))
	}
	return nil
}

// collectContext reads the files a model may look at, bounded by the strategy's
// byte budget.
//
// Files named by findings come first, because they are the ones that matter;
// the budget is spent on relevance rather than on whatever the filesystem walk
// happened to reach first. Truncation is explicit and marked, never silent.
func collectContext(root string, cc recipe.Context, findings model.Findings) (map[string]string, error) {
	budget := cc.MaxBytes
	if budget <= 0 {
		budget = 60_000
	}

	ordered := prioritizedPaths(root, cc.Include, findings)
	out := make(map[string]string, len(ordered))
	used := 0

	for _, rel := range ordered {
		if used >= budget {
			break
		}
		abs := filepath.Join(root, rel)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		remaining := budget - used
		if len(data) > remaining {
			data = append(data[:remaining], []byte("\n… [truncated by poultice context budget]\n")...)
		}
		out[rel] = string(data)
		used += len(data)
	}
	return out, nil
}

// prioritizedPaths returns include-matching repo paths, files referenced by
// findings first, then the rest in stable order.
func prioritizedPaths(root string, include []string, findings model.Findings) []string {
	matches := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable subtrees are skipped, not fatal
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", "node_modules", "target", "build", "dist", "vendor",
				".venv", "venv", "__pycache__", ".gradle", ".mvn":
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		for _, pat := range include {
			if policy.Match(pat, rel) {
				matches[rel] = true
				break
			}
		}
		return nil
	})

	var priority, rest []string
	seen := map[string]bool{}
	for _, f := range findings {
		if f.File != "" && matches[f.File] && !seen[f.File] {
			priority = append(priority, f.File)
			seen[f.File] = true
		}
	}
	for rel := range matches {
		if !seen[rel] {
			rest = append(rest, rel)
		}
	}
	sort.Strings(rest)
	return append(priority, rest...)
}

// globRepo counts files matching a glob pattern beneath root.
func globRepo(root, pattern string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "target", "build", "dist", "vendor",
				".venv", "venv", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		if policy.Match(pattern, filepath.ToSlash(rel)) {
			count++
		}
		return nil
	})
	return count, err
}
