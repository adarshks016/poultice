// Package policy enforces the blast radius of a fix.
//
// A strategy may propose anything; policy decides what is allowed to survive.
// Every check here exists because some real automated-fix tool has, at some
// point, done the thing it prevents.
package policy

import (
	"fmt"
	"path"
	"strings"

	"github.com/adarshks016/poultice/internal/recipe"
)

// Violation explains why a change set was rejected.
type Violation struct {
	Reason string
	Files  []string
}

func (v *Violation) Error() string {
	if len(v.Files) == 0 {
		return v.Reason
	}
	return fmt.Sprintf("%s: %s", v.Reason, strings.Join(v.Files, ", "))
}

// Check validates a set of changed paths and diff size against a policy.
// It returns nil when the change set is acceptable.
func Check(p recipe.Policy, changed []string, changedLines int) error {
	var denied []string
	for _, f := range changed {
		if matchAny(p.DenyPaths, f) {
			denied = append(denied, f)
		}
	}
	if len(denied) > 0 {
		return &Violation{Reason: "patch touches denied paths", Files: denied}
	}

	if len(p.AllowPaths) > 0 {
		var outside []string
		for _, f := range changed {
			if !matchAny(p.AllowPaths, f) {
				outside = append(outside, f)
			}
		}
		if len(outside) > 0 {
			return &Violation{Reason: "patch touches paths outside policy.allowPaths", Files: outside}
		}
	}

	if p.MaxChangedFiles > 0 && len(changed) > p.MaxChangedFiles {
		return &Violation{Reason: fmt.Sprintf(
			"patch changes %d files, policy allows %d", len(changed), p.MaxChangedFiles)}
	}
	if p.MaxChangedLines > 0 && changedLines > p.MaxChangedLines {
		return &Violation{Reason: fmt.Sprintf(
			"patch changes %d lines, policy allows %d", changedLines, p.MaxChangedLines)}
	}
	return nil
}

// matchAny reports whether the path matches any glob pattern.
func matchAny(patterns []string, p string) bool {
	p = path.Clean(strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "./"))
	for _, pat := range patterns {
		if Match(pat, p) {
			return true
		}
	}
	return false
}

// Match implements glob matching with `**` support, which path.Match lacks.
//
// `**` matches across separators; `*` and `?` do not. A pattern with no slash
// is also tried against the basename, so `*.pem` matches `certs/server.pem`.
func Match(pattern, name string) bool {
	pattern = strings.TrimPrefix(pattern, "./")
	if !strings.Contains(pattern, "/") {
		if ok, _ := path.Match(pattern, path.Base(name)); ok {
			return true
		}
	}
	return deepMatch(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

// deepMatch walks pattern and name segments, treating `**` as "zero or more
// segments" and backtracking when a greedy match fails.
func deepMatch(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Trailing `**` matches everything that remains, including nothing.
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if deepMatch(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
