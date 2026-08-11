package parse

import (
	"testing"

	"github.com/adarshks016/poultice/internal/model"
)

func TestRegistryLookup(t *testing.T) {
	if _, err := Get("snyk-json"); err != nil {
		t.Errorf("snyk-json should be registered: %v", err)
	}
	if _, err := Get("not-a-parser"); err == nil {
		t.Error("expected an error for an unknown parser")
	}
	if len(Names()) < 4 {
		t.Errorf("expected several registered parsers, got %v", Names())
	}
}

func TestGofmtList(t *testing.T) {
	p, _ := Get("gofmt-list")
	got, err := p(Input{Output: "main.go\ninternal/x/y.go\n\n"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2", len(got))
	}
	if !got[0].NativelyFixable {
		t.Error("gofmt findings are fixable by gofmt itself")
	}
	if got[1].File != "internal/x/y.go" {
		t.Errorf("file = %q", got[1].File)
	}
}

func TestGoVet(t *testing.T) {
	p, _ := Get("go-vet")
	out := "# demo\n./main.go:12:5: unreachable code\n/abs/repo/other.go:3: something\n"
	got, err := p(Input{Output: out, RepoDir: "/abs/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(got), got)
	}
	if got[0].Line != 12 || got[0].Message != "unreachable code" {
		t.Errorf("first = %+v", got[0])
	}
	// Absolute paths inside the repo are made relative.
	if got[1].File != "other.go" {
		t.Errorf("second file = %q, want other.go", got[1].File)
	}
}

// Snyk emits an object for one project and an array for --all-projects. Getting
// this wrong is the single most common bug in hand-rolled Snyk automation.
func TestSnykSingleObjectShape(t *testing.T) {
	p, _ := Get("snyk-json")
	body := `{
	  "displayTargetFile": "pom.xml",
	  "vulnerabilities": [
	    {"id":"SNYK-1","title":"RCE","severity":"critical","packageName":"log4j-core",
	     "version":"2.14.1","isUpgradable":true,"isPatchable":false,"fixedIn":["2.17.1"]}
	  ]
	}`
	got, err := p(Input{Output: body})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	f := got[0]
	if f.Severity != model.SeverityCritical || f.Package != "log4j-core" || f.FixedIn != "2.17.1" {
		t.Errorf("finding = %+v", f)
	}
	if !f.NativelyFixable {
		t.Error("an upgradable vulnerability is natively fixable")
	}
}

func TestSnykArrayShape(t *testing.T) {
	p, _ := Get("snyk-json")
	body := `[
	  {"displayTargetFile":"a/pom.xml","vulnerabilities":[
	    {"id":"SNYK-1","title":"x","severity":"high","packageName":"p1","version":"1"}]},
	  {"displayTargetFile":"b/pom.xml","vulnerabilities":[
	    {"id":"SNYK-2","title":"y","severity":"low","packageName":"p2","version":"2"}]}
	]`
	got, err := p(Input{Output: body})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2", len(got))
	}
}

// Snyk reports the same CVE once per path through the dependency graph.
func TestSnykDeduplicatesRepeatedPaths(t *testing.T) {
	p, _ := Get("snyk-json")
	body := `{"displayTargetFile":"pom.xml","vulnerabilities":[
	  {"id":"SNYK-1","title":"x","severity":"high","packageName":"p","version":"1"},
	  {"id":"SNYK-1","title":"x","severity":"high","packageName":"p","version":"1"},
	  {"id":"SNYK-1","title":"x","severity":"high","packageName":"p","version":"1"}]}`
	got, err := p(Input{Output: body})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got %d findings, want 1 after dedupe", len(got))
	}
}

func TestSnykEmptyOutput(t *testing.T) {
	p, _ := Get("snyk-json")
	got, err := p(Input{Output: "   "})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d findings, want 0", len(got))
	}
}

func TestRuffJSON(t *testing.T) {
	p, _ := Get("ruff-json")
	body := `[
	 {"code":"S105","message":"hardcoded password","filename":"app.py",
	  "location":{"row":4},"fix":{"applicability":"safe"}},
	 {"code":"E501","message":"line too long","filename":"app.py",
	  "location":{"row":9},"fix":null}
	]`
	got, err := p(Input{Output: body})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d findings, want 2", len(got))
	}
	if got[0].Severity != model.SeverityHigh {
		t.Errorf("security rule should be high, got %s", got[0].Severity)
	}
	if !got[0].NativelyFixable {
		t.Error("a safe ruff fix is natively fixable")
	}
	if got[1].NativelyFixable {
		t.Error("a finding with no fix is not natively fixable")
	}
	if got[1].Severity != model.SeverityLow {
		t.Errorf("style rule should be low, got %s", got[1].Severity)
	}
}
