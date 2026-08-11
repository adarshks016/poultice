// Package recipe loads and validates poultice recipes.
//
// A recipe is data, not code. It declares four things: how to detect that it
// applies, how to diagnose problems, which strategies may fix them, and — non
// negotiably — how to verify that the fix worked.
//
// The validator rejects any recipe without a verify block. This is the single
// most important rule in poultice: an unverifiable fix is not a fix, it is a
// guess wearing a commit message.
package recipe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/yaml"
)

// APIVersion is the only recipe schema version understood by this build.
const APIVersion = "poultice.dev/v1"

// Kind is the only recipe kind currently defined.
const Kind = "Recipe"

// Recipe is a declarative healing procedure for one ecosystem/tool pairing.
type Recipe struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Metadata   Metadata   `json:"metadata"`
	Detect     Detect     `json:"detect"`
	Diagnose   Diagnose   `json:"diagnose"`
	Fix        []Strategy `json:"fix"`
	Verify     []Step     `json:"verify"`

	// path records where the recipe was loaded from, for error messages.
	path string
}

// Path returns the file the recipe was loaded from.
func (r *Recipe) Path() string { return r.path }

// Metadata identifies a recipe.
type Metadata struct {
	Name        string   `json:"name"`
	Ecosystem   string   `json:"ecosystem"`
	Summary     string   `json:"summary"`
	Maintainers []string `json:"maintainers,omitempty"`
}

// Detect decides whether a recipe applies to a repository.
type Detect struct {
	// Files are glob patterns; at least one must match for the recipe to apply.
	Files []string `json:"files"`
	// Requires lists executables that must be on PATH.
	Requires []string `json:"requires,omitempty"`
}

// Diagnose produces findings.
type Diagnose struct {
	// Run is the command whose output is parsed. It may be empty when the parser
	// works purely from the filesystem.
	Run string `json:"run,omitempty"`
	// Parse names a registered parser for the command's output.
	Parse string `json:"parse"`
	// TimeoutSeconds bounds the diagnose command. Zero means the engine default.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// ExpectNonZeroExit is true for tools that signal "found problems" with a
	// non-zero exit code, which must not be treated as a run failure.
	ExpectNonZeroExit bool `json:"expectNonZeroExit,omitempty"`
}

// StrategyKind distinguishes deterministic remediation from model-generated
// remediation. The engine always exhausts native strategies first.
type StrategyKind string

const (
	// StrategyNative runs the upstream tool's own fixer. Free, deterministic,
	// and correct far more often than a language model.
	StrategyNative StrategyKind = "native"
	// StrategyAI asks a language model for a patch. Always gated behind a
	// verifier, always constrained by a Policy.
	StrategyAI StrategyKind = "ai"
)

// Strategy is one attempt at fixing findings.
type Strategy struct {
	Kind StrategyKind `json:"strategy"`
	Name string       `json:"name,omitempty"`
	// Run is the command for native strategies.
	Run string `json:"run,omitempty"`
	// TimeoutSeconds bounds the strategy. Zero means the engine default.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// MaxAttempts bounds retries for AI strategies. Ignored for native.
	MaxAttempts int `json:"maxAttempts,omitempty"`
	// Context controls what source is shown to the model.
	Context Context `json:"context,omitempty"`
	// Policy constrains the blast radius of the resulting patch.
	Policy Policy `json:"policy,omitempty"`
}

// DisplayName returns Name when set, else a name derived from the kind.
func (s Strategy) DisplayName() string {
	if s.Name != "" {
		return s.Name
	}
	return string(s.Kind)
}

// Context bounds what is sent to a model.
type Context struct {
	Include  []string `json:"include,omitempty"`
	MaxBytes int      `json:"maxBytes,omitempty"`
}

// Policy is the blast-radius contract for a generated patch. The engine
// enforces every field before a patch is allowed near the working tree.
type Policy struct {
	// AllowPaths restricts which files a patch may touch. Empty means "any file
	// not denied", which validation warns about for AI strategies.
	AllowPaths []string `json:"allowPaths,omitempty"`
	// DenyPaths always wins over AllowPaths.
	DenyPaths []string `json:"denyPaths,omitempty"`
	// MaxChangedFiles rejects sprawling patches. Zero means the engine default.
	MaxChangedFiles int `json:"maxChangedFiles,omitempty"`
	// MaxChangedLines rejects rewrites masquerading as fixes.
	MaxChangedLines int `json:"maxChangedLines,omitempty"`
}

