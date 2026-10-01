// Package config loads .poultice.yaml, a repository's defaults for the CLI.
//
// The file only fills in what the command line leaves unsaid: an explicit flag
// always wins. Secrets are deliberately not configurable here — a file that
// lives in the repository is the wrong place for an API key.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/yaml"
)

// FileName is the config file poultice looks for at the repository root.
const FileName = ".poultice.yaml"

// Builtin selects the recipe library compiled into the binary.
const Builtin = "builtin"

// File is a decoded config file. Zero values mean "not set".
type File struct {
	// Path is where the file was loaded from.
	Path       string
	Severity   string
	RecipesDir string
	Only       []string
	Skip       []string
	NoAI       bool
	PRBody     string
	AIModel    string
}

// Find returns the config file in dir, or "" when there is none.
func Find(dir string) string {
	for _, name := range []string{FileName, ".poultice.yml"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// Load reads and validates a config file.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(raw, path)
}

// Parse validates config bytes. Unknown keys are errors, for the same reason
// they are in recipes: a silently ignored typo is silently ignored intent.
func Parse(raw []byte, path string) (*File, error) {
	doc, err := yaml.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	r := yaml.NewReader(doc, "")
	f := &File{
		Path:       path,
		Severity:   r.String("severity"),
		RecipesDir: r.String("recipesDir"),
		Only:       r.StringSlice("only"),
		Skip:       r.StringSlice("skip"),
		NoAI:       r.Bool("noAI"),
		PRBody:     r.String("prBody"),
	}
	if ai := r.Mapping("ai"); ai != nil {
		f.AIModel = ai.String("model")
		ai.CheckUnknown()
	}
	r.CheckUnknown()

	problems := r.Errors()
	if f.Severity != "" && model.ParseSeverity(f.Severity) == model.SeverityUnknown {
		problems = append(problems, fmt.Sprintf(
			"severity: %q is not one of low|medium|high|critical", f.Severity))
	}
	skipped := map[string]bool{}
	for _, name := range f.Skip {
		skipped[name] = true
	}
	for _, name := range f.Only {
		if skipped[name] {
			problems = append(problems, fmt.Sprintf("recipe %q is listed in both only and skip", name))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s is invalid:\n  - %s", path, strings.Join(problems, "\n  - "))
	}
	return f, nil
}

// Resolve interprets a path from the file relative to the file's directory,
// so a config behaves the same whichever directory poultice is run from.
func (f *File) Resolve(p string) string {
	if p == "" || p == Builtin || filepath.IsAbs(p) || f.Path == "" {
		return p
	}
	return filepath.Join(filepath.Dir(f.Path), p)
}

// Template is what `poultice init` writes. It parses to an all-defaults File.
const Template = `# poultice configuration. Command-line flags override everything here.
# Reference: docs/configuration.md in github.com/adarshks016/poultice

# Minimum severity to act on: low | medium | high | critical.
severity: high

# Where recipes come from: "builtin" for the library compiled into poultice,
# or a directory relative to this file. Default: ./recipes when it exists,
# otherwise builtin.
# recipesDir: builtin

# Run only these recipes. Default: every recipe that applies to the repository.
# only:
#   - go-formatting

# Never run these recipes.
# skip:
#   - semgrep

# Deterministic strategies only; never call a model.
noAI: false

# Write a pull request body here after every heal, relative to this file.
# prBody: pr-body.md

# The API key is read from ANTHROPIC_API_KEY and is deliberately not settable
# here: secrets do not belong in the repository.
# ai:
#   model: claude-sonnet-5
`
