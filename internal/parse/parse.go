// Package parse turns tool-specific output into normalized model.Findings.
//
// Adding support for a new linter or scanner means writing one function here
// and registering it — no engine changes. This is the seam that lets a Go
// binary heal Java, Python and Node repositories.
package parse

import (
	"fmt"
	"sort"
	"sync"

	"github.com/adarshks016/poultice/internal/model"
)

// Input is everything a parser is given.
type Input struct {
	// Output is the captured stdout+stderr of the diagnose command.
	Output string
	// ExitCode is the diagnose command's exit code. Many scanners use a non-zero
	// exit to mean "found problems", which is not a failure.
	ExitCode int
	// RepoDir is the repository root, for parsers that must read files or
	// relativize absolute paths.
	RepoDir string
	// ArtifactPath is the file named by $POULTICE_OUT, for tools that write JSON
	// to disk rather than stdout. Empty when the recipe did not use it.
	ArtifactPath string
}

// Parser converts tool output into findings.
type Parser func(Input) (model.Findings, error)

var (
	mu       sync.RWMutex
	registry = map[string]Parser{}
)

// Register makes a parser available to recipes by name. It panics on duplicate
// registration, which can only be a programming error at init time.
func Register(name string, p Parser) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[name]; dup {
		panic("parse: duplicate parser registered: " + name)
	}
	registry[name] = p
}

// Get returns a registered parser.
func Get(name string) (Parser, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown parser %q (registered: %v)", name, namesLocked())
	}
	return p, nil
}

// Names lists registered parsers, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	return namesLocked()
}

func namesLocked() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
