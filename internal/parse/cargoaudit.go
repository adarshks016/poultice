package parse

import (
	"encoding/json"
	"strings"

	"github.com/adarshks016/poultice/internal/model"
)

func init() {
	Register("cargo-audit-json", parseCargoAuditJSON)
}

// cargo audit --json emits a single object. Only the "vulnerabilities" list
// is actionable; "warnings" (unmaintained, unsound) are informational and not
// surfaced as findings to avoid noise on repositories that cannot act on them.
type cargoAuditReport struct {
	Vulnerabilities cargoVulnBlock `json:"vulnerabilities"`
}

type cargoVulnBlock struct {
	List []cargoVulnEntry `json:"list"`
}

type cargoVulnEntry struct {
	Advisory cargoAdvisory `json:"advisory"`
	Package  cargoPkg      `json:"package"`
	Versions cargoVersions `json:"versions"`
}

type cargoAdvisory struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Severity    string   `json:"severity"`
	URL         string   `json:"url"`
	Description string   `json:"description"`
	Patched     []string `json:"patched"`
}

type cargoPkg struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type cargoVersions struct {
	Patched []string `json:"patched"`
}

func parseCargoAuditJSON(in Input) (model.Findings, error) {
	raw := strings.TrimSpace(in.Output)
	if raw == "" {
		return nil, nil
	}

	var report cargoAuditReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil, nil // tolerate non-JSON preamble from older cargo-audit versions
	}

	var out model.Findings
	for _, entry := range report.Vulnerabilities.List {
		adv := entry.Advisory
		pkg := entry.Package

		fixedIn := ""
		// Prefer the versions block; fall back to the advisory patched list.
		patched := entry.Versions.Patched
		if len(patched) == 0 {
			patched = adv.Patched
		}
		if len(patched) > 0 {
			fixedIn = patched[0]
		}

		msg := adv.Title
		if msg == "" {
			msg = adv.Description
		}

		out = append(out, model.Finding{
			RuleID:          adv.ID,
			Message:         strings.TrimSpace(msg),
			Severity:        model.ParseSeverity(adv.Severity),
			Package:         pkg.Name,
			Version:         pkg.Version,
			FixedIn:         fixedIn,
			NativelyFixable: fixedIn != "",
			Source:          "cargo-audit",
		})
	}
	return out.Dedupe(), nil
}
