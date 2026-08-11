package policy

import (
	"strings"
	"testing"

	"github.com/adarshks016/poultice/internal/recipe"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"**/pom.xml", "pom.xml", true},
		{"**/pom.xml", "services/api/pom.xml", true},
		{"**/pom.xml", "pom.xml.bak", false},
		{"**/*.go", "internal/engine/engine.go", true},
		{"**/*.go", "main.go", true},
		{"*.go", "internal/engine/engine.go", true}, // basename fallback
		{".github/**", ".github/workflows/ci.yml", true},
		{".github/**", "docs/.github/x", false},
		{"src/*.py", "src/app.py", true},
		{"src/*.py", "src/pkg/app.py", false},
		{"**/*.key", "certs/private.key", true},
		{"**/.env", ".env", true},
		{"**/.env", "config/.env", true},
		{"**/migrations/**", "app/migrations/0001.py", true},
		{"**", "anything/at/all.txt", true},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestDenyBeatsAllow(t *testing.T) {
	p := recipe.Policy{
		AllowPaths: []string{"**"},
		DenyPaths:  []string{".github/**"},
	}
	err := Check(p, []string{"README.md", ".github/workflows/ci.yml"}, 10)
	if err == nil {
		t.Fatal("expected denied path to be rejected even when allowed")
	}
	if !strings.Contains(err.Error(), ".github/workflows/ci.yml") {
		t.Errorf("error should name the offending file: %v", err)
	}
}

func TestOutsideAllowPathsRejected(t *testing.T) {
	p := recipe.Policy{AllowPaths: []string{"**/pom.xml"}}
	err := Check(p, []string{"pom.xml", "src/main/java/App.java"}, 20)
	if err == nil {
		t.Fatal("expected a source file to be rejected by a pom-only policy")
	}
	if !strings.Contains(err.Error(), "App.java") {
		t.Errorf("error should name the offending file: %v", err)
	}
}

func TestWithinAllowPathsAccepted(t *testing.T) {
	p := recipe.Policy{AllowPaths: []string{"**/pom.xml"}, MaxChangedFiles: 5, MaxChangedLines: 100}
	if err := Check(p, []string{"pom.xml", "svc/pom.xml"}, 20); err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
}

func TestFileAndLineCaps(t *testing.T) {
	p := recipe.Policy{MaxChangedFiles: 2}
	if err := Check(p, []string{"a", "b", "c"}, 1); err == nil {
		t.Error("expected file cap to be enforced")
	}

	p = recipe.Policy{MaxChangedLines: 50}
	if err := Check(p, []string{"a"}, 51); err == nil {
		t.Error("expected line cap to be enforced")
	}
	if err := Check(p, []string{"a"}, 50); err != nil {
		t.Errorf("boundary should be inclusive: %v", err)
	}
}

func TestDefaultDenyPathsBlockSecrets(t *testing.T) {
	p := recipe.Policy{AllowPaths: []string{"**"}, DenyPaths: recipe.DefaultDenyPaths}
	for _, bad := range []string{
		".github/workflows/release.yml",
		"deploy/id_rsa",
		"app/.env.production",
		"certs/server.pem",
		"conf/settings.xml",
	} {
		if err := Check(p, []string{bad}, 1); err == nil {
			t.Errorf("expected %q to be denied by default policy", bad)
		}
	}
}
