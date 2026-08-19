package parse

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/adarshks016/poultice/internal/model"
)

func init() {
	Register("npm-audit-json", parseNPMAuditJSON)
}

// npm audit --json emits different shapes depending on the npm major version:
//   - npm 6:  top-level "advisories" map keyed by advisory ID
//   - npm 7+: top-level "vulnerabilities" map keyed by package name
//
// We handle both so the recipe works regardless of which npm is installed.
type npmAuditReport struct {
	// npm 7+ shape
	Vulnerabilities map[string]npmVuln `json:"vulnerabilities"`
	// npm 6 shape
	Advisories map[string]npmAdvisory `json:"advisories"`
}

// npmVuln is a npm 7+ vulnerability entry.
type npmVuln struct {
	Name         string      `json:"name"`
	Severity     string      `json:"severity"`
	Via          []npmVia    `json:"via"`
	Effects      []string    `json:"effects"`
	FixAvailable interface{} `json:"fixAvailable"` // bool or object
}

// npmVia is either a string (transitive dep name) or an advisory object.
// npm mixes both in the same array, so we decode via raw JSON.
type npmVia struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	Severity   string `json:"severity"`
	Range      string `json:"range"`
	Dependency string `json:"dependency"`
}

// npmAdvisory is the npm 6 advisory shape.
type npmAdvisory struct {
	ID                 int    `json:"id"`
	Title              string `json:"title"`
	Severity           string `json:"severity"`
	ModuleName         string `json:"module_name"`
	VulnerableVersions string `json:"vulnerable_versions"`
	PatchedVersions    string `json:"patched_versions"`
	URL                string `json:"url"`
}

func parseNPMAuditJSON(in Input) (model.Findings, error) {
	raw := []byte(strings.TrimSpace(in.Output))
	if in.ArtifactPath != "" {
		b, err := os.ReadFile(in.ArtifactPath)
		if err != nil {
			return nil, fmt.Errorf("read npm audit report %s: %w", in.ArtifactPath, err)
		}
		raw = []byte(strings.TrimSpace(string(b)))
	}
	if len(raw) == 0 {
		return nil, nil
	}

	var report npmAuditReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("decode npm audit output: %w", err)
	}

	var out model.Findings

	// npm 7+ path
	for pkgName, v := range report.Vulnerabilities {
		// Skip pure transitive entries that are only listed because a direct dep
		// pulls them in — the direct dep entry already covers the finding.
		if len(v.Via) == 0 {
			continue
		}

		title, ruleID, fixedIn := npmVulnDetails(v, pkgName)
		fixable := npmIsFixable(v.FixAvailable)

		out = append(out, model.Finding{
			RuleID:          ruleID,
			Message:         title,
			Severity:        model.ParseSeverity(v.Severity),
			Package:         pkgName,
			FixedIn:         fixedIn,
			NativelyFixable: fixable,
			Source:          "npm-audit",
		})
	}

	// npm 6 path
	for _, adv := range report.Advisories {
		fixedIn := adv.PatchedVersions
		if fixedIn == "<0.0.0" {
			fixedIn = "" // npm uses this sentinel when no fix exists
		}
		out = append(out, model.Finding{
			RuleID:          fmt.Sprintf("NPMSA-%d", adv.ID),
			Message:         adv.Title,
			Severity:        model.ParseSeverity(adv.Severity),
			Package:         adv.ModuleName,
			FixedIn:         fixedIn,
			NativelyFixable: fixedIn != "",
			Source:          "npm-audit",
		})
	}

	return out.Dedupe(), nil
}

// npmVulnDetails extracts the human-readable title, a stable rule ID, and the
// first patched version from a npm 7+ vulnerability entry.
func npmVulnDetails(v npmVuln, pkgName string) (title, ruleID, fixedIn string) {
	title = pkgName + " vulnerability"
	ruleID = "NPM-" + strings.ToUpper(strings.ReplaceAll(pkgName, "-", "_"))

	for _, via := range v.Via {
		if via.Title != "" {
			title = via.Title
		}
		// npm 7+ advisory URLs carry the advisory ID: .../advisories/1234
		if via.URL != "" {
			parts := strings.Split(strings.TrimRight(via.URL, "/"), "/")
			if len(parts) > 0 {
				ruleID = "NPMSA-" + parts[len(parts)-1]
			}
		}
		if via.Range != "" && fixedIn == "" {
			fixedIn = via.Range
		}
	}
	return title, ruleID, fixedIn
}

func npmIsFixable(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case map[string]interface{}:
		return len(val) > 0
	}
	return false
}
