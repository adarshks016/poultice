package parse

import (
	"testing"

	"github.com/adarshks016/poultice/internal/model"
)

// npm 7+ shape: "vulnerabilities" map.
const npm7Sample = `{
  "vulnerabilities": {
    "lodash": {
      "name": "lodash",
      "severity": "high",
      "via": [
        {
          "title": "Prototype Pollution in lodash",
          "url": "https://github.com/advisories/GHSA-4xc9-xhrj-v574",
          "severity": "high",
          "range": ">=4.17.21"
        }
      ],
      "fixAvailable": true
    },
    "node-forge": {
      "name": "node-forge",
      "severity": "critical",
      "via": [
        {
          "title": "Improper Verification of Cryptographic Signature in node-forge",
          "url": "https://github.com/advisories/GHSA-x4jg-mjrx-434g",
          "severity": "critical",
          "range": ">=1.3.0"
        }
      ],
      "fixAvailable": false
    }
  },
  "metadata": {"vulnerabilities": {"total": 2}}
}`

// npm 6 shape: "advisories" map.
const npm6Sample = `{
  "advisories": {
    "1523": {
      "id": 1523,
      "title": "Arbitrary Code Execution",
      "severity": "critical",
      "module_name": "minimist",
      "vulnerable_versions": "<0.2.1",
      "patched_versions": ">=0.2.1",
      "url": "https://npmjs.com/advisories/1523"
    }
  }
}`

// Clean audit output — no vulnerabilities reported.
const npmCleanSample = `{
  "vulnerabilities": {},
  "metadata": {"vulnerabilities": {"total": 0}}
}`

func TestParseNPMAuditJSON_npm7(t *testing.T) {
	p, err := Get("npm-audit-json")
	if err != nil {
		t.Fatalf("parser not registered: %v", err)
	}

	findings, err := p(Input{Output: npm7Sample})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(findings), findings)
	}

	byPkg := map[string]model.Finding{}
	for _, f := range findings {
		byPkg[f.Package] = f
	}

	lodash := byPkg["lodash"]
	if lodash.Severity != model.SeverityHigh {
		t.Errorf("lodash severity = %s, want high", lodash.Severity)
	}
	if lodash.Source != "npm-audit" {
		t.Errorf("lodash source = %q, want npm-audit", lodash.Source)
	}
	if !lodash.NativelyFixable {
		t.Error("lodash should be natively fixable")
	}
	if lodash.RuleID != "NPMSA-GHSA-4xc9-xhrj-v574" {
		t.Errorf("lodash ruleID = %q", lodash.RuleID)
	}

	forge := byPkg["node-forge"]
	if forge.Severity != model.SeverityCritical {
		t.Errorf("node-forge severity = %s, want critical", forge.Severity)
	}
	if forge.NativelyFixable {
		t.Error("node-forge should not be natively fixable")
	}
}

func TestParseNPMAuditJSON_npm6(t *testing.T) {
	p, _ := Get("npm-audit-json")
	findings, err := p(Input{Output: npm6Sample})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.RuleID != "NPMSA-1523" {
		t.Errorf("RuleID = %q, want NPMSA-1523", f.RuleID)
	}
	if f.Severity != model.SeverityCritical {
		t.Errorf("severity = %s, want critical", f.Severity)
	}
	if f.Package != "minimist" {
		t.Errorf("package = %q, want minimist", f.Package)
	}
	if f.FixedIn != ">=0.2.1" {
		t.Errorf("fixedIn = %q, want >=0.2.1", f.FixedIn)
	}
	if !f.NativelyFixable {
		t.Error("minimist should be natively fixable")
	}
}

func TestParseNPMAuditJSON_clean(t *testing.T) {
	p, _ := Get("npm-audit-json")
	findings, err := p(Input{Output: npmCleanSample})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("want 0 findings for clean audit, got %d", len(findings))
	}
}

func TestParseNPMAuditJSON_empty(t *testing.T) {
	p, _ := Get("npm-audit-json")
	findings, err := p(Input{Output: ""})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("want 0 findings for empty output, got %d", len(findings))
	}
}
