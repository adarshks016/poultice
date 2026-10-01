// Command poultice heals repositories, and only keeps what it can prove.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	cfgfile "github.com/adarshks016/poultice/internal/config"
	"github.com/adarshks016/poultice/internal/engine"
	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/parse"
	"github.com/adarshks016/poultice/internal/recipe"
	"github.com/adarshks016/poultice/internal/report"
	"github.com/adarshks016/poultice/internal/strategy"
	builtin "github.com/adarshks016/poultice/recipes"
)

// version is overridden at build time via -ldflags.
var version = "0.0.1-dev"

const usage = `poultice — verified self-healing for repositories

usage:
  poultice heal     [flags]   diagnose, fix, verify, and keep only what passed
  poultice diagnose [flags]   report findings without changing anything
  poultice recipes  [flags]   list recipes and whether they apply here
  poultice validate <file>…   validate recipe files
  poultice init     [flags]   write a starter .poultice.yaml
  poultice version            print version

flags:
  -C, --dir <path>        repository root (default ".")
  --config <path>         config file (default <dir>/.poultice.yaml, if present)
  --recipes <path>        recipe directory, or "builtin" (default <dir>/recipes
                          when it exists, otherwise the built-in library)
  --recipe <a,b,…>        run only the named recipes
  --skip <a,b,…>          never run the named recipes
  --severity <level>      low|medium|high|critical (default "high")
  --no-ai                 deterministic strategies only; never call a model
  --dry-run               diagnose and report, change nothing
  --json                  emit machine-readable JSON
  --pr-body <path>        write a pull request body to this file

Flags override .poultice.yaml, which overrides the defaults above.
poultice refuses to keep any change that its recipe cannot verify.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "poultice: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	dir        string
	configPath string
	recipes    string
	only       string
	skip       string
	severity   string
	noAI       bool
	dryRun     bool
	asJSON     bool
	prBody     string
	aiModel    string
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("poultice %s\n", version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	}

	var cfg config
	fs := flag.NewFlagSet("poultice "+cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.dir, "dir", ".", "repository root")
	fs.StringVar(&cfg.dir, "C", ".", "repository root (shorthand)")
	fs.StringVar(&cfg.configPath, "config", "", "config file")
	fs.StringVar(&cfg.recipes, "recipes", "", "recipe directory, or builtin")
	fs.StringVar(&cfg.only, "recipe", "", "run only the named recipes (comma-separated)")
	fs.StringVar(&cfg.skip, "skip", "", "never run the named recipes (comma-separated)")
	fs.StringVar(&cfg.severity, "severity", "high", "minimum severity")
	fs.BoolVar(&cfg.noAI, "no-ai", false, "deterministic strategies only")
	fs.BoolVar(&cfg.dryRun, "dry-run", false, "change nothing")
	fs.BoolVar(&cfg.asJSON, "json", false, "machine-readable output")
	fs.StringVar(&cfg.prBody, "pr-body", "", "write a PR body to this path")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	abs, err := filepath.Abs(cfg.dir)
	if err != nil {
		return err
	}
	cfg.dir = abs

	switch cmd {
	case "validate":
		return cmdValidate(fs.Args())
	case "init":
		return cmdInit(cfg.dir)
	case "heal", "diagnose", "recipes":
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}

	if err := applyConfigFile(&cfg, set); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "heal":
		return cmdHeal(ctx, cfg, false)
	case "diagnose":
		return cmdHeal(ctx, cfg, true)
	default:
		return cmdRecipes(cfg)
	}
}

// applyConfigFile fills every setting the command line left unset from the
// repository's config file. An explicit flag always wins.
func applyConfigFile(cfg *config, set map[string]bool) error {
	path := cfg.configPath
	if path == "" {
		if path = cfgfile.Find(cfg.dir); path == "" {
			return nil
		}
	}
	f, err := cfgfile.Load(path)
	if err != nil {
		return err
	}
	cfg.configPath = path
	if !set["severity"] && f.Severity != "" {
		cfg.severity = f.Severity
	}
	if !set["recipes"] && f.RecipesDir != "" {
		cfg.recipes = f.Resolve(f.RecipesDir)
	}
	if !set["recipe"] && len(f.Only) > 0 {
		cfg.only = strings.Join(f.Only, ",")
	}
	if !set["skip"] && len(f.Skip) > 0 {
		cfg.skip = strings.Join(f.Skip, ",")
	}
	if !set["no-ai"] && f.NoAI {
		cfg.noAI = true
	}
	if !set["pr-body"] && f.PRBody != "" {
		cfg.prBody = f.Resolve(f.PRBody)
	}
	cfg.aiModel = f.AIModel
	return nil
}

// recipeSource decides where recipes come from: an explicit choice, else the
// repository's own recipes/ directory, else the library compiled into the
// binary — so a `go install`ed poultice works in any repository.
func recipeSource(cfg config) string {
	if cfg.recipes != "" {
		return cfg.recipes
	}
	local := filepath.Join(cfg.dir, "recipes")
	if st, err := os.Stat(local); err == nil && st.IsDir() {
		return local
	}
	return cfgfile.Builtin
}

func loadRecipes(cfg config) ([]*recipe.Recipe, error) {
	src := recipeSource(cfg)
	var (
		rs   []*recipe.Recipe
		errs []error
	)
	if src == cfgfile.Builtin {
		rs, errs = recipe.LoadFS(builtin.FS, cfgfile.Builtin)
	} else {
		rs, errs = recipe.LoadDir(src)
	}
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "warning: %v\n", e)
	}
	if len(rs) == 0 {
		return nil, fmt.Errorf("no valid recipes found in %s", src)
	}
	return selectRecipes(rs, splitList(cfg.only), splitList(cfg.skip))
}

// selectRecipes applies --recipe and --skip. Naming a recipe that does not
// exist is an error: a typo should not quietly run everything, or nothing.
func selectRecipes(all []*recipe.Recipe, only, skip []string) ([]*recipe.Recipe, error) {
	known := map[string]bool{}
	names := make([]string, 0, len(all))
	for _, r := range all {
		known[r.Metadata.Name] = true
		names = append(names, r.Metadata.Name)
	}
	for _, n := range append(append([]string(nil), only...), skip...) {
		if !known[n] {
			return nil, fmt.Errorf("recipe %q not found (available: %s)", n, strings.Join(names, ", "))
		}
	}
	want := map[string]bool{}
	for _, n := range only {
		want[n] = true
	}
	drop := map[string]bool{}
	for _, n := range skip {
		drop[n] = true
	}
	var out []*recipe.Recipe
	for _, r := range all {
		name := r.Metadata.Name
		if (len(want) == 0 || want[name]) && !drop[name] {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("every recipe was excluded by --recipe/--skip")
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cmdInit(dir string) error {
	if existing := cfgfile.Find(dir); existing != "" {
		return fmt.Errorf("%s already exists; refusing to overwrite it", existing)
	}
	path := filepath.Join(dir, cfgfile.FileName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(cfgfile.Template); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)
	return nil
}

func cmdHeal(ctx context.Context, cfg config, diagnoseOnly bool) error {
	recipes, err := loadRecipes(cfg)
	if err != nil {
		return err
	}

	sev := model.ParseSeverity(cfg.severity)
	if sev == model.SeverityUnknown {
		return fmt.Errorf("invalid --severity %q (want low|medium|high|critical)", cfg.severity)
	}

	// Select the AI provider. Absent credentials (or --no-ai) leaves the engine
	// with its Disabled patcher, so the AI path reports "skipped" rather than
	// failing — the deterministic half runs identically either way.
	var patcher strategy.Patcher = strategy.Disabled{}
	if !cfg.noAI && !diagnoseOnly {
		if p, ok := strategy.NewAnthropicFromEnv(); ok {
			// POULTICE_AI_MODEL, when set, outranks the repository's config.
			if a, isAnthropic := p.(*strategy.Anthropic); isAnthropic &&
				cfg.aiModel != "" && os.Getenv("POULTICE_AI_MODEL") == "" {
				a.Model = cfg.aiModel
			}
			patcher = p
			if !cfg.asJSON {
				fmt.Fprintf(os.Stderr, "  ai: using %s\n", p.Name())
			}
		}
	}

	eng := engine.New(engine.Options{
		RepoDir:  cfg.dir,
		Severity: sev,
		DryRun:   cfg.dryRun || diagnoseOnly,
		NoAI:     cfg.noAI,
		Patcher:  patcher,
		Log: func(format string, args ...any) {
			if !cfg.asJSON {
				fmt.Fprintf(os.Stderr, "  "+format+"\n", args...)
			}
		},
	})

	var (
		reports  []*engine.Report
		worst    = model.OutcomeClean
		anyError error
	)

	for _, rc := range recipes {
		if !cfg.asJSON {
			fmt.Fprintf(os.Stderr, "\n▸ %s (%s)\n", rc.Metadata.Name, rc.Metadata.Ecosystem)
		}
		rep, err := eng.Heal(ctx, rc)
		if err != nil {
			anyError = errors.Join(anyError, fmt.Errorf("%s: %w", rc.Metadata.Name, err))
			if rep != nil {
				rep.Outcome = model.OutcomeFailed
			}
		}
		if rep == nil {
			continue
		}
		reports = append(reports, rep)
		worst = worseOf(worst, rep.Outcome)
		if !cfg.asJSON {
			report.Terminal(os.Stdout, rep)
		}
	}

	if cfg.asJSON {
		if err := report.JSON(os.Stdout, mergeReports(reports)); err != nil {
			return err
		}
	}
	if cfg.prBody != "" && len(reports) > 0 {
		body := report.PullRequestBody(mergeReports(reports))
		if err := os.WriteFile(cfg.prBody, []byte(body), 0o644); err != nil {
			return fmt.Errorf("write pr body: %w", err)
		}
	}
	if anyError != nil {
		return anyError
	}

	// Exit codes are the contract with CI: 0 nothing to do or fully healed,
	// 2 partial, 3 unverified. Anything a pipeline should gate on is non-zero.
	switch worst {
	case model.OutcomePartial:
		os.Exit(2)
	case model.OutcomeUnverified:
		os.Exit(3)
	}
	return nil
}

// mergeReports combines per-recipe reports into one for aggregate rendering.
func mergeReports(reports []*engine.Report) *engine.Report {
	if len(reports) == 1 {
		return reports[0]
	}
	out := &engine.Report{Recipe: "multiple recipes", Outcome: model.OutcomeClean}
	names := make([]string, 0, len(reports))
	for _, r := range reports {
		names = append(names, r.Recipe)
		out.Ecosystem = r.Ecosystem
		out.Severity = r.Severity
		out.FindingsBefore = append(out.FindingsBefore, r.FindingsBefore...)
		out.FindingsAfter = append(out.FindingsAfter, r.FindingsAfter...)
		out.Resolved = append(out.Resolved, r.Resolved...)
		out.Attempts = append(out.Attempts, r.Attempts...)
		out.RolledBack = out.RolledBack || r.RolledBack
		out.DurationMS += r.DurationMS
		if r.GreenSHA != "" {
			out.GreenSHA = r.GreenSHA
		}
		if r.Verdict != nil {
			out.Verdict = r.Verdict
		}
		out.Outcome = worseOf(out.Outcome, r.Outcome)
	}
	out.Recipe = strings.Join(names, ", ")
	return out
}

// worseOf returns the outcome a caller should react to most strongly.
func worseOf(a, b model.Outcome) model.Outcome {
	rank := map[model.Outcome]int{
		model.OutcomeClean:      0,
		model.OutcomeHealed:     1,
		model.OutcomePartial:    2,
		model.OutcomeUnverified: 3,
		model.OutcomeFailed:     4,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func cmdRecipes(cfg config) error {
	recipes, err := loadRecipes(cfg)
	if err != nil {
		return err
	}
	eng := engine.New(engine.Options{RepoDir: cfg.dir})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tECOSYSTEM\tAPPLIES\tWHY")
	for _, r := range recipes {
		ok, why := eng.Applies(r)
		applies := "no"
		if ok {
			applies, why = "yes", "—"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Metadata.Name, r.Metadata.Ecosystem, applies, why)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "recipes from: %s\n", recipeSource(cfg))
	if cfg.configPath != "" {
		fmt.Fprintf(w, "config: %s\n", cfg.configPath)
	}
	fmt.Fprintf(w, "registered parsers: %s\n", strings.Join(parse.Names(), ", "))
	return w.Flush()
}

func cmdValidate(paths []string) error {
	if len(paths) == 0 {
		return errors.New("validate needs at least one recipe file")
	}
	var bad int
	for _, p := range paths {
		r, err := recipe.Load(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n%v\n\n", p, err)
			bad++
			continue
		}
		fmt.Printf("✓ %s (%s, %d verify step(s))\n", r.Metadata.Name, r.Metadata.Ecosystem, len(r.Verify))
	}
	if bad > 0 {
		return fmt.Errorf("%d recipe(s) invalid", bad)
	}
	return nil
}
