// Package yaml decodes the subset of YAML that poultice recipes are written in.
//
// Why not gopkg.in/yaml.v3? Because poultice runs inside CI with credentials
// and write access to the repository it is healing. Every third-party package
// it links is a supply-chain edge into that position, so the bar for adding one
// is high, and a recipe file does not need a general-purpose YAML engine.
//
// # Supported subset
//
//	key: value                # mapping with scalar values
//	key:                      # nested mapping (indentation-based)
//	  nested: value
//	list:                     # sequence of scalars
//	  - one
//	  - two
//	items:                    # sequence of mappings
//	  - name: a
//	    run: b
//	  - name: c
//	block: |                  # literal block scalar (newlines preserved)
//	  line one
//	  line two
//	folded: >                 # folded block scalar (newlines become spaces)
//	  wrapped
//	  text
//	quoted: "a: b"            # double- and single-quoted scalars
//	empty: []                 # empty inline sequence
//	flag: true                # bool, int and string scalars
//
// Deliberately unsupported, and rejected with a line number rather than
// silently misread: anchors and aliases, multiple documents, complex keys,
// inline (flow) collections other than the empty forms, and tab indentation.
package yaml

import (
	"fmt"
	"strconv"
	"strings"
)

// SyntaxError reports a problem at a specific line.
type SyntaxError struct {
	Line int
	Msg  string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

func errf(line int, format string, args ...any) error {
	return &SyntaxError{Line: line, Msg: fmt.Sprintf(format, args...)}
}

// Node is a decoded YAML value: a *Mapping, a []Node, or a string.
type Node any

// Mapping is an ordered YAML mapping. Order is preserved so that error
// messages and round-trips follow the author's file rather than a random
// map iteration.
type Mapping struct {
	keys   []string
	values map[string]Node
	lines  map[string]int
}

// NewMapping returns an empty Mapping.
func NewMapping() *Mapping {
	return &Mapping{values: map[string]Node{}, lines: map[string]int{}}
}

func (m *Mapping) set(key string, v Node, line int) {
	if _, exists := m.values[key]; !exists {
		m.keys = append(m.keys, key)
	}
	m.values[key] = v
	m.lines[key] = line
}

// Get returns the value for key.
func (m *Mapping) Get(key string) (Node, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.values[key]
	return v, ok
}

// Keys returns the keys in document order.
func (m *Mapping) Keys() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.keys...)
}

// Line returns the source line a key was defined on.
func (m *Mapping) Line(key string) int {
	if m == nil {
		return 0
	}
	return m.lines[key]
}

// line is one significant input line, already stripped of comments.
type line struct {
	num    int
	indent int
	text   string
}

// Parse decodes a YAML document into a *Mapping.
func Parse(src []byte) (*Mapping, error) {
	lines, err := scan(string(src))
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return NewMapping(), nil
	}
	p := &parser{lines: lines}
	m, err := p.mapping(lines[0].indent)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.lines) {
		return nil, errf(p.lines[p.pos].num, "unexpected indentation")
	}
	return m, nil
}

// scan strips comments and blank lines, rejects tabs, and records indentation.
// Block scalars are reassembled here so the parser sees them as single lines.
func scan(src string) ([]line, error) {
	raw := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []line

	for i := 0; i < len(raw); i++ {
		text := raw[i]
		num := i + 1

		if strings.HasPrefix(strings.TrimSpace(text), "---") {
			if len(out) > 0 {
				return nil, errf(num, "multiple documents are not supported")
			}
			continue
		}
		if idx := strings.IndexByte(text, '\t'); idx >= 0 && strings.TrimSpace(text[:idx]) == "" {
			return nil, errf(num, "tab indentation is not allowed; use spaces")
		}

		stripped := stripComment(text)
		if strings.TrimSpace(stripped) == "" {
			continue
		}
		indent := len(stripped) - len(strings.TrimLeft(stripped, " "))
		content := strings.TrimRight(strings.TrimLeft(stripped, " "), " ")

		// A block scalar swallows the following more-indented lines.
		if marker, ok := blockMarker(content); ok {
			body, consumed, err := readBlock(raw, i+1, indent, marker)
			if err != nil {
				return nil, err
			}
			key := strings.TrimSpace(content[:strings.LastIndex(content, ":")+1])
			out = append(out, line{num: num, indent: indent, text: key + " " + quoteForParser(body)})
			i += consumed
			continue
		}

		out = append(out, line{num: num, indent: indent, text: content})
	}
	return out, nil
}

// blockMarker reports whether a line ends with a `|` or `>` block indicator.
func blockMarker(content string) (byte, bool) {
	colon := strings.LastIndex(content, ":")
	if colon < 0 {
		return 0, false
	}
	rest := strings.TrimSpace(content[colon+1:])
	if rest == "|" || rest == "|-" {
		return '|', true
	}
	if rest == ">" || rest == ">-" {
		return '>', true
	}
	return 0, false
}

// readBlock collects the indented body of a block scalar.
func readBlock(raw []string, start, parentIndent int, marker byte) (string, int, error) {
	var body []string
	consumed := 0
	blockIndent := -1

	for i := start; i < len(raw); i++ {
		text := raw[i]
		if strings.TrimSpace(text) == "" {
			body = append(body, "")
			consumed++
			continue
		}
		indent := len(text) - len(strings.TrimLeft(text, " "))
		if indent <= parentIndent {
			break
		}
		if blockIndent < 0 {
			blockIndent = indent
		}
		if indent < blockIndent {
			return "", 0, errf(i+1, "inconsistent block scalar indentation")
		}
		body = append(body, text[blockIndent:])
		consumed++
	}

	// Trailing blank lines belong to the document, not the scalar.
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
		consumed--
	}

	if marker == '>' {
		return strings.Join(body, " "), consumed, nil
	}
	return strings.Join(body, "\n"), consumed, nil
}

