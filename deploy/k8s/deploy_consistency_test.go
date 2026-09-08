// Package k8s_test pins deployment-facing artifacts (deploy scripts, the
// secret schema doc, runbooks, CI workflow triggers) against drift from the
// live manifest and from each other. These are plain text/YAML files with
// no Go build of their own, so nothing else catches a stale default or a
// resurrected retired identifier before it reaches an operator's terminal
// (OPS-1..4).
package k8s_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot resolves the module root relative to this test file's package
// directory (deploy/k8s/), independent of `go test`'s working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	root = filepath.Dir(root) // deploy/k8s -> deploy -> repo root
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found at resolved repo root %s (deploy_consistency_test.go moved?): %v", root, err)
	}
	return root
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// secretKeyRefEntry is one env-from-secret binding found in the deployment
// manifest.
type secretKeyRefEntry struct {
	key      string
	optional bool
}

// extractSecretKeyRefs does a small block scan (not a full YAML parse — the
// module has no direct YAML dependency and the manifest's structure is
// simple/stable) over deployment.yaml's `env:` entries, pulling the `key:`
// and `optional:` fields that follow each `secretKeyRef:` line.
func extractSecretKeyRefs(t *testing.T, path string) []secretKeyRefEntry {
	t.Helper()
	lines := strings.Split(readFile(t, path), "\n")
	var entries []secretKeyRefEntry
	for i := 0; i < len(lines); i++ {
		if !strings.Contains(lines[i], "secretKeyRef:") {
			continue
		}
		var key string
		optional := false
		for j := i + 1; j < len(lines) && j < i+6; j++ {
			line := strings.TrimSpace(lines[j])
			if strings.HasPrefix(line, "- name:") {
				break // next env entry started
			}
			if strings.HasPrefix(line, "key:") {
				key = strings.TrimSpace(strings.TrimPrefix(line, "key:"))
			}
			if strings.HasPrefix(line, "optional:") {
				optional = strings.TrimSpace(strings.TrimPrefix(line, "optional:")) == "true"
			}
		}
		if key != "" {
			entries = append(entries, secretKeyRefEntry{key: key, optional: optional})
		}
	}
	return entries
}

// extractPatchFieldsBlock returns the concatenated text of every
// scripts/deploy-stage.sh line that assigns into the $patch_fields shell
// variable — the exact "KEY":"value" pairs that end up inside the
// `kubectl patch --type merge` payload sent to the cluster. A key merely
// MENTIONED elsewhere in the script (e.g. the OIDC_CLIENT_ID bootstrap
// guard, which reads the secret but must never write it) is not actually
// sent, so a whole-file substring check can't distinguish "sent" from
// "referenced" — this can.
func extractPatchFieldsBlock(t *testing.T, scriptBody string) string {
	t.Helper()
	var b strings.Builder
	for _, line := range strings.Split(scriptBody, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "patch_fields=") {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// patchFieldKeyLiteral is how a key appears inside a patch_fields
// assignment line: the JSON-object-key form `\"KEY\":` (the surrounding
// bash string is double-quoted, so the literal `"` is backslash-escaped in
// the script source).
func patchFieldKeyLiteral(key string) string {
	return `\"` + key + `\":`
}

// TestDeploySecretConsistency: every non-optional secretKeyRef.key the live
// deployment manifest references must (a) be a key deploy/k8s/r6-stage/secret-template.yaml
// documents, and (b) actually be assigned into scripts/deploy-stage.sh's
// $patch_fields payload (OPS-1) — otherwise a fresh secret bootstrap via the
// script boots a pod that immediately fast-fails on a missing required env
// var. OIDC_CLIENT_ID is a documented, deliberate exception: it must appear
// in the template and in the script's bootstrap guard (which reads it via
// `jsonpath='{.data.OIDC_CLIENT_ID}'`) but must NEVER be assigned into
// patch_fields — it is reconciler-owned, and the script writing it would
// race the platform app-registry reconciler and could stomp a valid value
// with an empty/stale one. TAVILY_API_KEY is optional in deployment.yaml and
// so is exempt from the "must be in patch_fields" requirement (it is in
// fact conditionally assigned there today, but that is not required).
func TestDeploySecretConsistency(t *testing.T) {
	root := repoRoot(t)
	deploymentPath := filepath.Join(root, "deploy", "k8s", "r6-stage", "deployment.yaml")
	entries := extractSecretKeyRefs(t, deploymentPath)
	if len(entries) == 0 {
		t.Fatalf("no secretKeyRef entries found in %s — parser broken or fixture moved", deploymentPath)
	}

	scriptBody := readFile(t, filepath.Join(root, "scripts", "deploy-stage.sh"))
	templateBody := readFile(t, filepath.Join(root, "deploy", "k8s", "r6-stage", "secret-template.yaml"))
	patchFields := extractPatchFieldsBlock(t, scriptBody)
	if patchFields == "" {
		t.Fatalf("no patch_fields= assignment lines found in scripts/deploy-stage.sh — parser broken or script restructured")
	}

	for _, e := range entries {
		if e.optional {
			continue
		}
		if !strings.Contains(templateBody, e.key) {
			t.Errorf("non-optional secret key %q (from deployment.yaml) is missing from deploy/k8s/r6-stage/secret-template.yaml", e.key)
		}

		if e.key == "OIDC_CLIENT_ID" {
			if !strings.Contains(scriptBody, "jsonpath='{.data.OIDC_CLIENT_ID}'") {
				t.Errorf("OIDC_CLIENT_ID: expected the bootstrap guard's jsonpath read (jsonpath='{.data.OIDC_CLIENT_ID}') in scripts/deploy-stage.sh, not found")
			}
			if strings.Contains(patchFields, patchFieldKeyLiteral(e.key)) {
				t.Errorf("OIDC_CLIENT_ID must never be assigned into $patch_fields in scripts/deploy-stage.sh — it is reconciler-owned, not written by this script")
			}
			continue
		}

		if !strings.Contains(patchFields, patchFieldKeyLiteral(e.key)) {
			t.Errorf("non-optional secret key %q (from deployment.yaml) is not assigned into $patch_fields in scripts/deploy-stage.sh — a fresh secret created by that script would boot a pod that fast-fails on this env var", e.key)
		}
	}
}

// healthURLHostPath extracts the host+path of a script's `HEALTH_URL`
// default via `HEALTH_URL="${HEALTH_URL:-<url>}"`.
var healthURLDefaultRe = regexp.MustCompile(`HEALTH_URL="\$\{HEALTH_URL:-(https?://[^}"]+)\}"`)

func healthURLHostPath(t *testing.T, path string) (host, urlPath string) {
	t.Helper()
	body := readFile(t, path)
	m := healthURLDefaultRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no HEALTH_URL=\"${HEALTH_URL:-...}\" default found in %s", path)
	}
	rest := strings.TrimPrefix(m[1], "https://")
	rest = strings.TrimPrefix(rest, "http://")
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return rest, ""
	}
	return rest[:slash], rest[slash:]
}

