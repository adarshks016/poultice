package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseFull(t *testing.T) {
	src := `
severity: medium
recipesDir: tools/recipes
only:
  - go-formatting
  - go-mod-tidy
skip:
  - semgrep
noAI: true
prBody: out/pr.md
ai:
  model: claude-opus-5
`
	f, err := Parse([]byte(src), "/repo/.poultice.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := &File{
		Path:       "/repo/.poultice.yaml",
		Severity:   "medium",
		RecipesDir: "tools/recipes",
		Only:       []string{"go-formatting", "go-mod-tidy"},
		Skip:       []string{"semgrep"},
		NoAI:       true,
		PRBody:     "out/pr.md",
		AIModel:    "claude-opus-5",
	}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("got %+v\nwant %+v", f, want)
	}
	if got := f.Resolve(f.RecipesDir); got != filepath.Join("/repo", "tools/recipes") {
		t.Errorf("Resolve = %q", got)
	}
	if got := f.Resolve(Builtin); got != Builtin {
		t.Errorf("builtin must not be resolved as a path, got %q", got)
	}
}

func TestParseRejectsMistakes(t *testing.T) {
	cases := map[string]string{
		"typo":          "severty: high\n",
		"bad severity":  "severity: urgent\n",
		"only and skip": "only:\n  - a\nskip:\n  - a\n",
		"ai typo":       "ai:\n  modle: x\n",
		"bad bool":      "noAI: sometimes\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(src), ".poultice.yaml"); err == nil {
				t.Errorf("expected %q to be rejected", src)
			}
		})
	}
}

// The file `poultice init` writes must itself be a valid, all-defaults config.
func TestTemplateParses(t *testing.T) {
	f, err := Parse([]byte(Template), FileName)
	if err != nil {
		t.Fatalf("template does not parse: %v", err)
	}
	if f.Severity != "high" || f.NoAI || len(f.Only) != 0 || f.RecipesDir != "" {
		t.Errorf("template should be all defaults, got %+v", f)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	if got := Find(dir); got != "" {
		t.Errorf("empty dir: Find = %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, ".poultice.yml"), []byte("noAI: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Find(dir); !strings.HasSuffix(got, ".poultice.yml") {
		t.Errorf("Find = %q, want the .yml variant", got)
	}
}
