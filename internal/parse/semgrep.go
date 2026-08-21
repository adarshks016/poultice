package parse

import (
	"encoding/json"
	"strings"

	"github.com/adarshks016/poultice/internal/model"
)

func init() {
	Register("semgrep-json", parseSemgrepJSON)
}

// semgrep --json emits a single object with a "results" array. Each result
// maps to one finding; the "errors" array is informational and not surfaced
// as findings because they represent scan failures, not code problems.
type semgrepReport struct {
	Results []semgrepResult `json:"results"`
}

type semgrepResult struct {
	CheckID string       `json:"check_id"`
	Path    string       `json:"path"`
	Start   semgrepPos   `json:"start"`
	Extra   semgrepExtra `json:"extra"`
}

type semgrepPos struct {
	Line int `json:"line"`
}

type semgrepExtra struct {
	Message  string          `json:"message"`
	Severity string          `json:"severity"`
	Metadata semgrepMetadata `json:"metadata"`
}

type semgrepMetadata struct {
	// Semgrep rule authors use these fields inconsistently, so all are optional.
	Impact     string   `json:"impact"`
	Likelihood string   `json:"likelihood"`
	Confidence string   `json:"confidence"`
	CWE        []string `json:"cwe"`
}

func parseSemgrepJSON(in Input) (model.Findings, error) {
	raw := strings.TrimSpace(in.Output)
	if raw == "" {
		return nil, nil
	}

	var report semgrepReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil, nil // semgrep sometimes emits non-JSON progress lines; tolerate
	}

	var out model.Findings
	for _, r := range report.Results {
		sev := semgrepSeverity(r.Extra.Severity, r.Extra.Metadata)
		out = append(out, model.Finding{
			RuleID:   r.CheckID,
			Message:  strings.TrimSpace(r.Extra.Message),
			Severity: sev,
			File:     r.Path,
			Line:     r.Start.Line,
			Source:   "semgrep",
		})
	}
	return out.Dedupe(), nil
}

// semgrepSeverity maps semgrep's severity string to the normalized model, with
// a fallback that infers from the rule metadata when the top-level field is
// absent or unrecognized. Semgrep rule authors use at least four different
// conventions (ERROR/WARNING/INFO, HIGH/MEDIUM/LOW, CRITICAL, and mixed case),
// so the mapping has to be liberal.
func semgrepSeverity(raw string, meta semgrepMetadata) model.Severity {
	if s := model.ParseSeverity(raw); s != model.SeverityUnknown {
		return s
	}
	// Fall back to the metadata impact field, which is more consistently set.
	if s := model.ParseSeverity(meta.Impact); s != model.SeverityUnknown {
		return s
	}
	// Last resort: anything with a CWE and high confidence is at least medium.
	if len(meta.CWE) > 0 && strings.EqualFold(meta.Confidence, "high") {
		return model.SeverityMedium
	}
	return model.SeverityLow
}
