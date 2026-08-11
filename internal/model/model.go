// Package model defines the normalized domain types that flow through the
// poultice engine. Every detector emits Findings; every strategy consumes them
// and emits Patches; every verifier emits a Verdict.
//
// Keeping these types small and toolchain-agnostic is what allows a single
// engine to heal Java, Python, Go and Node repositories without knowing
// anything about them.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Severity is a normalized severity ranking shared by every parser.
type Severity string

const (
	SeverityUnknown  Severity = "unknown"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

var severityRank = map[Severity]int{
	SeverityUnknown:  0,
	SeverityLow:      1,
	SeverityMedium:   2,
	SeverityHigh:     3,
	SeverityCritical: 4,
}

// ParseSeverity normalizes a tool-specific severity string.
func ParseSeverity(s string) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low", "minor", "note", "info", "informational":
		return SeverityLow
	case "medium", "moderate", "warning", "warn":
		return SeverityMedium
	case "high", "error":
		return SeverityHigh
	case "critical", "fatal", "blocker":
		return SeverityCritical
	default:
		return SeverityUnknown
	}
}

// AtLeast reports whether s meets or exceeds the threshold.
func (s Severity) AtLeast(threshold Severity) bool {
	return severityRank[s] >= severityRank[threshold]
}

func (s Severity) String() string { return string(s) }

// Finding is a single problem discovered by a diagnose step.
//
// It deliberately does not distinguish "vulnerability" from "lint error" from
// "compile failure" — to the engine they are all just things that a strategy
// might fix and a verifier must later confirm are gone.
type Finding struct {
	// RuleID is the tool's identifier: CVE-2024-1234, SNYK-JAVA-..., E501, S1481.
	RuleID string `json:"ruleId"`
	// Message is a human-readable one-line description.
	Message string `json:"message"`
	// Severity is the normalized severity.
	Severity Severity `json:"severity"`
	// File is repo-relative where known; empty for project-wide findings.
	File string `json:"file,omitempty"`
	// Line is 1-indexed; zero when not applicable.
	Line int `json:"line,omitempty"`
	// Package and Version identify the offending dependency, for dep findings.
	Package string `json:"package,omitempty"`
	Version string `json:"version,omitempty"`
	// FixedIn records the first non-vulnerable version, when the tool reports one.
	FixedIn string `json:"fixedIn,omitempty"`
	// NativelyFixable is true when the originating tool claims it can fix this
	// itself. The engine uses this to decide whether spending model tokens is
	// even worth attempting.
	NativelyFixable bool `json:"nativelyFixable"`
	// Source names the parser that produced this finding.
	Source string `json:"source"`
}

// Fingerprint is a stable identity for a finding, used to deduplicate across
// runs so that a weekly schedule does not reopen an identical pull request.
//
// It deliberately excludes Message (tools reword) and Line (code moves).
func (f Finding) Fingerprint() string {
	key := strings.Join([]string{f.Source, f.RuleID, f.File, f.Package}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func (f Finding) String() string {
	loc := f.File
	if loc != "" && f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	if loc == "" {
		loc = f.Package
	}
	return fmt.Sprintf("[%s] %s %s: %s", f.Severity, f.RuleID, loc, f.Message)
}

// Findings is a sortable collection with convenience filters.
type Findings []Finding

// AtLeast returns the subset meeting the severity threshold.
func (fs Findings) AtLeast(threshold Severity) Findings {
	out := make(Findings, 0, len(fs))
	for _, f := range fs {
		if f.Severity.AtLeast(threshold) {
			out = append(out, f)
		}
	}
	return out
}

// Dedupe removes findings sharing a fingerprint, preserving first occurrence.
func (fs Findings) Dedupe() Findings {
	seen := make(map[string]struct{}, len(fs))
	out := make(Findings, 0, len(fs))
	for _, f := range fs {
		fp := f.Fingerprint()
		if _, dup := seen[fp]; dup {
			continue
		}
		seen[fp] = struct{}{}
		out = append(out, f)
	}
	return out
}

// Sorted returns findings ordered by descending severity, then file, then rule.
func (fs Findings) Sorted() Findings {
	out := append(Findings(nil), fs...)
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := severityRank[out[i].Severity], severityRank[out[j].Severity]; a != b {
			return a > b
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out
}

// Fingerprints returns the set of fingerprints present.
func (fs Findings) Fingerprints() map[string]struct{} {
	set := make(map[string]struct{}, len(fs))
	for _, f := range fs {
		set[f.Fingerprint()] = struct{}{}
	}
	return set
}

// Resolved returns the findings in fs that are absent from after.
func (fs Findings) Resolved(after Findings) Findings {
	remaining := after.Fingerprints()
	out := make(Findings, 0, len(fs))
	for _, f := range fs {
		if _, still := remaining[f.Fingerprint()]; !still {
			out = append(out, f)
		}
	}
	return out
}

// Verdict is the outcome of running a recipe's verify block.
type Verdict struct {
	// Passed is true only when every verify step exited zero.
	Passed bool `json:"passed"`
	// Steps records per-step results in declaration order.
	Steps []StepResult `json:"steps"`
}

// FailedStep returns the first failing step, or nil when the verdict passed.
func (v Verdict) FailedStep() *StepResult {
	for i := range v.Steps {
		if !v.Steps[i].Passed {
			return &v.Steps[i]
		}
	}
	return nil
}

// StepResult is the result of one verify step.
type StepResult struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	ExitCode int    `json:"exitCode"`
	// Output is the captured combined output, already truncated for reporting.
	Output string `json:"output,omitempty"`
	// DurationMS is wall-clock time for the step.
	DurationMS int64 `json:"durationMs"`
}

// Outcome is the terminal state of a heal run. These map directly onto what a
// caller should do next, which is why they are exhaustive and named for the
// decision rather than the internal state.
type Outcome string

const (
	// OutcomeClean means diagnose found nothing above threshold. No branch, no PR.
	OutcomeClean Outcome = "clean"
	// OutcomeHealed means findings were fixed and verification passed.
	OutcomeHealed Outcome = "healed"
	// OutcomePartial means some findings were fixed and verification passed, but
	// findings remain. Still worth a pull request.
	OutcomePartial Outcome = "partial"
	// OutcomeUnverified means fixes were produced but could not be verified, and
	// the working tree was rolled back to the last green checkpoint. Any surviving
	// changes are safe; the unverifiable ones were discarded.
	OutcomeUnverified Outcome = "unverified"
	// OutcomeFailed means the run could not complete (tool missing, crash).
	OutcomeFailed Outcome = "failed"
)

// ShouldOpenPR reports whether the outcome warrants a pull request.
func (o Outcome) ShouldOpenPR() bool {
	return o == OutcomeHealed || o == OutcomePartial || o == OutcomeUnverified
}

// DraftPR reports whether any pull request opened should be a draft, because a
// human needs to finish the job.
func (o Outcome) DraftPR() bool { return o == OutcomeUnverified }
