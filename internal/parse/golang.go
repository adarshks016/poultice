package parse

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/adarshks016/poultice/internal/model"
)

func init() {
	Register("gofmt-list", parseGofmtList)
	Register("go-vet", parseGoVet)
	Register("go-build", parseGoBuild)
}

// parseGofmtList reads `gofmt -l`, which prints one unformatted file per line.
func parseGofmtList(in Input) (model.Findings, error) {
	var out model.Findings
	for _, line := range strings.Split(in.Output, "\n") {
		file := strings.TrimSpace(line)
		if file == "" {
			continue
		}
		out = append(out, model.Finding{
			RuleID:          "gofmt",
			Message:         "file is not gofmt-formatted",
			Severity:        model.SeverityLow,
			File:            relativize(in.RepoDir, file),
			NativelyFixable: true,
			Source:          "gofmt",
		})
	}
	return out, nil
}

// goDiagLine matches the `file:line:col: message` shape shared by go vet and
// the Go compiler.
var goDiagLine = regexp.MustCompile(`^(.+?\.go):(\d+):(?:(\d+):)?\s+(.*)$`)

func parseGoVet(in Input) (model.Findings, error) {
	return parseGoDiagnostics(in, "go-vet", model.SeverityMedium)
}

func parseGoBuild(in Input) (model.Findings, error) {
	return parseGoDiagnostics(in, "go-build", model.SeverityHigh)
}

func parseGoDiagnostics(in Input, source string, sev model.Severity) (model.Findings, error) {
	var out model.Findings
	for _, line := range strings.Split(in.Output, "\n") {
		line = strings.TrimRight(line, "\r")
		m := goDiagLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[2])
		out = append(out, model.Finding{
			RuleID:   source,
			Message:  strings.TrimSpace(m[4]),
			Severity: sev,
			File:     relativize(in.RepoDir, m[1]),
			Line:     lineNo,
			// Neither vet findings nor compile errors have a deterministic fixer.
			NativelyFixable: false,
			Source:          source,
		})
	}
	return out, nil
}

// relativize converts an absolute path under root into a repo-relative one,
// leaving anything else untouched.
func relativize(root, path string) string {
	if root == "" || !filepath.IsAbs(path) {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
