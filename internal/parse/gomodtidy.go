package parse

import (
	"fmt"
	"strings"

	"github.com/adarshks016/poultice/internal/model"
)

func init() {
	Register("go-mod-tidy-diff", parseGoModTidyDiff)
}

// parseGoModTidyDiff reads `go mod tidy -diff` (Go 1.23+), which changes
// nothing on disk and prints one unified diff per file tidy would rewrite:
//
//	diff current/go.mod tidy/go.mod
//	--- current/go.mod
//	+++ tidy/go.mod
//
// It exits 1 both when the module is untidy and when tidy itself fails, so a
// non-zero exit with no diff is an error to surface, not a clean bill of health.
func parseGoModTidyDiff(in Input) (model.Findings, error) {
	type pending struct {
		file           string
		added, removed int
	}
	var files []*pending
	for _, line := range strings.Split(in.Output, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "diff current/"):
			fields := strings.Fields(line)
			files = append(files, &pending{file: strings.TrimPrefix(fields[1], "current/")})
		case len(files) == 0,
			strings.HasPrefix(line, "+++ "),
			strings.HasPrefix(line, "--- "):
		case strings.HasPrefix(line, "+"):
			files[len(files)-1].added++
		case strings.HasPrefix(line, "-"):
			files[len(files)-1].removed++
		}
	}

	if len(files) == 0 {
		if in.ExitCode == 0 {
			return nil, nil
		}
		out := strings.TrimSpace(in.Output)
		if strings.Contains(out, "flag provided but not defined: -diff") {
			return nil, fmt.Errorf("go mod tidy -diff needs Go 1.23 or newer; " +
				"upgrade the toolchain or skip the go-mod-tidy recipe")
		}
		return nil, fmt.Errorf("go mod tidy -diff exited %d: %s", in.ExitCode, out)
	}

	out := make(model.Findings, 0, len(files))
	for _, f := range files {
		out = append(out, model.Finding{
			RuleID: "go-mod-tidy",
			Message: fmt.Sprintf("%s is out of sync with the code: tidy would add %d and remove %d line(s)",
				f.file, f.added, f.removed),
			Severity:        model.SeverityLow,
			File:            f.file,
			NativelyFixable: true,
			Source:          "go-mod-tidy",
		})
	}
	return out, nil
}
