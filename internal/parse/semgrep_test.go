package parse

import (
	"strings"
	"testing"

	"github.com/adarshks016/poultice/internal/model"
)

const semgrepSample = `{
  "results": [
    {
      "check_id": "python.lang.security.audit.exec-use",
      "path": "app/server.py",
      "start": {"line": 42, "col": 5},
      "end":   {"line": 42, "col": 20},
      "extra": {
        "message": "Use of exec() is a security risk.",
        "severity": "ERROR",
        "metadata": {
          "cwe": ["CWE-78"],
          "confidence": "HIGH",
          "impact": "HIGH"
        }
      }
    },
    {
      "check_id": "python.lang.maintainability.useless-ifmain",
      "path": "app/main.py",
      "start": {"line": 1, "col": 1},
      "end":   {"line": 1, "col": 5},
      "extra": {
        "message": "Useless if __name__ == '__main__' block.",
        "severity": "WARNING",
        "metadata": {}
      }
    }
  ],
  "errors": []
}`

// Duplicate of the first result — deduplication must collapse it.
const semgrepDupeSample = `{
  "results": [
    {
      "check_id": "rule.x",
      "path": "f.py",
      "start": {"line": 1},
      "extra": {"message": "msg", "severity": "ERROR", "metadata": {}}
    },
    {
      "check_id": "rule.x",
      "path": "f.py",
      "start": {"line": 1},
      "extra": {"message": "msg", "severity": "ERROR", "metadata": {}}
    }
  ]
}`

func TestParseSemgrepJSON(t *testing.T) {
	p, err := Get("semgrep-json")
	if err != nil {
		t.Fatalf("parser not registered: %v", err)
	}

	findings, err := p(Input{Output: semgrepSample})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(findings), findings)
	}

	byRule := map[string]model.Finding{}
	for _, f := range findings {
		byRule[f.RuleID] = f
	}

	exec := byRule["python.lang.security.audit.exec-use"]
	if exec.Severity != model.SeverityHigh {
		t.Errorf("exec severity = %s, want high (ERROR)", exec.Severity)
	}
	if exec.File != "app/server.py" {
		t.Errorf("exec file = %q", exec.File)
	}
	if exec.Line != 42 {
		t.Errorf("exec line = %d, want 42", exec.Line)
	}
	if exec.Source != "semgrep" {
		t.Errorf("exec source = %q", exec.Source)
	}
	if !strings.Contains(exec.Message, "exec()") {
		t.Errorf("exec message = %q", exec.Message)
	}

	warn := byRule["python.lang.maintainability.useless-ifmain"]
	if warn.Severity != model.SeverityMedium {
		t.Errorf("warning severity = %s, want medium (WARNING)", warn.Severity)
	}
}

func TestParseSemgrepJSONDeduplicates(t *testing.T) {
	p, _ := Get("semgrep-json")
	findings, err := p(Input{Output: semgrepDupeSample})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 1 {
		t.Errorf("want 1 finding after dedup, got %d", len(findings))
	}
}

func TestParseSemgrepJSONEmpty(t *testing.T) {
	p, _ := Get("semgrep-json")
	findings, err := p(Input{Output: ""})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("want 0 findings for empty output, got %d", len(findings))
	}
}

func TestSemgrepSeverityFallback(t *testing.T) {
	cases := []struct {
		raw  string
		meta semgrepMetadata
		want model.Severity
	}{
		{"ERROR", semgrepMetadata{}, model.SeverityHigh},
		{"WARNING", semgrepMetadata{}, model.SeverityMedium},
		{"INFO", semgrepMetadata{}, model.SeverityLow},
		// Unknown raw → fall back to metadata impact
		{"", semgrepMetadata{Impact: "HIGH"}, model.SeverityHigh},
		// Unknown raw + unknown impact + CWE + high confidence → medium
		{"", semgrepMetadata{CWE: []string{"CWE-79"}, Confidence: "HIGH"}, model.SeverityMedium},
		// Nothing known → low
		{"", semgrepMetadata{}, model.SeverityLow},
	}
	for _, c := range cases {
		if got := semgrepSeverity(c.raw, c.meta); got != c.want {
			t.Errorf("semgrepSeverity(%q, %+v) = %s, want %s", c.raw, c.meta, got, c.want)
		}
	}
}
