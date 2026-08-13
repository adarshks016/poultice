package strategy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adarshks016/poultice/internal/recipe"
)

// Default configuration for the Anthropic provider. The model is a deliberate
// choice: a healer retries and verifies, so a fast mid-tier model is a better
// default than the most expensive one — correctness is enforced downstream by
// verification, not by paying for a larger model. Override with POULTICE_AI_MODEL.
const (
	defaultAnthropicModel   = "claude-sonnet-5"
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	anthropicVersion        = "2023-06-01"
	defaultMaxTokens        = 4096
)

// Anthropic is a Patcher backed by the Claude Messages API.
//
// It holds no per-run state and is safe to reuse across recipes. It never
// applies a patch itself: it returns a unified diff, and the engine validates
// it against policy and `git apply --check` before anything reaches the working
// tree.
type Anthropic struct {
	APIKey    string
	Model     string
	BaseURL   string
	MaxTokens int
	HTTP      *http.Client
}

// NewAnthropicFromEnv builds an Anthropic patcher from the environment.
//
// It returns (Disabled{}, false) when ANTHROPIC_API_KEY is unset, so callers can
// fall back to the no-op provider without special-casing. This keeps --no-ai and
// unauthenticated runs behaving identically: the AI path reports "skipped".
func NewAnthropicFromEnv() (Patcher, bool) {
	key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if key == "" {
		return Disabled{}, false
	}
	model := strings.TrimSpace(os.Getenv("POULTICE_AI_MODEL"))
	if model == "" {
		model = defaultAnthropicModel
	}
	base := strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
	if base == "" {
		base = defaultAnthropicBaseURL
	}
	return &Anthropic{
		APIKey:    key,
		Model:     model,
		BaseURL:   strings.TrimRight(base, "/"),
		MaxTokens: defaultMaxTokens,
		HTTP:      &http.Client{Timeout: 90 * time.Second},
	}, true
}

// Name implements Patcher.
func (a *Anthropic) Name() string { return "anthropic/" + a.model() }

func (a *Anthropic) model() string {
	if a.Model != "" {
		return a.Model
	}
	return defaultAnthropicModel
}