// quoteForParser re-encodes a block body as a double-quoted scalar so the
// value parser can consume it uniformly.
func quoteForParser(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// stripComment removes a trailing `#` comment that is outside quotes.
func stripComment(s string) string {
	var inSingle, inDouble bool
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '\\':
			if inDouble {
				i++
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || s[i-1] == ' ') {
				return s[:i]
			}
		}
	}
	return s
}

type parser struct {
	lines []line
	pos   int
}

func (p *parser) peek() (line, bool) {
	if p.pos >= len(p.lines) {
		return line{}, false
	}
	return p.lines[p.pos], true
}

// mapping parses consecutive `key: value` lines at the given indent.
func (p *parser) mapping(indent int) (*Mapping, error) {
	m := NewMapping()
	for {
		ln, ok := p.peek()
		if !ok || ln.indent < indent {
			return m, nil
		}
		if ln.indent > indent {
			return nil, errf(ln.num, "unexpected indentation")
		}
		if strings.HasPrefix(ln.text, "- ") || ln.text == "-" {
			return m, nil
		}

		key, rest, err := splitKey(ln)
		if err != nil {
			return nil, err
		}
		p.pos++

		if rest != "" {
			v, err := parseScalar(rest, ln.num)
			if err != nil {
				return nil, err
			}
			m.set(key, v, ln.num)
			continue
		}

		child, ok := p.peek()
		if !ok || child.indent <= indent {
			// `key:` with nothing under it is an explicit empty value.
			m.set(key, "", ln.num)
			continue
		}
		if strings.HasPrefix(child.text, "- ") || child.text == "-" {
			seq, err := p.sequence(child.indent)
			if err != nil {
				return nil, err
			}
			m.set(key, seq, ln.num)
			continue
		}
		sub, err := p.mapping(child.indent)
		if err != nil {
			return nil, err
		}
		m.set(key, sub, ln.num)
	}
}

// sequence parses `- ` items at the given indent.
func (p *parser) sequence(indent int) ([]Node, error) {
	var out []Node
	for {
		ln, ok := p.peek()
		if !ok || ln.indent < indent {
			return out, nil
		}
		if ln.indent > indent {
			return nil, errf(ln.num, "unexpected indentation in sequence")
		}
		if !strings.HasPrefix(ln.text, "- ") && ln.text != "-" {
			return out, nil
		}

		item := strings.TrimSpace(strings.TrimPrefix(ln.text, "-"))
		p.pos++

		// `- key: value` starts a mapping whose first key sits on the dash line.
		if k, rest, ok := trySplitKey(item); ok {
			itemIndent := indent + 2
			sub := NewMapping()
			if rest != "" {
				v, err := parseScalar(rest, ln.num)
				if err != nil {
					return nil, err
				}
				sub.set(k, v, ln.num)
			} else {
				nested, ok := p.peek()
				if ok && nested.indent > itemIndent {
					child, err := p.mapping(nested.indent)
					if err != nil {
						return nil, err
					}
					sub.set(k, child, ln.num)
				} else {
					sub.set(k, "", ln.num)
				}
			}
			// Remaining keys of this item are indented to align past the dash.
			for {
				next, ok := p.peek()
				if !ok || next.indent != itemIndent || strings.HasPrefix(next.text, "- ") {
					break
				}
				rest, err := p.mapping(itemIndent)
				if err != nil {
					return nil, err
				}
				for _, k := range rest.Keys() {
					v, _ := rest.Get(k)
					sub.set(k, v, rest.Line(k))
				}
				break
			}
			out = append(out, sub)
			continue
		}

		if item == "" {
			return nil, errf(ln.num, "empty sequence item")
		}
		v, err := parseScalar(item, ln.num)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

// splitKey parses `key:` or `key: value`.
func splitKey(ln line) (key, rest string, err error) {
	k, r, ok := trySplitKey(ln.text)
	if !ok {
		return "", "", errf(ln.num, "expected \"key: value\", got %q", ln.text)
	}
	return k, r, nil
}

// trySplitKey finds the colon that separates a key from its value, ignoring
// colons inside quotes.
func trySplitKey(s string) (key, rest string, ok bool) {
	var inSingle, inDouble bool
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ':':
			if inSingle || inDouble {
				continue
			}
			// A colon only separates when followed by space or end of line;
			// this keeps `url: https://x` and `image: repo:tag` intact.
			if i+1 < len(s) && s[i+1] != ' ' {
				continue
			}
			key = strings.TrimSpace(s[:i])
			rest = strings.TrimSpace(s[i+1:])
			if key == "" {
				return "", "", false
			}
			return unquote(key), rest, true
		}
	}
	return "", "", false
}

// parseScalar decodes a scalar value.
func parseScalar(s string, num int) (Node, error) {
	switch s {
	case "[]":
		return []Node{}, nil
	case "{}":
		return NewMapping(), nil
	case "~", "null":
		return "", nil
	}
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") {
		return nil, errf(num, "inline collections are not supported; use block style")
	}
	if strings.HasPrefix(s, "&") || strings.HasPrefix(s, "*") {
		return nil, errf(num, "anchors and aliases are not supported")
	}
	return unquote(s), nil
}

// unquote removes surrounding quotes and resolves escapes in double-quoted
// scalars.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if out, err := strconv.Unquote(s); err == nil {
			return out
		}
		return s[1 : len(s)-1]
	}
	return s
}
