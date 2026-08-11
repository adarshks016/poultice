// Package report renders a heal run for humans and machines.
//
// The same Report drives the terminal summary, the JSON artifact and the pull
// request body, so that what a reviewer reads in GitHub is exactly what the
// engine recorded — no second, prettier version of events.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/adarshks016/poultice/internal/engine"
	"github.com/adarshks016/poultice/internal/model"
)

// JSON writes the report as indented JSON.
func JSON(w io.Writer, r *engine.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Terminal writes a compact human summary.
func Terminal(w io.Writer, r *engine.Report) {
	fmt.Fprintf(w, "\n  %s  %s\n", outcomeBadge(r.Outcome), r.Recipe)
	if r.Skipped != "" {
		fmt.Fprintf(w, "  skipped: %s\n\n", r.Skipped)
		return
	}
	fmt.Fprintf(w, "  findings: %d before → %d after (%d resolved)\n",
		len(r.FindingsBefore), len(r.FindingsAfter), len(r.Resolved))

	for _, a := range r.Attempts {
		status := "rejected"
		if a.Accepted {
			status = "accepted"
		}
		line := fmt.Sprintf("  · %-24s %s", a.Strategy, status)
		if a.Rejection != "" {
			line += " — " + firstLine(a.Rejection)
		} else if a.Note != "" {
			line += " — " + firstLine(a.Note)
		}
		fmt.Fprintln(w, line)
	}

	if r.RolledBack {
		fmt.Fprintf(w, "  rolled back to last verified-green commit\n")
	}
	if r.GreenSHA != "" {
		fmt.Fprintf(w, "  green: %s\n", short(r.GreenSHA))
	}
	fmt.Fprintf(w, "  took %.1fs\n\n", float64(r.DurationMS)/1000)
}

// PullRequestBody renders the markdown body for a pull request.
func PullRequestBody(r *engine.Report) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## poultice — `%s`\n\n", r.Recipe)
	fmt.Fprintf(&b, "**Outcome:** %s\n\n", outcomeSentence(r.Outcome))

	fmt.Fprintf(&b, "| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| Ecosystem | `%s` |\n", r.Ecosystem)
	fmt.Fprintf(&b, "| Severity threshold | `%s` |\n", r.Severity)
	fmt.Fprintf(&b, "| Findings before | %d |\n", len(r.FindingsBefore))
	fmt.Fprintf(&b, "| Findings after | %d |\n", len(r.FindingsAfter))
	fmt.Fprintf(&b, "| Resolved | %d |\n", len(r.Resolved))
	if r.Verdict != nil {
		fmt.Fprintf(&b, "| Verification | %s |\n", passFail(r.Verdict.Passed))
	}
	fmt.Fprintf(&b, "| Rolled back | %v |\n\n", r.RolledBack)

	if len(r.Resolved) > 0 {
		b.WriteString("### Resolved\n\n")
		for _, f := range r.Resolved.Sorted() {
			fmt.Fprintf(&b, "- `%s` %s\n", f.RuleID, f.Message)
		}
		b.WriteString("\n")
	}

	if len(r.FindingsAfter) > 0 {
		b.WriteString("### Still outstanding\n\n")
		for _, f := range r.FindingsAfter.Sorted() {
			loc := f.File
			if loc == "" {
				loc = f.Package
			}
			fmt.Fprintf(&b, "- **%s** `%s` %s — %s\n", f.Severity, f.RuleID, loc, f.Message)
		}
		b.WriteString("\n")
	}

	b.WriteString("### What was attempted\n\n")
	for _, a := range r.Attempts {
		mark := "❌"
		if a.Accepted {
			mark = "✅"
		}
		fmt.Fprintf(&b, "- %s **%s** (`%s`)", mark, a.Strategy, a.Kind)
		if a.Rejection != "" {
			fmt.Fprintf(&b, " — %s", firstLine(a.Rejection))
		} else if a.Note != "" {
			fmt.Fprintf(&b, " — %s", firstLine(a.Note))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	if r.Verdict != nil && !r.Verdict.Passed {
		if failed := r.Verdict.FailedStep(); failed != nil {
			fmt.Fprintf(&b, "### Verification failure — `%s`\n\n```\n%s\n```\n\n",
				failed.Name, strings.TrimSpace(failed.Output))
		}
	}

	b.WriteString("### Reviewer checklist\n\n")
	b.WriteString("- [ ] Every change below is one poultice could verify — check the verifier is strong enough\n")
	b.WriteString("- [ ] No behavioural change beyond the stated fix\n")
	b.WriteString("- [ ] Commits prefixed `fix(ai):` reviewed line by line\n\n")
	b.WriteString("---\n_Every commit on this branch passed the recipe's verify block. ")
	b.WriteString("Changes that could not be verified were discarded, not pushed._\n")

	return b.String()
}

func outcomeBadge(o model.Outcome) string {
	switch o {
	case model.OutcomeClean:
		return "CLEAN     "
	case model.OutcomeHealed:
		return "HEALED    "
	case model.OutcomePartial:
		return "PARTIAL   "
	case model.OutcomeUnverified:
		return "UNVERIFIED"
	default:
		return "FAILED    "
	}
}

func outcomeSentence(o model.Outcome) string {
	switch o {
	case model.OutcomeClean:
		return "✅ **clean** — nothing to fix at this severity threshold."
	case model.OutcomeHealed:
		return "✅ **healed** — all findings resolved and verification passed."
	case model.OutcomePartial:
		return "🟡 **partial** — some findings resolved, verification passed, others remain."
	case model.OutcomeUnverified:
		return "🔴 **unverified** — fixes were produced but failed verification and were rolled back. A human needs to take it from here."
	default:
		return "🔴 **failed** — the run could not complete."
	}
}

func passFail(ok bool) string {
	if ok {
		return "✅ passed"
	}
	return "❌ failed"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
