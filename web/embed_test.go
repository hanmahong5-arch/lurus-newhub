package web

import (
	"io/fs"
	"os"
	"sort"
	"testing"
)

// Every file the frontend build wrote must be served. go:embed without the
// all: prefix drops names starting with '_' or '.', which is how Vite's
// "_arrayReduce-<hash>.js" chunk went missing from production (see
// embed.go). This compares the embedded tree with the dist directory on
// disk, so it is exact against a real build (the Docker image build, or a
// local `bun run build`) and trivially true against CI's placeholder dist.
func TestBuildFSEmbedsEveryDistFile(t *testing.T) {
	list := func(fsys fs.FS) []string {
		var out []string
		err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk: %v", err)
		}
		sort.Strings(out)
		return out
	}

	embedded, err := fs.Sub(BuildFS, "dist")
	if err != nil {
		t.Fatalf("sub: %v", err)
	}
	onDisk := list(os.DirFS("dist"))
	inBinary := map[string]bool{}
	for _, p := range list(embedded) {
		inBinary[p] = true
	}
	var missing []string
	for _, p := range onDisk {
		if !inBinary[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d built file(s) are not embedded, so production would 404 them: %v", len(missing), missing)
	}
	if len(onDisk) == 0 {
		t.Fatalf("dist is empty — the comparison proved nothing")
	}
}
