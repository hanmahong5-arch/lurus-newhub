package common

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"

	"github.com/gin-contrib/static"
)

// Credit: https://github.com/gin-contrib/static/issues/19

type embedFileSystem struct {
	http.FileSystem
}

func (e *embedFileSystem) Exists(prefix string, path string) bool {
	return StaticFileExists(e, path)
}

func (e *embedFileSystem) Open(name string) (http.File, error) {
	if name == "/" {
		// This will make sure the index page goes to NoRouter handler,
		// which will use the replaced index bytes with analytic codes.
		return nil, os.ErrNotExist
	}
	return e.FileSystem.Open(name)
}

// StaticFileExists reports whether the static middleware should answer a
// path itself: only for a regular file that is not an SPA document.
//
// A directory or an index.html handed to http.FileServer is answered with a
// redirect, not content — "/next" -> "next/", "/x/index.html" -> "./" — and
// that redirect goes out under the one-week Cache-Control middleware.Cache()
// has already set. For the new console's mount that was a loop: /next
// redirected to /next/ and /next/ back to /next, so the "try the new console"
// link never opened (found on the local stack 2026-10-09). Reporting these as
// missing sends them to NoRoute, which serves the right document with
// no-cache. "/" was the one case handled before this, in Open.
func StaticFileExists(fsys http.FileSystem, name string) bool {
	if path.Base(name) == "index.html" {
		return false
	}
	f, err := fsys.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func EmbedFolder(fsEmbed embed.FS, targetPath string) static.ServeFileSystem {
	efs, err := fs.Sub(fsEmbed, targetPath)
	if err != nil {
		panic(err)
	}
	return &embedFileSystem{
		FileSystem: http.FS(efs),
	}
}