// TestHealthURLDefaultsConsistent: deploy-stage.sh and stage-rollback.sh
// must default HEALTH_URL to the same host (hub.lurus.cn — the production
// newhub instance these scripts deploy; test-newhub.lurus.cn is the
// separate, isolated UAT instance since the 2026-08-30 domain cutover and
// is not touched by either script) and the same deep-health
// path (/api/health, not /api/status — see doc/runbook/staging-deploy.md).
func TestHealthURLDefaultsConsistent(t *testing.T) {
	root := repoRoot(t)
	for _, script := range []string{"deploy-stage.sh", "stage-rollback.sh"} {
		host, path := healthURLHostPath(t, filepath.Join(root, "scripts", script))
		if host != "hub.lurus.cn" {
			t.Errorf("%s HEALTH_URL default host = %q, want hub.lurus.cn", script, host)
		}
		if path != "/api/health" {
			t.Errorf("%s HEALTH_URL default path = %q, want /api/health", script, path)
		}
	}
}

// retiredIdentifierChecks are (label, regex) pairs for identifiers that
// belonged exclusively to the service retired 2026-04-23 (`lurus-api`,
// ns `lurus-system` as ITS namespace, host `100.98.57.55`, db `lurus_hub`)
// or to domains/files retired later (hub-stage.lurus.cn, staging-environment.md).
// `kubectl -n lurus-system` is checked as an exact operational-command
// substring — bare `lurus-system` is NOT banned, it is newhub's own live
// Redis namespace.
var retiredIdentifierChecks = []struct {
	label string
	re    *regexp.Regexp
}{
	{"deploy/lurus-api", regexp.MustCompile(`deploy/lurus-api`)},
	{"app=lurus-api", regexp.MustCompile(`app=lurus-api`)},
	{"lurus-api-secrets", regexp.MustCompile(`lurus-api-secrets`)},
	{"100.98.57.55", regexp.MustCompile(`100\.98\.57\.55`)},
	{"hub-stage.lurus.cn", regexp.MustCompile(`hub-stage\.lurus\.cn`)},
	{"lurus_hub (db name)", regexp.MustCompile(`\blurus_hub\b`)},
	{"staging-environment.md", regexp.MustCompile(`staging-environment\.md`)},
	{"kubectl -n lurus-system", regexp.MustCompile(`kubectl -n lurus-system`)},
}

// retiredIdentifierAllowlist: files allowed to mention retired identifiers
// because they are explicitly the historical record of the retirement
// itself, not operational guidance.
var retiredIdentifierAllowlist = map[string]bool{
	filepath.Join("doc", "runbook", "deployment.md"): true,
}

// retiredIdentifierMaxFileSize bounds the repo-root file scan below — build
// artifacts (e.g. a locally-built server.exe binary) can be 100MB+ and are
// never operational guidance text, so reading them in full would only slow
// the test down for no coverage benefit.
const retiredIdentifierMaxFileSize = 2 << 20 // 2MiB

