package strategy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adarshks016/poultice/internal/model"
	"github.com/adarshks016/poultice/internal/recipe"
)

func sampleRequest() PatchRequest {
	return PatchRequest{
		Findings: model.Findings{
			{RuleID: "E501", Message: "line too long", Severity: model.SeverityHigh, File: "app.py", Line: 3, Source: "ruff"},
		},
		Files:   map[string]string{"app.py": "x = 1\n"},
		Policy:  recipe.Policy{AllowPaths: []string{"**/*.py"}, MaxChangedFiles: 1},
		Attempt: 1,
	}
}

func TestAnthropicProposeSuccess(t *testing.T) {
	const diff = "diff --git a/app.py b/app.py\n--- a/app.py\n+++ b/app.py\n@@ -1 +1 @@\n-x = 1\n+x = 2\n"

	var gotReq messagesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing/wrong x-api-key header: %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != anthropicVersion {
			t.Errorf("wrong anthropic-version: %q", r.Header.Get("anthropic-version"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Fatalf("request body not valid JSON: %v", err)
		}
		// The model must be wrapped in ```fences to prove extractDiff runs.
		_ = json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{{Type: "text", Text: "```diff\n" + diff + "```"}},
		})
	}))
	defer srv.Close()

	a := &Anthropic{APIKey: "test-key", Model: "test-model", BaseURL: srv.URL, HTTP: srv.Client()}
	out, err := a.Propose(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if strings.TrimSpace(out) != strings.TrimSpace(diff) {
		t.Fatalf("diff not extracted cleanly:\n%q", out)
	}

	if gotReq.Model != "test-model" {
		t.Errorf("model = %q, want test-model", gotReq.Model)
	}
	if gotReq.MaxTokens == 0 {
		t.Error("max_tokens not set")
	}
	if len(gotReq.Messages) != 1 || gotReq.Messages[0].Role != "user" {
		t.Fatalf("unexpected messages: %+v", gotReq.Messages)
	}
	prompt := gotReq.Messages[0].Content
	for _, want := range []string{"E501", "app.py", "**/*.py"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n%s", want, prompt)
		}
	}
}

func TestAnthropicProposeAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()

	a := &Anthropic{APIKey: "k", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := a.Propose(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if !strings.Contains(err.Error(), "slow down") || !strings.Contains(err.Error(), "rate_limit_error") {
		t.Errorf("error did not surface API message: %v", err)
	}
}

func TestAnthropicProposeNoKey(t *testing.T) {
	a := &Anthropic{APIKey: ""}
	_, err := a.Propose(context.Background(), sampleRequest())
	if err != ErrNotConfigured {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

func TestNewAnthropicFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	if p, ok := NewAnthropicFromEnv(); ok {
		t.Errorf("want disabled without key, got %T", p)
	} else if _, isDisabled := p.(Disabled); !isDisabled {
		t.Errorf("fallback should be Disabled, got %T", p)
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("POULTICE_AI_MODEL", "custom-model")
	p, ok := NewAnthropicFromEnv()
	if !ok {
		t.Fatal("want configured provider with key set")
	}
	a, isAnthropic := p.(*Anthropic)
	if !isAnthropic {
		t.Fatalf("want *Anthropic, got %T", p)
	}
	if a.Model != "custom-model" {
		t.Errorf("model override ignored: %q", a.Model)
	}
	if !strings.HasPrefix(a.Name(), "anthropic/") {
		t.Errorf("Name() = %q", a.Name())
	}
}

func TestExtractDiff(t *testing.T) {
	want := "diff --git a/f b/f\n@@ -1 +1 @@\n-a\n+b"
	cases := map[string]string{
		"bare":             want,
		"fenced diff":      "```diff\n" + want + "\n```",
		"fenced plain":     "```\n" + want + "\n```",
		"preamble":         "Here is the fix:\n\n" + want,
		"preamble+fence":   "Sure!\n```\n" + want + "\n```\n",
		"trailing newline": want + "\n\n",
	}
	for name, in := range cases {
		if got := extractDiff(in); got != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
}
