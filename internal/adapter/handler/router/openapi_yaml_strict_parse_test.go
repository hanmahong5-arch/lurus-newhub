package router

// openapi_yaml_strict_parse_test.go — the YAML twin has to be YAML.
//
// WHY IT EXISTS: cycle 14 found the published specs unreadable by standard
// parsers (a byte-order mark on two JSON specs) and gated the JSON bytes in
// openapi_strict_parse_test.go. The same cycle then shipped an api-v2.yaml
// tag description containing an unquoted "Read-only: this spec ...": a
// second ": " inside a plain scalar, which every conforming YAML parser
// rejects ("mapping values are not allowed here"). Nothing noticed, because
// the contract lock reads the YAML twin with patterns rather than a parser.
//
// WHAT IT DOES NOT CHECK: that the YAML describes the same operations as the
// JSON twin (the contract lock's twins test does that), or anything about
// the content beyond "a YAML 1.2 parser accepts these bytes".

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPISpecs_YAMLTwinParsesAsPublishedBytes(t *testing.T) {
	root := repoRootFromRouterPkg()
	path := filepath.Join(root, "docs", "openapi", "api-v2.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("api-v2.yaml is not valid YAML as committed — a customer's code generator cannot read it: %v", err)
	}
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatalf("api-v2.yaml parsed but documents no paths — the parse is not evidence of anything")
	}
}
