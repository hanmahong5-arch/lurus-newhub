// Package web provides embedded frontend assets.
package web

import "embed"

// BuildFS is the built console (web/dist).
//
// The pattern is "all:dist", not "dist": without the all: prefix go:embed
// silently skips every file whose name starts with '_' or '.', and Vite
// names some chunks after the module they hold — lodash's internals give
// "_arrayReduce-<hash>.js". That chunk was absent from the binary, so the
// vendor-logo pack that imports it failed to load in production ("Failed to
// fetch dynamically imported module") and every logo rendered blank, while
// the dev server and the unit tests, which never go through the embed,
// looked fine (found on UAT 2026-09-22). embed_test.go keeps the two
// listings equal.
//
//go:embed all:dist
var BuildFS embed.FS

//go:embed dist/index.html
var IndexPage []byte
