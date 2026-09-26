package router

// openapi_strict_parse_test.go — the byte-level gate on docs/openapi/.
//
// WHY IT EXISTS: until 2026-09-21 two of the three published JSON specs
// (relay.json and api.json) began with a UTF-8 byte order mark. A standard
// JSON parser rejects those bytes — Go's own encoding/json included — so the
// spec a paying customer needs could not be loaded by the customer's tooling
// at all. The contract lock beside this file was green the whole time because
// it ran bytes.TrimPrefix(raw, {0xEF,0xBB,0xBF}) before parsing. A consumer
// does not get to strip anything, so this gate does not either: it hands the
// bytes exactly as committed to encoding/json and fails if they do not parse.
//
// WHAT IT SCANS: every *.json file under docs/openapi/, discovered by reading
// the directory — not a list written in this file, so a spec added tomorrow is
// covered the day it lands. For each: the raw bytes must carry no BOM, must be
// valid UTF-8, must parse as JSON, must be an OpenAPI 3 document with a
// non-empty paths map, and every documented path template must be mounted on
// the real router (for the specs the contract lock reconciles — see
// contractLockMountedSpecs).
//
// BLIND SPOT — read this before trusting a green run:
//   - It proves the bytes parse and that the documented paths are mounted. It
//     does NOT prove the documented request shapes, response shapes, status
//     codes, headers or examples are what the server actually sends. Nothing
//     here issues a request.
//   - It does not validate the documents against the OpenAPI 3 meta-schema. A
//     structurally nonsense-but-parseable spec passes.
//   - It says nothing about api-v2.yaml's bytes: YAML parsing would mean
//     promoting an indirect module dependency to a direct one. The YAML twin is
//     covered only by the operation-set comparison in
//     TestOpenAPIContract_V2TwinsDocumentTheSameOperations, which is a line
//     scan, not a parse.
//   - Line endings are not checked. .gitattributes already pins *.json to LF,
//     and CRLF would not stop a JSON parser the way a byte order mark does, so
//     this gate has nothing to add there.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// utf8BOM is the three-byte mark that made two published specs unreadable.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// publishedJSONSpecs lists every *.json file in docs/openapi/ by reading the
// directory, so the gate cannot go stale against a spec somebody adds later.
func publishedJSONSpecs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(openapiSpecDir())
	if err != nil {
		t.Fatalf("read %s: %v", openapiSpecDir(), err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	// Scanner-honesty floor: docs/openapi/ has carried three JSON specs since
	// relay.json landed. Zero means the directory walk broke, not that the
	// repository stopped publishing contracts.
	const minPublishedSpecs = 3
	if len(names) < minPublishedSpecs {
		t.Fatalf("found %d published JSON spec(s) in %s, want at least %d — the directory scan is broken", len(names), openapiSpecDir(), minPublishedSpecs)
	}
	return names
}

// TestOpenAPISpecs_ParseAsPublishedBytes is the gate proper: the bytes on disk,
// unmodified, must be something encoding/json accepts.
func TestOpenAPISpecs_ParseAsPublishedBytes(t *testing.T) {
	for _, name := range publishedJSONSpecs(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(openapiSpecDir(), name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if len(raw) == 0 {
				t.Fatalf("%s is empty", name)
			}

			// Checked before the parse so the failure names the real cause:
			// json.Unmarshal's message for a BOM is the unhelpful
			// "invalid character 'ï' looking for beginning of value".
			if len(raw) >= len(utf8BOM) && string(raw[:len(utf8BOM)]) == string(utf8BOM) {
				t.Errorf("%s starts with a UTF-8 byte order mark (EF BB BF). A standard JSON parser rejects it; publish the document without one", name)
			}
			if !utf8.Valid(raw) {
				t.Errorf("%s is not valid UTF-8", name)
			}

			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("%s does not parse as published (no stripping, no preprocessing): %v", name, err)
			}

			version, _ := doc["openapi"].(string)
			if !strings.HasPrefix(version, "3.") {
				t.Errorf("%s declares openapi=%q, want an OpenAPI 3.x document", name, version)
			}
			paths, ok := doc["paths"].(map[string]any)
			if !ok || len(paths) == 0 {
				t.Fatalf("%s has no non-empty paths object", name)
			}
			t.Logf("%s: %d byte(s), openapi %s, %d path template(s)", name, len(raw), version, len(paths))
		})
	}
}

// TestOpenAPISpecs_DocumentedPathsAreMounted restates the doc -> router
// direction from this gate's own point of view, over the specs the contract
// lock reconciles. It is deliberately a near-duplicate of lock (a): if someone
// narrows contractLockMountedSpecs, this test narrows with it and the header
// above stops over-claiming, rather than the claim outliving the check.
func TestOpenAPISpecs_DocumentedPathsAreMounted(t *testing.T) {
	engine := buildContractLockEngine(t)
	registered, wildcardPrefixes := realRouteTable(t, engine)

	if len(contractLockMountedSpecs) == 0 {
		t.Fatal("contractLockMountedSpecs is empty — this gate would pass vacuously")
	}
	for _, spec := range contractLockMountedSpecs {
		t.Run(spec, func(t *testing.T) {
			ops := documentedOperations(t, loadOpenAPIJSONDoc(t, spec), spec)
			var missing []string
			for op := range ops {
				sep := strings.IndexByte(op, ' ')
				key := op[:sep+1] + ginPathFromOpenAPI(op[sep+1:])
				if registered[key] {
					continue
				}
				matched := false
				for _, prefix := range wildcardPrefixes {
					if strings.HasPrefix(key, prefix) {
						matched = true
						break
					}
				}
				if !matched {
					missing = append(missing, key)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("%s documents %d operation(s) the router does not mount: %v", spec, len(missing), missing)
			}
		})
	}
}