// Step is one verification command.
type Step struct {
	Name           string `json:"name"`
	Run            string `json:"run"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

// DefaultDenyPaths are refused for every AI strategy regardless of the recipe.
// A healer that can rewrite its own CI configuration or exfiltrate credentials
// is not a healer.
var DefaultDenyPaths = []string{
	".github/**",
	".gitlab-ci.yml",
	"Jenkinsfile",
	".git/**",
	"**/*.pem",
	"**/*.key",
	"**/id_rsa*",
	"**/.env",
	"**/.env.*",
	"**/.npmrc",
	"**/settings.xml",
}

// ValidationError describes why a recipe was rejected.
type ValidationError struct {
	Path     string
	Problems []string
}

func (e *ValidationError) Error() string {
	where := e.Path
	if where == "" {
		where = "recipe"
	}
	return fmt.Sprintf("%s is invalid:\n  - %s", where, strings.Join(e.Problems, "\n  - "))
}

// Load reads and validates a single recipe file.
func Load(path string) (*Recipe, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read recipe: %w", err)
	}
	return Parse(raw, path)
}

// Parse validates recipe bytes. The path is used only for error messages.
func Parse(raw []byte, path string) (*Recipe, error) {
	doc, err := yaml.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	root := yaml.NewReader(doc, "")
	r := decodeRecipe(root)
	r.path = path

	// Structural problems (wrong types, unknown fields) are reported before
	// semantic ones, because a misread field makes every later check noise.
	if problems := root.Errors(); len(problems) > 0 {
		return nil, &ValidationError{Path: path, Problems: problems}
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	r.applyDefaults()
	return r, nil
}

// decodeRecipe maps a YAML document onto the Recipe struct explicitly.
//
// This is hand-written rather than reflective on purpose: the recipe schema is
// the project's public contract, and an explicit decoder makes every field and
// every error message greppable.
func decodeRecipe(root *yaml.Reader) *Recipe {
	r := &Recipe{
		APIVersion: root.String("apiVersion"),
		Kind:       root.String("kind"),
	}

	if md := root.Mapping("metadata"); md != nil {
		r.Metadata = Metadata{
			Name:        md.String("name"),
			Ecosystem:   md.String("ecosystem"),
			Summary:     md.String("summary"),
			Maintainers: md.StringSlice("maintainers"),
		}
		md.CheckUnknown()
	}

	if d := root.Mapping("detect"); d != nil {
		r.Detect = Detect{
			Files:    d.StringSlice("files"),
			Requires: d.StringSlice("requires"),
		}
		d.CheckUnknown()
	}

	if d := root.Mapping("diagnose"); d != nil {
		r.Diagnose = Diagnose{
			Run:               d.String("run"),
			Parse:             d.String("parse"),
			TimeoutSeconds:    d.Int("timeoutSeconds"),
			ExpectNonZeroExit: d.Bool("expectNonZeroExit"),
		}
		d.CheckUnknown()
	}

	for _, f := range root.MappingSlice("fix") {
		s := Strategy{
			Kind:           StrategyKind(f.String("strategy")),
			Name:           f.String("name"),
			Run:            f.String("run"),
			TimeoutSeconds: f.Int("timeoutSeconds"),
			MaxAttempts:    f.Int("maxAttempts"),
		}
		if c := f.Mapping("context"); c != nil {
			s.Context = Context{
				Include:  c.StringSlice("include"),
				MaxBytes: c.Int("maxBytes"),
			}
			c.CheckUnknown()
		}
		if p := f.Mapping("policy"); p != nil {
			s.Policy = Policy{
				AllowPaths:      p.StringSlice("allowPaths"),
				DenyPaths:       p.StringSlice("denyPaths"),
				MaxChangedFiles: p.Int("maxChangedFiles"),
				MaxChangedLines: p.Int("maxChangedLines"),
			}
			p.CheckUnknown()
		}
		f.CheckUnknown()
		r.Fix = append(r.Fix, s)
	}

	for _, v := range root.MappingSlice("verify") {
		r.Verify = append(r.Verify, Step{
			Name:           v.String("name"),
			Run:            v.String("run"),
			TimeoutSeconds: v.Int("timeoutSeconds"),
		})
		v.CheckUnknown()
	}

	root.CheckUnknown()
	return r
}

// LoadDir loads every .yaml/.yml recipe in a directory, sorted by name.
// It returns all successfully loaded recipes together with any load errors, so
// that one malformed recipe does not hide the rest.
func LoadDir(dir string) ([]*Recipe, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []error{fmt.Errorf("read recipe dir: %w", err)}
	}
	var (
		out  []*Recipe
		errs []error
	)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		r, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Metadata.Name < out[j].Metadata.Name })
	return out, errs
}

// Validate enforces the recipe contract.
func (r *Recipe) Validate() error {
	var problems []string

	if r.APIVersion != APIVersion {
		problems = append(problems, fmt.Sprintf("apiVersion must be %q, got %q", APIVersion, r.APIVersion))
	}
	if r.Kind != Kind {
		problems = append(problems, fmt.Sprintf("kind must be %q, got %q", Kind, r.Kind))
	}
	if strings.TrimSpace(r.Metadata.Name) == "" {
		problems = append(problems, "metadata.name is required")
	}
	if strings.TrimSpace(r.Metadata.Ecosystem) == "" {
		problems = append(problems, "metadata.ecosystem is required")
	}
	if len(r.Detect.Files) == 0 {
		problems = append(problems, "detect.files must list at least one glob")
	}
	if strings.TrimSpace(r.Diagnose.Parse) == "" {
		problems = append(problems, "diagnose.parse is required (name a registered parser)")
	}
	if len(r.Fix) == 0 {
		problems = append(problems, "fix must declare at least one strategy")
	}

	// The rule the whole project rests on.
	if len(r.Verify) == 0 {
		problems = append(problems, "verify must declare at least one step: "+
			"poultice refuses recipes that cannot prove their own fixes")
	}
	for i, step := range r.Verify {
		if strings.TrimSpace(step.Run) == "" {
			problems = append(problems, fmt.Sprintf("verify[%d].run is required", i))
		}
		if strings.TrimSpace(step.Name) == "" {
			problems = append(problems, fmt.Sprintf("verify[%d].name is required", i))
		}
	}

	seenNames := map[string]bool{}
	for i, s := range r.Fix {
		switch s.Kind {
		case StrategyNative:
			if strings.TrimSpace(s.Run) == "" {
				problems = append(problems, fmt.Sprintf("fix[%d]: native strategy requires run", i))
			}
		case StrategyAI:
			if len(s.Policy.AllowPaths) == 0 {
				problems = append(problems, fmt.Sprintf(
					"fix[%d]: ai strategy requires policy.allowPaths (an unbounded model patch is not permitted)", i))
			}
		case "":
			problems = append(problems, fmt.Sprintf("fix[%d].strategy is required (native|ai)", i))
		default:
			problems = append(problems, fmt.Sprintf("fix[%d].strategy %q is not one of native|ai", i, s.Kind))
		}
		name := s.DisplayName()
		if seenNames[name] {
			problems = append(problems, fmt.Sprintf("fix[%d]: duplicate strategy name %q", i, name))
		}
		seenNames[name] = true
	}

	// Native strategies must precede AI strategies: cheap and deterministic
	// before expensive and probabilistic.
	sawAI := false
	for i, s := range r.Fix {
		if s.Kind == StrategyAI {
			sawAI = true
			continue
		}
		if sawAI && s.Kind == StrategyNative {
			problems = append(problems, fmt.Sprintf(
				"fix[%d]: native strategy declared after an ai strategy; "+
					"deterministic fixes must run first", i))
		}
	}

	if len(problems) > 0 {
		return &ValidationError{Path: r.path, Problems: problems}
	}
	return nil
}

// applyDefaults fills in engine defaults after validation has passed.
func (r *Recipe) applyDefaults() {
	for i := range r.Fix {
		s := &r.Fix[i]
		s.Policy.DenyPaths = append(s.Policy.DenyPaths, DefaultDenyPaths...)
		if s.Kind == StrategyAI {
			if s.MaxAttempts <= 0 {
				s.MaxAttempts = 2
			}
			if s.Policy.MaxChangedFiles <= 0 {
				s.Policy.MaxChangedFiles = 10
			}
			if s.Policy.MaxChangedLines <= 0 {
				s.Policy.MaxChangedLines = 400
			}
			if s.Context.MaxBytes <= 0 {
				s.Context.MaxBytes = 60_000
			}
		}
	}
}

// NativeStrategies returns only the deterministic strategies.
func (r *Recipe) NativeStrategies() []Strategy {
	var out []Strategy
	for _, s := range r.Fix {
		if s.Kind == StrategyNative {
			out = append(out, s)
		}
	}
	return out
}

// AIStrategies returns only the model-backed strategies.
func (r *Recipe) AIStrategies() []Strategy {
	var out []Strategy
	for _, s := range r.Fix {
		if s.Kind == StrategyAI {
			out = append(out, s)
		}
	}
	return out
}

// SeverityThresholdDefault is used when the caller does not specify one.
const SeverityThresholdDefault = model.SeverityHigh
