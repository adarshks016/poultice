package yaml

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Mapping {
	t.Helper()
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func getString(t *testing.T, m *Mapping, key string) string {
	t.Helper()
	v, ok := m.Get(key)
	if !ok {
		t.Fatalf("key %q missing", key)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("key %q is %T, want string", key, v)
	}
	return s
}

func TestScalars(t *testing.T) {
	m := mustParse(t, `
name: poultice        # trailing comment
empty:
quoted: "a: b"
single: 'it''s here'
url: https://example.com/x
image: repo:tag
num: 42
flag: true
`)
	cases := map[string]string{
		"name":   "poultice",
		"empty":  "",
		"quoted": "a: b",
		"single": "it's here",
		"url":    "https://example.com/x",
		"image":  "repo:tag",
		"num":    "42",
		"flag":   "true",
	}
	for k, want := range cases {
		if got := getString(t, m, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestCommentInsideQuotesIsNotStripped(t *testing.T) {
	m := mustParse(t, `run: echo "value # not a comment"`)
	if got := getString(t, m, "run"); got != `echo "value # not a comment"` {
		t.Errorf("got %q", got)
	}
}

func TestNestedMapping(t *testing.T) {
	m := mustParse(t, `
metadata:
  name: demo
  ecosystem: go
detect:
  files:
    - "**/*.go"
    - go.mod
`)
	md, ok := m.Get("metadata")
	if !ok {
		t.Fatal("metadata missing")
	}
	sub, ok := md.(*Mapping)
	if !ok {
		t.Fatalf("metadata is %T", md)
	}
	if got := getString(t, sub, "name"); got != "demo" {
		t.Errorf("name = %q", got)
	}

	d, _ := m.Get("detect")
	files, _ := d.(*Mapping).Get("files")
	seq, ok := files.([]Node)
	if !ok {
		t.Fatalf("files is %T", files)
	}
	if len(seq) != 2 || seq[0] != "**/*.go" || seq[1] != "go.mod" {
		t.Errorf("files = %#v", seq)
	}
}

func TestSequenceOfMappings(t *testing.T) {
	m := mustParse(t, `
verify:
  - name: build
    run: go build ./...
    timeoutSeconds: 600
  - name: test
    run: go test ./...
`)
	v, _ := m.Get("verify")
	items, ok := v.([]Node)
	if !ok {
		t.Fatalf("verify is %T", v)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	first := items[0].(*Mapping)
	if got := getString(t, first, "name"); got != "build" {
		t.Errorf("name = %q", got)
	}
	if got := getString(t, first, "run"); got != "go build ./..." {
		t.Errorf("run = %q", got)
	}
	if got := getString(t, first, "timeoutSeconds"); got != "600" {
		t.Errorf("timeoutSeconds = %q", got)
	}
	second := items[1].(*Mapping)
	if got := getString(t, second, "name"); got != "test" {
		t.Errorf("second name = %q", got)
	}
}

func TestNestedMappingInsideSequenceItem(t *testing.T) {
	m := mustParse(t, `
fix:
  - strategy: ai
    name: residual
    policy:
      allowPaths:
        - "**/pom.xml"
      maxChangedFiles: 10
`)
	items, _ := m.Get("fix")
	item := items.([]Node)[0].(*Mapping)
	if got := getString(t, item, "strategy"); got != "ai" {
		t.Errorf("strategy = %q", got)
	}
	pol, ok := item.Get("policy")
	if !ok {
		t.Fatal("policy missing")
	}
	pm, ok := pol.(*Mapping)
	if !ok {
		t.Fatalf("policy is %T", pol)
	}
	if got := getString(t, pm, "maxChangedFiles"); got != "10" {
		t.Errorf("maxChangedFiles = %q", got)
	}
	ap, _ := pm.Get("allowPaths")
	if seq := ap.([]Node); len(seq) != 1 || seq[0] != "**/pom.xml" {
		t.Errorf("allowPaths = %#v", ap)
	}
}

func TestLiteralBlockScalar(t *testing.T) {
	m := mustParse(t, `
script: |
  line one
  line two
after: yes
`)
	if got := getString(t, m, "script"); got != "line one\nline two" {
		t.Errorf("script = %q", got)
	}
	if got := getString(t, m, "after"); got != "yes" {
		t.Errorf("after = %q", got)
	}
}

func TestFoldedBlockScalar(t *testing.T) {
	m := mustParse(t, `
summary: >
  wrapped
  text
next: ok
`)
	if got := getString(t, m, "summary"); got != "wrapped text" {
		t.Errorf("summary = %q", got)
	}
	if got := getString(t, m, "next"); got != "ok" {
		t.Errorf("next = %q", got)
	}
}

func TestEmptyInlineSequence(t *testing.T) {
	m := mustParse(t, `deps: []`)
	v, _ := m.Get("deps")
	seq, ok := v.([]Node)
	if !ok || len(seq) != 0 {
		t.Errorf("deps = %#v", v)
	}
}

func TestRejections(t *testing.T) {
	cases := map[string]string{
		"tab indentation":    "root:\n\tkey: value\n",
		"anchors":            "base: &anchor\nother: *anchor\n",
		"inline mapping":     "policy: {a: 1}\n",
		"inline sequence":    "files: [a, b]\n",
		"multiple documents": "a: 1\n---\nb: 2\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(src)); err == nil {
				t.Fatalf("expected an error for %s", name)
			}
		})
	}
}

func TestSyntaxErrorReportsLine(t *testing.T) {
	_, err := Parse([]byte("good: 1\nthis line has no colon\n"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q does not mention line 2", err)
	}
}

func TestReaderUnknownFieldIsAnError(t *testing.T) {
	m := mustParse(t, "name: x\nsevrity: high\n")
	r := NewReader(m, "")
	r.String("name")
	r.CheckUnknown()
	errs := r.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0], "sevrity") {
		t.Errorf("errors = %#v", errs)
	}
}

func TestReaderTypedAccess(t *testing.T) {
	m := mustParse(t, "count: 7\nflag: true\nlist:\n  - a\nbare: solo\n")
	r := NewReader(m, "")
	if got := r.Int("count"); got != 7 {
		t.Errorf("Int = %d", got)
	}
	if !r.Bool("flag") {
		t.Error("Bool = false")
	}
	if got := r.StringSlice("list"); len(got) != 1 || got[0] != "a" {
		t.Errorf("StringSlice = %#v", got)
	}
	// A bare scalar is accepted where a list is expected.
	if got := r.StringSlice("bare"); len(got) != 1 || got[0] != "solo" {
		t.Errorf("StringSlice(bare) = %#v", got)
	}
	if errs := r.Errors(); len(errs) != 0 {
		t.Errorf("unexpected errors %#v", errs)
	}
}

func TestReaderReportsWrongType(t *testing.T) {
	m := mustParse(t, "count: notanumber\n")
	r := NewReader(m, "")
	r.Int("count")
	if errs := r.Errors(); len(errs) != 1 || !strings.Contains(errs[0], "integer") {
		t.Errorf("errors = %#v", errs)
	}
}