// Propose implements Patcher. It asks the model for a unified diff addressing the
// outstanding findings and returns it verbatim; validation is the engine's job.
func (a *Anthropic) Propose(ctx context.Context, req PatchRequest) (string, error) {
	if strings.TrimSpace(a.APIKey) == "" {
		return "", ErrNotConfigured
	}

	reqBody := messagesRequest{
		Model:     a.model(),
		MaxTokens: a.maxTokens(),
		System:    systemPrompt,
		Messages: []message{
			{Role: "user", Content: userPrompt(req)},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	base := a.BaseURL
	if base == "" {
		base = defaultAnthropicBaseURL
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", a.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	client := a.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call anthropic: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var e errorResponse
		if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
			return "", fmt.Errorf("anthropic %d: %s: %s", resp.StatusCode, e.Error.Type, e.Error.Message)
		}
		return "", fmt.Errorf("anthropic %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var mr messagesResponse
	if err := json.Unmarshal(body, &mr); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	text := mr.text()
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("anthropic returned no text content")
	}
	return extractDiff(text), nil
}

func (a *Anthropic) maxTokens() int {
	if a.MaxTokens > 0 {
		return a.MaxTokens
	}
	return defaultMaxTokens
}

// ── prompt construction ────────────────────────────────────────────────────

const systemPrompt = `You are a code-repair assistant embedded in a tool that ` +
	`applies your output with 'git apply' and then runs a verification suite. ` +
	`A patch that does not apply cleanly, or that fails verification, is discarded ` +
	`entirely — so precision matters more than ambition.

Rules:
- Respond with a single unified diff and nothing else. No prose, no explanation, no code fences.
- Use 'git apply'-compatible format: a "diff --git" header and "--- a/<path>" / "+++ b/<path>" lines, with correct @@ hunk headers.
- Paths must be repo-relative, exactly as given to you.
- Change the minimum necessary to resolve the findings. Do not reformat unrelated code, rename symbols, or alter behavior.
- Never touch CI configuration, secrets, or build credentials.
- If you cannot produce a safe fix, output exactly: CANNOT_FIX`

// userPrompt renders the findings, file contents, and policy into a single
// message. It is deterministic given its input, which keeps tests stable.
func userPrompt(req PatchRequest) string {
	var b strings.Builder

	b.WriteString("Resolve the following findings.\n\n## Findings\n")
	for _, f := range req.Findings.Sorted() {
		b.WriteString("- ")
		b.WriteString(f.String())
		if f.FixedIn != "" {
			b.WriteString(" (fixed in " + f.FixedIn + ")")
		}
		b.WriteByte('\n')
	}

	if strings.TrimSpace(req.FailureOutput) != "" {
		b.WriteString("\n## Build failure to repair\n```\n")
		b.WriteString(strings.TrimSpace(req.FailureOutput))
		b.WriteString("\n```\n")
	}

	if len(req.Files) > 0 {
		b.WriteString("\n## Files\n")
		for _, path := range sortedKeys(req.Files) {
			b.WriteString("\n=== " + path + " ===\n")
			b.WriteString(req.Files[path])
			if !strings.HasSuffix(req.Files[path], "\n") {
				b.WriteByte('\n')
			}
		}
	}

	b.WriteString("\n## Constraints\n")
	writePolicy(&b, req.Policy)
	if req.Attempt > 1 {
		b.WriteString("\nThis is retry attempt " + strconv.Itoa(req.Attempt) +
			". A previous patch failed to apply or verify; produce a different, more conservative fix.\n")
	}

	return b.String()
}

func writePolicy(b *strings.Builder, p recipe.Policy) {
	if len(p.AllowPaths) > 0 {
		b.WriteString("- Only modify files matching: " + strings.Join(p.AllowPaths, ", ") + "\n")
	}
	if len(p.DenyPaths) > 0 {
		b.WriteString("- Never modify files matching: " + strings.Join(p.DenyPaths, ", ") + "\n")
	}
	if p.MaxChangedFiles > 0 {
		b.WriteString("- Change at most " + strconv.Itoa(p.MaxChangedFiles) + " file(s).\n")
	}
	if p.MaxChangedLines > 0 {
		b.WriteString("- Change at most " + strconv.Itoa(p.MaxChangedLines) + " line(s).\n")
	}
	b.WriteString("- The patch must apply cleanly against the files shown above.\n")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// extractDiff strips the wrapping a model tends to add around a diff — a
// ```fence, or a sentence of preamble before the first diff marker — so the
// engine receives something git apply can read. It is intentionally lenient:
// the authoritative check is still `git apply --check`, which rejects anything
// this misses.
func extractDiff(s string) string {
	s = strings.TrimSpace(s)
	// Take the contents of a fenced block if one is present anywhere.
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:] // drop an optional language tag on the fence line
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			rest = rest[:j]
		}
		s = strings.TrimSpace(rest)
	}
	// Already a diff? Leave it alone — matching "--- " again here would wrongly
	// strip the "diff --git" header, since that marker also appears mid-diff.
	if strings.HasPrefix(s, "diff --git ") || strings.HasPrefix(s, "--- ") {
		return s
	}
	// Otherwise drop any prose preamble before the first diff marker that starts
	// its own line.
	for _, marker := range []string{"\ndiff --git ", "\n--- "} {
		if k := strings.Index(s, marker); k >= 0 {
			return strings.TrimSpace(s[k+1:])
		}
	}
	return s
}

// ── Messages API wire types ────────────────────────────────────────────────

type messagesRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	Content []contentBlock `json:"content"`
}

func (r messagesResponse) text() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type errorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