// TestNoRetiredIdentifiers scans every doc/runbook, scripts and deploy file,
// plus the repo root's own top-level files (non-recursive — Deploy-To-K3s.ps1
// hardcoded a retired host at the repo root and none of the directory walks
// below ever looked at it; a recursive root walk would also crawl .git/ and
// web/'s JS dependency tree for no benefit, hence non-recursive), for
// identifiers that belong only to the service retired 2026-04-23 (or to
// domains/files retired later). A resurrected identifier here means an
// operator following the doc/script ends up pointed at a host, namespace or
// database that either doesn't exist or serves something else entirely.
func TestNoRetiredIdentifiers(t *testing.T) {
	root := repoRoot(t)
	var files []string
	for _, dir := range []string{"doc/runbook", "scripts", "deploy"} {
		full := filepath.Join(root, filepath.FromSlash(dir))
		err := filepath.Walk(full, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			files = append(files, p)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", full, err)
		}
	}
	rootEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read repo root %s: %v", root, err)
	}
	for _, e := range rootEntries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", e.Name(), err)
		}
		if info.Size() > retiredIdentifierMaxFileSize {
			continue
		}
		files = append(files, filepath.Join(root, e.Name()))
	}
	if len(files) == 0 {
		t.Fatalf("no files found under doc/runbook, scripts, deploy, or repo root — scan is broken")
	}

	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			t.Fatalf("rel path for %s: %v", f, err)
		}
		if retiredIdentifierAllowlist[rel] {
			continue
		}
		if strings.HasSuffix(rel, filepath.Join("deploy", "k8s", "deploy_consistency_test.go")) {
			continue // this file's own pattern literals are not the identifiers themselves
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		body := string(data)
		for _, chk := range retiredIdentifierChecks {
			if chk.re.MatchString(body) {
				t.Errorf("%s: contains retired identifier %q", rel, chk.label)
			}
		}
	}
}

// TestScriptsMigrateAbsent: the standalone MySQL->Postgres migration tool
// (its own go.mod, never wired into the module build) predates the 2026-06
// PG-only cutover that deleted MySQL support entirely — it has had nothing
// to migrate FROM since, and nothing referenced it (TI/OPS-3).
func TestScriptsMigrateAbsent(t *testing.T) {
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "scripts", "migrate")); !os.IsNotExist(err) {
		t.Errorf("scripts/migrate/ still exists (err=%v) — dead MySQL->PG one-shot tool, PG-only since 2026-06", err)
	}
}

// mdLinkRe matches `[label](target.md)` markdown links.
var mdLinkRe = regexp.MustCompile(`\[[^\]]+\]\(([^)]+\.md)\)`)

// TestRunbookIndexLinksResolve: every runbook INDEX.md link must resolve to
// a real file in the same directory — a dead link here sends an on-call
// engineer at 3am to a 404 instead of the runbook.
func TestRunbookIndexLinksResolve(t *testing.T) {
	root := repoRoot(t)
	indexPath := filepath.Join(root, "doc", "runbook", "INDEX.md")
	body := readFile(t, indexPath)
	matches := mdLinkRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatalf("no markdown links found in %s — parser broken or fixture moved", indexPath)
	}
	dir := filepath.Dir(indexPath)
	for _, m := range matches {
		target := filepath.Join(dir, filepath.FromSlash(m[1]))
		if _, err := os.Stat(target); err != nil {
			t.Errorf("INDEX.md link target does not exist: %s (%v)", m[1], err)
		}
	}
}

// workflowOnBlockRe grabs the `on:` trigger block of a GitHub Actions
// workflow (everything from `on:` up to the next top-level key).
var workflowOnBlockRe = regexp.MustCompile(`(?ms)^on:\n(.*?)^\S`)

// TestWorkflowsHaveReachableTriggers: no workflow may trigger on a
// `push: tags:` push at all. This repo's release process is exclusively
// merge-to-main -> docker-image-main.yml -> auto-pin -> ArgoCD (see
// doc/runbook/staging-deploy.md); it has never used git tags to ship
// anything, so a tag-triggered workflow — even one that ALSO offers
// workflow_dispatch as a fallback trigger, as the two deleted ones did — is
// dead weight nobody remembers to invoke by hand, plus (for release.yml)
// carried `contents: write` for a path that never fires (OPS-4).
func TestWorkflowsHaveReachableTriggers(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var workflows []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") {
			workflows = append(workflows, name)
		}
	}
	if len(workflows) == 0 {
		t.Fatalf("no workflow files found in %s — scan is broken", dir)
	}

	for _, name := range workflows {
		body := readFile(t, filepath.Join(dir, name))
		// Append a sentinel top-level key so the trailing `on:` block (if the
		// file ends without another top-level key following it) still
		// matches workflowOnBlockRe's lookahead for "next top-level key".
		block := workflowOnBlockRe.FindStringSubmatch(body + "\n\x00")
		if block == nil {
			t.Errorf("%s: could not find an `on:` trigger block", name)
			continue
		}
		if strings.Contains(block[1], "tags:") {
			t.Errorf("%s: triggers on push:tags: — this repo's release path is merge-to-main -> docker-image-main.yml -> ArgoCD, never git tags", name)
		}
	}
}
