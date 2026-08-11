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

	"github.com/adarshks016/poultice/internal/engine"
	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/parse"
	"github.com/adarshks016/poultice/internal/recipe"
	"github.com/adarshks016/poultice/internal/report"
)

// version is overridden at build time via -ldflags.
var version = "0.0.1-dev"

const usage = `poultice — verified self-healing for repositories

usage:
  poultice heal     [flags]   diagnose, fix, verify, and keep only what passed
  poultice diagnose [flags]   report findings without changing anything
  poultice recipes  [flags]   list recipes and whether they apply here
  poultice validate <file>…   validate recipe files
  poultice version            print version

flags:
  -C, --dir <path>        repository root (default ".")
  --recipes <path>        recipe directory (default "./recipes")
  --recipe <name>         run only the named recipe
  --severity <level>      low|medium|high|critical (default "high")
  --no-ai                 deterministic strategies only; never call a model
  --dry-run               diagnose and report, change nothing
  --json                  emit machine-readable JSON
  --pr-body <path>        write a pull request body to this file

poultice refuses to keep any change that its recipe cannot verify.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "poultice: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	dir      string
	recipes  string
	only     string
	severity string
	noAI     bool
	dryRun   bool
	asJSON   bool
	prBody   string
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
	fs.StringVar(&cfg.recipes, "recipes", "", "recipe directory")
	fs.StringVar(&cfg.only, "recipe", "", "run only the named recipe")
	fs.StringVar(&cfg.severity, "severity", "high", "minimum severity")
	fs.BoolVar(&cfg.noAI, "no-ai", false, "deterministic strategies only")
	fs.BoolVar(&cfg.dryRun, "dry-run", false, "change nothing")
	fs.BoolVar(&cfg.asJSON, "json", false, "machine-readable output")
	fs.StringVar(&cfg.prBody, "pr-body", "", "write a PR body to this path")
	if err := fs.Parse(rest); err != nil {
		return err
	}

	abs, err := filepath.Abs(cfg.dir)
	if err != nil {
		return err
	}
	cfg.dir = abs
	if cfg.recipes == "" {
		cfg.recipes = filepath.Join(abs, "recipes")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "heal":
		return cmdHeal(ctx, cfg, false)
	case "diagnose":
		return cmdHeal(ctx, cfg, true)
	case "recipes":
		return cmdRecipes(cfg)
	case "validate":
		return cmdValidate(fs.Args())
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
}

func loadRecipes(cfg config) ([]*recipe.Recipe, error) {
	rs, errs := recipe.LoadDir(cfg.recipes)
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "warning: %v\n", e)
	}
	if len(rs) == 0 {
		return nil, fmt.Errorf("no valid recipes found in %s", cfg.recipes)
	}
	if cfg.only != "" {
		for _, r := range rs {
			if r.Metadata.Name == cfg.only {
				return []*recipe.Recipe{r}, nil
			}
		}
		return nil, fmt.Errorf("recipe %q not found in %s", cfg.only, cfg.recipes)
	}
	return rs, nil
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

	eng := engine.New(engine.Options{
		RepoDir:  cfg.dir,
		Severity: sev,
		DryRun:   cfg.dryRun || diagnoseOnly,
		NoAI:     cfg.noAI,
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
