package parse

import (
	"testing"

	"github.com/adarshks016/poultice/internal/model"
)

const cargoAuditSample = `{
  "database": {"advisory-count": 512},
  "vulnerabilities": {
    "found": true,
    "count": 2,
    "list": [
      {
        "advisory": {
          "id": "RUSTSEC-2021-0001",
          "title": "Memory corruption in smallvec",
          "severity": "critical",
          "url": "https://rustsec.org/advisories/RUSTSEC-2021-0001.html",
          "description": "A buffer overflow exists in smallvec.",
          "patched": []
        },
        "package": {"name": "smallvec", "version": "0.6.13"},
        "versions": {"patched": ["^0.6.14", "^1.6.1"], "unaffected": []}
      },
      {
        "advisory": {
          "id": "RUSTSEC-2020-0071",
          "title": "Time-of-check time-of-use in time crate",
          "severity": "high",
          "url": "https://rustsec.org/advisories/RUSTSEC-2020-0071.html",
          "description": "TOCTOU vulnerability in the time crate.",
          "patched": [">=0.2.23"]
        },
        "package": {"name": "time", "version": "0.1.44"},
        "versions": {"patched": [], "unaffected": []}
      }
    ]
  },
  "warnings": {}
}`

const cargoAuditClean = `{
  "vulnerabilities": {"found": false, "count": 0, "list": []},
  "warnings": {}
}`

func TestParseCargoAuditJSON(t *testing.T) {
	p, err := Get("cargo-audit-json")
	if err != nil {
		t.Fatalf("parser not registered: %v", err)
	}

	findings, err := p(Input{Output: cargoAuditSample})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(findings), findings)
	}

	byID := map[string]model.Finding{}
	for _, f := range findings {
		byID[f.RuleID] = f
	}

	sv := byID["RUSTSEC-2021-0001"]
	if sv.Severity != model.SeverityCritical {
		t.Errorf("smallvec severity = %s, want critical", sv.Severity)
	}
	if sv.Package != "smallvec" {
		t.Errorf("package = %q", sv.Package)
	}
	if sv.Version != "0.6.13" {
		t.Errorf("version = %q", sv.Version)
	}
	// fixedIn should come from versions.patched, not advisory.patched (which is empty)
	if sv.FixedIn != "^0.6.14" {
		t.Errorf("fixedIn = %q, want ^0.6.14", sv.FixedIn)
	}
	if !sv.NativelyFixable {
		t.Error("smallvec should be natively fixable (patch exists)")
	}
	if sv.Source != "cargo-audit" {
		t.Errorf("source = %q", sv.Source)
	}

	tm := byID["RUSTSEC-2020-0071"]
	if tm.Severity != model.SeverityHigh {
		t.Errorf("time severity = %s, want high", tm.Severity)
	}
	// fixedIn should fall back to advisory.patched when versions.patched is empty
	if tm.FixedIn != ">=0.2.23" {
		t.Errorf("time fixedIn = %q, want >=0.2.23", tm.FixedIn)
	}
}

func TestParseCargoAuditJSONClean(t *testing.T) {
	p, _ := Get("cargo-audit-json")
	findings, err := p(Input{Output: cargoAuditClean})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("want 0 findings for clean audit, got %d", len(findings))
	}
}

func TestParseCargoAuditJSONEmpty(t *testing.T) {
	p, _ := Get("cargo-audit-json")
	findings, err := p(Input{Output: ""})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("want 0 findings for empty output, got %d", len(findings))
	}
}
