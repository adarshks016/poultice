package recipe

import (
	"path/filepath"
	"strings"
	"testing"
)

const validRecipe = `
apiVersion: poultice.dev/v1
kind: Recipe
metadata:
  name: demo
  ecosystem: go
detect:
  files:
    - "**/*.go"
diagnose:
  parse: gofmt-list
  run: gofmt -l .
fix:
  - strategy: native
    name: fmt
    run: gofmt -w .
verify:
  - name: build
    run: go build ./...
`

func TestParseValidRecipe(t *testing.T) {
	r, err := Parse([]byte(validRecipe), "demo.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Metadata.Name != "demo" || r.Metadata.Ecosystem != "go" {
		t.Errorf("metadata = %+v", r.Metadata)
	}
	if len(r.Fix) != 1 || r.Fix[0].Kind != StrategyNative {
		t.Errorf("fix = %+v", r.Fix)
	}
	if len(r.Verify) != 1 || r.Verify[0].Name != "build" {
		t.Errorf("verify = %+v", r.Verify)
	}
}

// The rule the project rests on.
func TestRecipeWithoutVerifyIsRejected(t *testing.T) {
	src := strings.Split(validRecipe, "verify:")[0]
	_, err := Parse([]byte(src), "noverify.yaml")
	if err == nil {
		t.Fatal("expected a recipe with no verify block to be rejected")
	}
	if !strings.Contains(err.Error(), "verify") {
		t.Errorf("error should name the missing verify block, got: %v", err)
	}
}

func TestAIStrategyRequiresAllowPaths(t *testing.T) {
	src := `
apiVersion: poultice.dev/v1
kind: Recipe
metadata:
  name: demo
  ecosystem: go
detect:
  files:
    - "**/*.go"
diagnose:
  parse: gofmt-list
fix:
  - strategy: ai
    name: unbounded
verify:
  - name: build
    run: go build ./...
`
	_, err := Parse([]byte(src), "unbounded.yaml")
	if err == nil {
		t.Fatal("expected an AI strategy without allowPaths to be rejected")
	}
	if !strings.Contains(err.Error(), "allowPaths") {
		t.Errorf("error should name allowPaths, got: %v", err)
	}
}

func TestNativeStrategyMayNotFollowAI(t *testing.T) {
	src := `
apiVersion: poultice.dev/v1
kind: Recipe
metadata:
  name: demo
  ecosystem: go
detect:
  files:
    - "**/*.go"
diagnose:
  parse: gofmt-list
fix:
  - strategy: ai
    name: model
    policy:
      allowPaths:
        - "**/*.go"
  - strategy: native
    name: late
    run: gofmt -w .
verify:
  - name: build
    run: go build ./...
`
	_, err := Parse([]byte(src), "ordering.yaml")
	if err == nil {
		t.Fatal("expected native-after-ai ordering to be rejected")
	}
	if !strings.Contains(err.Error(), "deterministic fixes must run first") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	src := strings.Replace(validRecipe, "  ecosystem: go", "  ecosystem: go\n  eco system: typo", 1)
	_, err := Parse([]byte(src), "typo.yaml")
	if err == nil {
		t.Fatal("expected an unknown field to be rejected")
	}
}

func TestDefaultsAppliedToAIStrategy(t *testing.T) {
	src := `
apiVersion: poultice.dev/v1
kind: Recipe
metadata:
  name: demo
  ecosystem: java
detect:
  files:
    - "**/pom.xml"
diagnose:
  parse: snyk-json
fix:
  - strategy: ai
    name: bumps
    policy:
      allowPaths:
        - "**/pom.xml"
verify:
  - name: compile
    run: mvn -B compile
`
	r, err := Parse([]byte(src), "defaults.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s := r.Fix[0]
	if s.MaxAttempts != 2 {
		t.Errorf("MaxAttempts = %d, want 2", s.MaxAttempts)
	}
	if s.Policy.MaxChangedFiles != 10 || s.Policy.MaxChangedLines != 400 {
		t.Errorf("policy limits = %+v", s.Policy)
	}
	// Dangerous paths are denied even though the recipe never mentioned them.
	var sawGitHub bool
	for _, d := range s.Policy.DenyPaths {
		if d == ".github/**" {
			sawGitHub = true
		}
	}
	if !sawGitHub {
		t.Errorf("default deny paths not applied: %v", s.Policy.DenyPaths)
	}
}

// The recipes shipped in the repository must be valid, or the first thing a new
// user runs fails.
func TestShippedRecipesAreValid(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "recipes", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no recipes found; expected the repository to ship some")
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			r, err := Load(p)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if len(r.Verify) == 0 {
				t.Error("shipped recipe has no verify steps")
			}
		})
	}
}
