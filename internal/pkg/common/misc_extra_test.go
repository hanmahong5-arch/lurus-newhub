package common

import (
	"embed"
	"testing"
)

//go:embed testdata/embedroot
var testEmbedFS embed.FS

func TestEmbedFolder(t *testing.T) {
	sfs := EmbedFolder(testEmbedFS, "testdata/embedroot")

	// Exists uses Open under the hood; the embedded file must be found.
	if !sfs.Exists("/", "/file.txt") {
		t.Error("embedded file.txt should exist")
	}
	if sfs.Exists("/", "/missing.txt") {
		t.Error("missing file should not exist")
	}

	// Open on a real file returns a handle; Open("/") is redirected to NotExist
	// so the SPA index falls through to the NoRoute handler.
	f, err := sfs.Open("/file.txt")
	if err != nil {
		t.Fatalf("open embedded file: %v", err)
	}
	_ = f.Close()
	if _, err := sfs.Open("/"); err == nil {
		t.Error(`Open("/") must return an error so index goes to NoRoute`)
	}
}

// Directories and index.html documents must never be answered by the static
// middleware: http.FileServer turns them into redirects that carry a week of
// Cache-Control, and for the /next mount the two redirects formed a loop.
func TestEmbedFolder_DirectoriesAndDocumentsAreNotStaticFiles(t *testing.T) {
	sfs := EmbedFolder(testEmbedFS, "testdata/embedroot")
	for _, p := range []string{"/next", "/next/", "/next/index.html", "/index.html"} {
		if sfs.Exists("/", p) {
			t.Errorf("%s must report missing so NoRoute serves the document", p)
		}
	}
	if !sfs.Exists("/", "/next/app.js") {
		t.Error("a regular file under a directory must still be served")
	}
}

func TestInitZitaClient_DisabledWhenUnset(t *testing.T) {
	orig := ZitaClient
	t.Cleanup(func() { ZitaClient = orig })
	ZitaClient = nil

	t.Setenv("IDENTITY_PUBLIC_URL", "")
	t.Setenv("IDENTITY_SESSION_SECRET", "")
	InitZitaClient()
	if ZitaClient != nil {
		t.Error("zita client must stay nil when identity env is unset")
	}
}
