package parse

import (
	"testing"

	"github.com/adarshks016/poultice/internal/model"
)

// Minimal govulncheck -json output with one actionable finding (trace present)
// and one OSV advisory envelope. Real output mixes "osv" and "finding" lines in
// any order; our parser accumulates both before correlating.
const govulnSample = `
{"osv":{"id":"GO-2023-1558","summary":"Denial of service in net/http","severity":[{"type":"CVSS_V3","score":"7.5"}],"affected":[{"package":{"name":"stdlib"},"ranges":[{"events":[{"introduced":"0"},{"fixed":"1.20.7"}]}]}]}}
{"finding":{"osv":"GO-2023-1558","trace":[{"module":"golang.org/x/net","version":"v0.7.0","package":"golang.org/x/net/http2"},{"module":"example.com/myapp","package":"example.com/myapp/server","function":"ListenAndServe","position":{"filename":"server/main.go","line":42}}]}}
`

// A finding with no trace must be silently dropped — govulncheck emits these for
// code paths that are reachable in the dependency graph but never called.
const govulnNoTrace = `
{"osv":{"id":"GO-2023-0001","summary":"Some vuln"}}
{"finding":{"osv":"GO-2023-0001","trace":[]}}
`

func TestParseGovulncheckJSON(t *testing.T) {
	p, err := Get("govulncheck-json")
	if err != nil {
		t.Fatalf("parser not registered: %v", err)
	}

	findings, err := p(Input{Output: govulnSample})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(findings), findings)
	}

	f := findings[0]
	if f.RuleID != "GO-2023-1558" {
		t.Errorf("RuleID = %q, want GO-2023-1558", f.RuleID)
	}
	if f.Message != "Denial of service in net/http" {
		t.Errorf("Message = %q", f.Message)
	}
	if f.Severity != model.SeverityHigh {
		t.Errorf("Severity = %s, want high (CVSS 7.5)", f.Severity)
	}
	if f.File != "server/main.go" {
		t.Errorf("File = %q, want server/main.go", f.File)
	}
	if f.Line != 42 {
		t.Errorf("Line = %d, want 42", f.Line)
	}
	if f.FixedIn != "1.20.7" {
		t.Errorf("FixedIn = %q, want 1.20.7", f.FixedIn)
	}
	if f.Source != "govulncheck" {
		t.Errorf("Source = %q, want govulncheck", f.Source)
	}
}

func TestParseGovulncheckDropsNoTraceFinding(t *testing.T) {
	p, _ := Get("govulncheck-json")
	findings, err := p(Input{Output: govulnNoTrace})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("want 0 findings for trace-less entry, got %d: %+v", len(findings), findings)
	}
}

func TestParseGovulncheckEmptyOutput(t *testing.T) {
	p, _ := Get("govulncheck-json")
	findings, err := p(Input{Output: ""})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("want 0 findings for empty output, got %d", len(findings))
	}
}

func TestScoreToSeverity(t *testing.T) {
	cases := []struct {
		score string
		want  model.Severity
	}{
		{"9.8", model.SeverityCritical},
		{"10", model.SeverityCritical},
		{"10.0", model.SeverityCritical},
		{"8.1", model.SeverityHigh},
		{"7.0", model.SeverityHigh},
		{"6.9", model.SeverityMedium},
		{"4.0", model.SeverityMedium},
		{"3.9", model.SeverityLow},
		{"0.0", model.SeverityLow},
		{"", model.SeverityUnknown},
	}
	for _, c := range cases {
		if got := scoreToSeverity(c.score); got != c.want {
			t.Errorf("scoreToSeverity(%q) = %s, want %s", c.score, got, c.want)
		}
	}
}
