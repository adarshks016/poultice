package parse

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/adarshks016/poultice/internal/model"
)

func init() {
	Register("snyk-json", parseSnykJSON)
}

// snykProject mirrors the subset of Snyk's JSON we depend on. Snyk emits a bare
// object for a single project and an array when --all-projects matched several,
// which is the shape that most hand-rolled jq pipelines get wrong.
type snykProject struct {
	DisplayTargetFile string          `json:"displayTargetFile"`
	Vulnerabilities   []snykVulnEntry `json:"vulnerabilities"`
}

type snykVulnEntry struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Severity     string   `json:"severity"`
	PackageName  string   `json:"packageName"`
	Version      string   `json:"version"`
	IsUpgradable bool     `json:"isUpgradable"`
	IsPatchable  bool     `json:"isPatchable"`
	FixedIn      []string `json:"fixedIn"`
}

func parseSnykJSON(in Input) (model.Findings, error) {
	raw := []byte(in.Output)
	if in.ArtifactPath != "" {
		b, err := os.ReadFile(in.ArtifactPath)
		if err != nil {
			return nil, fmt.Errorf("read snyk report %s: %w", in.ArtifactPath, err)
		}
		raw = b
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, nil
	}

	projects, err := decodeSnykProjects(raw)
	if err != nil {
		return nil, err
	}

	var out model.Findings
	for _, p := range projects {
		for _, v := range p.Vulnerabilities {
			fixedIn := ""
			if len(v.FixedIn) > 0 {
				fixedIn = v.FixedIn[0]
			}
			out = append(out, model.Finding{
				RuleID:          v.ID,
				Message:         v.Title,
				Severity:        model.ParseSeverity(v.Severity),
				File:            p.DisplayTargetFile,
				Package:         v.PackageName,
				Version:         v.Version,
				FixedIn:         fixedIn,
				NativelyFixable: v.IsUpgradable || v.IsPatchable,
				Source:          "snyk",
			})
		}
	}
	// Snyk reports one entry per vulnerable path through the dependency graph, so
	// the same CVE in the same pom appears many times.
	return out.Dedupe(), nil
}

// decodeSnykProjects handles both the single-object and array response shapes.
func decodeSnykProjects(raw []byte) ([]snykProject, error) {
	trimmed := strings.TrimLeftFunc(string(raw), func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})
	if strings.HasPrefix(trimmed, "[") {
		var many []snykProject
		if err := json.Unmarshal(raw, &many); err != nil {
			return nil, fmt.Errorf("decode snyk array output: %w", err)
		}
		return many, nil
	}
	var one snykProject
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, fmt.Errorf("decode snyk object output: %w", err)
	}
	return []snykProject{one}, nil
}
