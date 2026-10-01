// Package recipes embeds the built-in recipe library, so that a binary from
// `go install` or a release archive can heal a repository that ships no
// recipes of its own.
package recipes

import "embed"

// FS holds every shipped recipe at its root.
//
//go:embed *.yaml
var FS embed.FS
