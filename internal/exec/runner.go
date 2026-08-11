// Package exec runs external tools on behalf of the engine.
//
// Everything poultice does to a repository ultimately happens by spawning some
// other program, so this package is deliberately strict: every command gets a
// timeout, a working directory, a captured output buffer with a hard size cap,
// and a process group that is killed as a unit so that a hung `mvn` does not
// leak a forked JVM into the runner.
package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout applies when neither the recipe nor the caller sets one.
const DefaultTimeout = 15 * time.Minute

// MaxCapturedBytes bounds how much output is retained per command. Build logs
// routinely run to hundreds of megabytes; we keep the tail, which is where the
// errors are.
const MaxCapturedBytes = 256 * 1024

// Result is the outcome of one command.
type Result struct {
	Command  string
	ExitCode int
	Output   string
	Duration time.Duration
	TimedOut bool
}

// Success reports a zero exit code.
func (r Result) Success() bool { return r.ExitCode == 0 && !r.TimedOut }

// Tail returns the last n bytes of output, trimmed to a line boundary.
func (r Result) Tail(n int) string {
	if len(r.Output) <= n {
		return r.Output
	}
	cut := r.Output[len(r.Output)-n:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i < len(cut)-1 {
		cut = cut[i+1:]
	}
	return "…\n" + cut
}

// Runner executes commands inside a repository.
type Runner struct {
	// Dir is the working directory for every command.
	Dir string
	// Env is appended to the parent environment.
	Env []string
	// Echo receives a line per command before it runs; may be nil.
	Echo func(string)
}

// New returns a Runner rooted at dir.
func New(dir string) *Runner { return &Runner{Dir: dir} }

// Run executes a shell command with a timeout.
//
// The command string is passed to `sh -c` because recipes are written by humans
// who expect pipes and globs to work. This is a deliberate trust decision:
// recipes are executable content, exactly like a CI config, and must be
// reviewed as such. Untrusted recipes must never be run — see SECURITY.md.
func (r *Runner) Run(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{}, errors.New("empty command")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if r.Echo != nil {
		r.Echo(command)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), r.Env...)
	configureProcessGroup(cmd)

	buf := &cappedBuffer{limit: MaxCapturedBytes}
	cmd.Stdout = buf
	cmd.Stderr = buf

	// WaitDelay ensures that if the context fires, children that ignore SIGKILL
	// on the process group do not block Wait forever.
	cmd.WaitDelay = 10 * time.Second

	runErr := cmd.Run()
	res := Result{
		Command:  command,
		Output:   buf.String(),
		Duration: time.Since(start),
	}

	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.ExitCode = -1
		return res, fmt.Errorf("command timed out after %s: %s", timeout, command)
	}

	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		res.ExitCode = 0
	case errors.As(runErr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		res.ExitCode = -1
		return res, fmt.Errorf("run %q: %w", command, runErr)
	}
	return res, nil
}

// Look reports whether every named executable is on PATH, returning those that
// are not.
func Look(names []string) (missing []string) {
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			missing = append(missing, n)
		}
	}
	return missing
}

// cappedBuffer keeps at most limit bytes, discarding from the front so the tail
// (where compiler and test errors live) survives.
type cappedBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	if _, err := c.buf.Write(p); err != nil {
		return 0, err
	}
	if excess := c.buf.Len() - c.limit; excess > 0 {
		c.buf.Next(excess)
	}
	return n, nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
