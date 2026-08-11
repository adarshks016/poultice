package parse

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adarshks016/poultice/internal/model"
)

func init() {
	Register("ruff-json", parseRuffJSON)
}

type ruffDiagnostic struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Filename string `json:"filename"`
	Location struct {
		Row int `json:"row"`
	} `json:"location"`
	Fix *struct {
		Applicability string `json:"applicability"`
	} `json:"fix"`
}

func parseRuffJSON(in Input) (model.Findings, error) {
	raw := strings.TrimSpace(in.Output)
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var diags []ruffDiagnostic
	if err := json.Unmarshal([]byte(raw), &diags); err != nil {
		return nil, fmt.Errorf("decode ruff json: %w", err)
	}
	out := make(model.Findings, 0, len(diags))
	for _, d := range diags {
		out = append(out, model.Finding{
			RuleID:   d.Code,
			Message:  d.Message,
			Severity: ruffSeverity(d.Code),
			File:     relativize(in.RepoDir, d.Filename),
			Line:     d.Location.Row,
			// Ruff distinguishes safe from unsafe fixes; only safe ones are
			// applied by `ruff check --fix` without an extra opt-in flag.
			NativelyFixable: d.Fix != nil && d.Fix.Applicability == "safe",
			Source:          "ruff",
		})
	}
	return out, nil
}

// ruffSeverity maps rule prefixes onto normalized severities. Ruff itself has
// no severity concept, so this encodes the conventional reading: security and
// bug-prone rules outrank style.
func ruffSeverity(code string) model.Severity {
	switch {
	case strings.HasPrefix(code, "S"): // flake8-bandit, security
		return model.SeverityHigh
	case strings.HasPrefix(code, "B"), // flake8-bugbear
		strings.HasPrefix(code, "F"): // pyflakes, real errors
		return model.SeverityMedium
	default:
		return model.SeverityLow
	}
}
