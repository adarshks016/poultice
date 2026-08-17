package parse

import (
	"encoding/json"
	"strings"

	"github.com/adarshks016/poultice/internal/model"
)

func init() {
	Register("govulncheck-json", parseGovulncheckJSON)
}

// govulncheck -json emits one JSON object per line. Each relevant finding is a
// "finding" entry whose traces list call stacks that reach a vulnerable symbol.
// Only findings with at least one trace are actionable (the others are
// "dependency reachability" warnings govulncheck emits even for code paths that
// never call the vulnerable symbol, which Snyk doesn't produce and which create
// noise in PRs).
type govulnFinding struct {
	OSV   string        `json:"osv"`
	Trace []govulnFrame `json:"trace"`
}

type govulnFrame struct {
	Module   string     `json:"module"`
	Version  string     `json:"version"`
	Package  string     `json:"package"`
	Function string     `json:"function,omitempty"`
	Position *govulnPos `json:"position,omitempty"`
}

type govulnPos struct {
	Filename string `json:"filename"`
	Line     int    `json:"line"`
}

// govulnOSV carries the advisory detail emitted in the "osv" envelope lines.
type govulnOSV struct {
	ID       string           `json:"id"`
	Summary  string           `json:"summary"`
	Severity []govulnSeverity `json:"severity"`
	Affected []govulnAffected `json:"affected"`
}

type govulnSeverity struct {
	Type  string `json:"type"`
	Score string `json:"score"`
}

type govulnAffected struct {
	Package govulnPkg     `json:"package"`
	Ranges  []govulnRange `json:"ranges"`
}

type govulnPkg struct {
	Name string `json:"name"`
}

type govulnRange struct {
	Events []govulnEvent `json:"events"`
}

type govulnEvent struct {
	Fixed string `json:"fixed,omitempty"`
}

// govulnLine is the top-level wrapper govulncheck emits on each stdout line.
type govulnLine struct {
	Message *govulnFinding `json:"finding,omitempty"`
	OSV     *govulnOSV     `json:"osv,omitempty"`
}

func parseGovulncheckJSON(in Input) (model.Findings, error) {
	advisories := map[string]*govulnOSV{}
	var findings []govulnFinding

	for _, line := range strings.Split(strings.TrimSpace(in.Output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var row govulnLine
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			continue // tolerate partial/malformed output from interrupted runs
		}
		if row.OSV != nil {
			advisories[row.OSV.ID] = row.OSV
		}
		if row.Message != nil && len(row.Message.Trace) > 0 {
			findings = append(findings, *row.Message)
		}
	}

	var out model.Findings
	for _, f := range findings {
		if len(f.Trace) == 0 {
			continue
		}
		vuln := f.Trace[0] // outermost frame: the vulnerable module
		osv := advisories[f.OSV]

		sev := model.SeverityHigh // govulncheck doesn't normalize severity; high is a safe default
		summary := f.OSV
		fixedIn := ""
		pkg := vuln.Module

		if osv != nil {
			if osv.Summary != "" {
				summary = osv.Summary
			}
			// Use the first CVSS score to pick a severity.
			for _, s := range osv.Severity {
				if parsed := scoreToSeverity(s.Score); parsed != model.SeverityUnknown {
					sev = parsed
					break
				}
			}
			// First fixed version across all affected ranges.
			for _, aff := range osv.Affected {
				if fixedIn != "" {
					break
				}
				for _, r := range aff.Ranges {
					for _, ev := range r.Events {
						if ev.Fixed != "" {
							fixedIn = ev.Fixed
							break
						}
					}
				}
			}
		}

		// Prefer the package name from the OSV affected list when available.
		if osv != nil && len(osv.Affected) > 0 && osv.Affected[0].Package.Name != "" {
			pkg = osv.Affected[0].Package.Name
		}

		file := ""
		line := 0
		// Walk the trace for the first caller with a concrete file position.
		for _, frame := range f.Trace[1:] {
			if frame.Position != nil && frame.Position.Filename != "" {
				file = frame.Position.Filename
				line = frame.Position.Line
				break
			}
		}

		out = append(out, model.Finding{
			RuleID:   f.OSV,
			Message:  summary,
			Severity: sev,
			File:     file,
			Line:     line,
			Package:  pkg,
			Version:  vuln.Version,
			FixedIn:  fixedIn,
			Source:   "govulncheck",
		})
	}
	return out.Dedupe(), nil
}

// scoreToSeverity maps a CVSS v3 base score string ("7.5") to a Severity.
// govulncheck only emits CVSS v3; we do not attempt to parse v2.
func scoreToSeverity(score string) model.Severity {
	if score == "" {
		return model.SeverityUnknown
	}
	// Handle the exact maximum first so the single-digit switch below is clean.
	if score == "10" || score == "10.0" {
		return model.SeverityCritical
	}
	// CVSS v3: 0.0–3.9 low, 4.0–6.9 medium, 7.0–8.9 high, 9.0–9.9 critical.
	major := score[0]
	switch {
	case major == '9':
		return model.SeverityCritical
	case major == '7' || major == '8':
		return model.SeverityHigh
	case major == '4' || major == '5' || major == '6':
		return model.SeverityMedium
	case major >= '0' && major <= '3':
		return model.SeverityLow
	default:
		return model.SeverityUnknown
	}
}
