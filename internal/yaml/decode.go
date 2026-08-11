package yaml

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Reader is a typed, error-accumulating view over a Mapping.
//
// Recipes are written by hand and get things wrong, so the goal here is to
// report every mistake in one pass with a line number, rather than failing at
// the first one and making the author iterate.
type Reader struct {
	m      *Mapping
	path   string
	errs   *[]string
	known  map[string]bool
	strict bool
}

// NewReader returns a Reader over m. Errors accumulate into a shared slice.
func NewReader(m *Mapping, path string) *Reader {
	errs := &[]string{}
	return &Reader{m: m, path: path, errs: errs, known: map[string]bool{}, strict: true}
}

func (r *Reader) child(m *Mapping, name string) *Reader {
	return &Reader{m: m, path: r.join(name), errs: r.errs, known: map[string]bool{}, strict: r.strict}
}

func (r *Reader) join(name string) string {
	if r.path == "" {
		return name
	}
	return r.path + "." + name
}

func (r *Reader) fail(key, format string, args ...any) {
	loc := ""
	if line := r.m.Line(key); line > 0 {
		loc = fmt.Sprintf(" (line %d)", line)
	}
	*r.errs = append(*r.errs, r.join(key)+loc+": "+fmt.Sprintf(format, args...))
}

// Errors returns everything accumulated, sorted for stable output.
func (r *Reader) Errors() []string {
	out := append([]string(nil), *r.errs...)
	sort.Strings(out)
	return out
}

// String reads a scalar string.
func (r *Reader) String(key string) string {
	r.known[key] = true
	v, ok := r.m.Get(key)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		r.fail(key, "expected a string")
		return ""
	}
	return s
}

// Int reads a scalar integer.
func (r *Reader) Int(key string) int {
	r.known[key] = true
	v, ok := r.m.Get(key)
	if !ok {
		return 0
	}
	s, ok := v.(string)
	if !ok {
		r.fail(key, "expected an integer")
		return 0
	}
	if strings.TrimSpace(s) == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		r.fail(key, "expected an integer, got %q", s)
		return 0
	}
	return n
}

// Bool reads a scalar boolean.
func (r *Reader) Bool(key string) bool {
	r.known[key] = true
	v, ok := r.m.Get(key)
	if !ok {
		return false
	}
	s, ok := v.(string)
	if !ok {
		r.fail(key, "expected a boolean")
		return false
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on":
		return true
	case "false", "no", "off", "":
		return false
	default:
		r.fail(key, "expected true or false, got %q", s)
		return false
	}
}

// StringSlice reads a sequence of scalars. A bare scalar is accepted as a
// one-element list, which is what recipe authors always mean.
func (r *Reader) StringSlice(key string) []string {
	r.known[key] = true
	v, ok := r.m.Get(key)
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []Node:
		out := make([]string, 0, len(t))
		for i, item := range t {
			s, ok := item.(string)
			if !ok {
				r.fail(key, "element %d is not a string", i)
				continue
			}
			out = append(out, s)
		}
		return out
	default:
		r.fail(key, "expected a list of strings")
		return nil
	}
}

// Mapping reads a nested mapping, returning nil when absent.
func (r *Reader) Mapping(key string) *Reader {
	r.known[key] = true
	v, ok := r.m.Get(key)
	if !ok {
		return nil
	}
	m, ok := v.(*Mapping)
	if !ok {
		if s, isStr := v.(string); isStr && s == "" {
			return nil
		}
		r.fail(key, "expected a mapping")
		return nil
	}
	return r.child(m, key)
}

// MappingSlice reads a sequence of mappings.
func (r *Reader) MappingSlice(key string) []*Reader {
	r.known[key] = true
	v, ok := r.m.Get(key)
	if !ok {
		return nil
	}
	items, ok := v.([]Node)
	if !ok {
		r.fail(key, "expected a list")
		return nil
	}
	out := make([]*Reader, 0, len(items))
	for i, item := range items {
		m, ok := item.(*Mapping)
		if !ok {
			r.fail(key, "element %d is not a mapping", i)
			continue
		}
		out = append(out, r.child(m, fmt.Sprintf("%s[%d]", key, i)))
	}
	return out
}

// Has reports whether a key is present.
func (r *Reader) Has(key string) bool {
	if r == nil {
		return false
	}
	_, ok := r.m.Get(key)
	return ok
}

// CheckUnknown records an error for every key that was never read. A typo in a
// recipe should be loud: silently ignoring `severty:` would mean silently
// ignoring the author's intent.
func (r *Reader) CheckUnknown() {
	if r == nil {
		return
	}
	for _, k := range r.m.Keys() {
		if !r.known[k] {
			r.fail(k, "unknown field")
		}
	}
}
