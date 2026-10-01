package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cfgfile "github.com/adarshks016/poultice/internal/config"
	"github.com/adarshks016/poultice/internal/recipe"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigFillsOnlyWhatFlagsLeftUnset(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, cfgfile.FileName), `
severity: low
recipesDir: my-recipes
skip:
  - semgrep
noAI: true
ai:
  model: claude-opus-5
`)
	cfg := config{dir: dir, severity: "critical"}
	if err := applyConfigFile(&cfg, map[string]bool{"severity": true}); err != nil {
		t.Fatal(err)
	}
	if cfg.severity != "critical" {
		t.Errorf("explicit --severity must win, got %q", cfg.severity)
	}
	if cfg.recipes != filepath.Join(dir, "my-recipes") {
		t.Errorf("recipesDir should resolve against the config file, got %q", cfg.recipes)
	}
	if cfg.skip != "semgrep" || !cfg.noAI || cfg.aiModel != "claude-opus-5" {
		t.Errorf("config values not applied: %+v", cfg)
	}
}

func TestNoConfigFileIsFine(t *testing.T) {
	cfg := config{dir: t.TempDir(), severity: "high"}
	if err := applyConfigFile(&cfg, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if cfg.configPath != "" || cfg.severity != "high" {
		t.Errorf("nothing should change without a config file: %+v", cfg)
	}
}

func TestRecipeSourceFallsBackToBuiltin(t *testing.T) {
	dir := t.TempDir()
	if got := recipeSource(config{dir: dir}); got != cfgfile.Builtin {
		t.Errorf("no recipes/ dir: source = %q, want builtin", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := recipeSource(config{dir: dir}); got != filepath.Join(dir, "recipes") {
		t.Errorf("with recipes/ dir: source = %q", got)
	}
	if got := recipeSource(config{dir: dir, recipes: cfgfile.Builtin}); got != cfgfile.Builtin {
		t.Errorf("explicit builtin: source = %q", got)
	}
}

func TestBuiltinLibraryLoads(t *testing.T) {
	rs, err := loadRecipes(config{dir: t.TempDir(), recipes: cfgfile.Builtin, only: "go-formatting,go-mod-tidy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 {
		t.Errorf("got %d recipes, want 2", len(rs))
	}
}

func TestSelectRecipes(t *testing.T) {
	all := []*recipe.Recipe{
		{Metadata: recipe.Metadata{Name: "a"}},
		{Metadata: recipe.Metadata{Name: "b"}},
		{Metadata: recipe.Metadata{Name: "c"}},
	}
	names := func(rs []*recipe.Recipe) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Metadata.Name)
		}
		return out
	}

	got, err := selectRecipes(all, nil, []string{"b"})
	if err != nil || !reflect.DeepEqual(names(got), []string{"a", "c"}) {
		t.Errorf("skip b: got %v, %v", names(got), err)
	}
	got, err = selectRecipes(all, []string{"c", "a"}, nil)
	if err != nil || !reflect.DeepEqual(names(got), []string{"a", "c"}) {
		t.Errorf("only c,a: got %v, %v", names(got), err)
	}
	if _, err := selectRecipes(all, []string{"typo"}, nil); err == nil || !strings.Contains(err.Error(), "available: a, b, c") {
		t.Errorf("unknown recipe should list what exists, got %v", err)
	}
	if _, err := selectRecipes(all, []string{"a"}, []string{"a"}); err == nil {
		t.Error("excluding everything should be an error, not a silent no-op")
	}
}

func TestSplitList(t *testing.T) {
	if got := splitList(" a, ,b ,"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("splitList = %q", got)
	}
	if got := splitList(""); got != nil {
		t.Errorf("empty input = %q, want nil", got)
	}
}

func TestInitWritesTemplateOnce(t *testing.T) {
	dir := t.TempDir()
	if err := cmdInit(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := cfgfile.Load(filepath.Join(dir, cfgfile.FileName)); err != nil {
		t.Errorf("written config does not load: %v", err)
	}
	if err := cmdInit(dir); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("second init must refuse, got %v", err)
	}
}
