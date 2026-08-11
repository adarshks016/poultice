package model

import "testing"

func TestSeverityThreshold(t *testing.T) {
	if !SeverityCritical.AtLeast(SeverityHigh) {
		t.Error("critical should meet a high threshold")
	}
	if SeverityLow.AtLeast(SeverityHigh) {
		t.Error("low should not meet a high threshold")
	}
	if !SeverityHigh.AtLeast(SeverityHigh) {
		t.Error("threshold should be inclusive")
	}
}

func TestParseSeverityNormalizesToolVocabulary(t *testing.T) {
	cases := map[string]Severity{
		"CRITICAL": SeverityCritical,
		"blocker":  SeverityCritical,
		"High":     SeverityHigh,
		"error":    SeverityHigh,
		"moderate": SeverityMedium,
		"warning":  SeverityMedium,
		"note":     SeverityLow,
		"":         SeverityUnknown,
		"weird":    SeverityUnknown,
	}
	for in, want := range cases {
		if got := ParseSeverity(in); got != want {
			t.Errorf("ParseSeverity(%q) = %s, want %s", in, got, want)
		}
	}
}

// Fingerprints must survive the things that change between runs — reworded
// messages and shifted line numbers — or every scheduled run reopens the same
// pull request.
func TestFingerprintIsStableAcrossRewordingAndLineMoves(t *testing.T) {
	a := Finding{Source: "snyk", RuleID: "CVE-1", File: "pom.xml", Package: "log4j",
		Message: "Remote code execution", Line: 10}
	b := Finding{Source: "snyk", RuleID: "CVE-1", File: "pom.xml", Package: "log4j",
		Message: "RCE in log4j-core", Line: 42}

	if a.Fingerprint() != b.Fingerprint() {
		t.Error("fingerprint should ignore message text and line number")
	}

	c := a
	c.RuleID = "CVE-2"
	if a.Fingerprint() == c.Fingerprint() {
		t.Error("different rules must not share a fingerprint")
	}
}

func TestDedupe(t *testing.T) {
	f := Finding{Source: "snyk", RuleID: "CVE-1", File: "pom.xml", Package: "log4j"}
	fs := Findings{f, f, f}
	if got := fs.Dedupe(); len(got) != 1 {
		t.Errorf("Dedupe kept %d, want 1", len(got))
	}
}

func TestResolvedComputesTheDifference(t *testing.T) {
	fixed := Finding{Source: "s", RuleID: "A", File: "x"}
	stays := Finding{Source: "s", RuleID: "B", File: "x"}

	before := Findings{fixed, stays}
	after := Findings{stays}

	resolved := before.Resolved(after)
	if len(resolved) != 1 || resolved[0].RuleID != "A" {
		t.Errorf("resolved = %+v, want just A", resolved)
	}
}

func TestAtLeastFilters(t *testing.T) {
	fs := Findings{
		{RuleID: "a", Severity: SeverityLow},
		{RuleID: "b", Severity: SeverityHigh},
		{RuleID: "c", Severity: SeverityCritical},
	}
	if got := fs.AtLeast(SeverityHigh); len(got) != 2 {
		t.Errorf("AtLeast(high) = %d findings, want 2", len(got))
	}
}

func TestSortedPutsWorstFirst(t *testing.T) {
	fs := Findings{
		{RuleID: "a", Severity: SeverityLow},
		{RuleID: "b", Severity: SeverityCritical},
		{RuleID: "c", Severity: SeverityMedium},
	}
	got := fs.Sorted()
	if got[0].Severity != SeverityCritical || got[2].Severity != SeverityLow {
		t.Errorf("Sorted order wrong: %+v", got)
	}
}

func TestOutcomeDrivesPullRequestDecisions(t *testing.T) {
	if OutcomeClean.ShouldOpenPR() {
		t.Error("a clean run should not open a PR")
	}
	if !OutcomeHealed.ShouldOpenPR() || OutcomeHealed.DraftPR() {
		t.Error("a healed run should open a ready PR")
	}
	if !OutcomeUnverified.DraftPR() {
		t.Error("an unverified run must open a draft PR, never a ready one")
	}
}

func TestVerdictFailedStep(t *testing.T) {
	v := Verdict{Steps: []StepResult{
		{Name: "compile", Passed: true},
		{Name: "test", Passed: false},
	}}
	failed := v.FailedStep()
	if failed == nil || failed.Name != "test" {
		t.Errorf("FailedStep = %+v, want test", failed)
	}
	if (Verdict{Passed: true}).FailedStep() != nil {
		t.Error("a passing verdict has no failed step")
	}
}
