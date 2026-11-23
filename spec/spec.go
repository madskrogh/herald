// Package spec holds Herald's spec, so that the code's checks and the
// scenario's harness read the same files a person reads, from any package.
package spec

import "embed"

// Files holds the spec's Markdown files: its requirements, the limits table
// and the shape files, such as span-schema.md.
//
//go:embed *.md
var Files embed.FS
