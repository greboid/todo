// Package ui embeds the built Svelte frontend so the Go binary ships as a
// single artifact. FS returns an fs.FS rooted at the build output.
//
// Build output is produced by `go generate` (see ui/generate.go) which runs
// `pnpm install && pnpm build` in web/ and writes the result to ui/dist.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS returns an fs.FS rooted at the embedded build output.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
