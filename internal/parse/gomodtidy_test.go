package parse

import (
	"strings"
	"testing"

	"github.com/adarshks016/poultice/internal/model"
)

const tidyDiff = `diff current/go.mod tidy/go.mod
--- current/go.mod
+++ tidy/go.mod
@@ -2,6 +2,5 @@

 go 1.22

-require example.com/unused v1.0.0
-
+require example.com/needed v1.2.0
 replace example.com/dep => ./dep
diff current/go.sum tidy/go.sum
--- current/go.sum
+++ tidy/go.sum
@@ -0,0 +1,2 @@
+example.com/needed v1.2.0 h1:abc=
+example.com/needed v1.2.0/go.mod h1:def=
`

func TestParseGoModTidyDiff(t *testing.T) {
	p, err := Get("go-mod-tidy-diff")
	if err != nil {
		t.Fatal(err)
	}
	got, err := p(Input{Output: tidyDiff, ExitCode: 1})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(got), got)
	}

	mod := got[0]
	if mod.File != "go.mod" || mod.Severity != model.SeverityLow || !mod.NativelyFixable {
		t.Errorf("go.mod finding = %+v", mod)
	}
	if !strings.Contains(mod.Message, "add 1 and remove 2") {
		t.Errorf("go.mod message = %q", mod.Message)
	}
	if got[1].File != "go.sum" || !strings.Contains(got[1].Message, "add 2 and remove 0") {
		t.Errorf("go.sum finding = %+v", got[1])
	}
	if mod.Fingerprint() == got[1].Fingerprint() {
		t.Error("go.mod and go.sum findings must have distinct fingerprints")
	}
}

func TestParseGoModTidyDiffClean(t *testing.T) {
	p, _ := Get("go-mod-tidy-diff")
	got, err := p(Input{Output: "", ExitCode: 0})
	if err != nil || len(got) != 0 {
		t.Errorf("clean module: got %v, %v", got, err)
	}
}

// A failing tidy prints no diff; treating that as "clean" would hide it.
func TestParseGoModTidyDiffSurfacesFailures(t *testing.T) {
	p, _ := Get("go-mod-tidy-diff")

	_, err := p(Input{Output: "go: example.com/x@v1.0.0: module lookup disabled by GOPROXY=off\n", ExitCode: 1})
	if err == nil || !strings.Contains(err.Error(), "GOPROXY=off") {
		t.Errorf("want the tool's error surfaced, got %v", err)
	}

	_, err = p(Input{Output: "flag provided but not defined: -diff\nusage: go mod tidy ...\n", ExitCode: 2})
	if err == nil || !strings.Contains(err.Error(), "Go 1.23") {
		t.Errorf("want a toolchain-version hint, got %v", err)
	}
}
