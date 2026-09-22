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
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
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
	// ns lurus-staging never materialised on R6 (the pg-access-control netpol
	// that once required it is gone; the live ns is lurus-newhub, with
	// lurus-newhub-uat for the isolated UAT instance). Matched only in its
	// OPERATIONAL shapes -- `-n lurus-staging`, `--namespace lurus-staging`,
	// `namespace: lurus-staging`, "namespace `lurus-staging`" -- so a sentence
	// that merely names the retired ns while explaining that it is retired is
	// not a finding. A command is.
	{"lurus-staging (namespace)", regexp.MustCompile("(?:-n|--namespace[ =]|namespace[:s]?)[ \t]*`?lurus-staging")},
	// The PG pod is lurus-pg-0. lurus-pg-1 has never existed on this cluster;
	// a command naming it fails with "pod not found" after the operator has
	// already typed the rest of a restore.
	{"lurus-pg-1 (PG pod)", regexp.MustCompile(`\blurus-pg-1\b`)},
	// scripts/pg-restore-drill.sh was deleted 2026-09-19: it exercised a
	// wal-g/S3 path the deployment does not use and silently skipped when
	// WALG_S3_PREFIX was unset, so a green run proved nothing.
	{"pg-restore-drill.sh", regexp.MustCompile(`pg-restore-drill\.sh`)},
}

// retiredIdentifierAllowlist: files allowed to mention retired identifiers,
// mapped to the reason. Two kinds of entry live here and the reason says
// which: (a) the file is explicitly the historical record of the retirement
// itself, not operational guidance; (b) the mention is a real stale
// reference that the cycle-13 wiring pass did not own the file to fix, in
// which case the reason says so and names the owner follow-up.
//
// A reason is not optional: an entry without one is a hole, not an
// exemption. TestRetiredIdentifierAllowlistIsNotStale additionally fails when
// an allowlisted file stops matching any pattern, so a cleaned-up file cannot
// keep a silent exemption that would cover a future regression.
var retiredIdentifierAllowlist = map[string]string{
	filepath.Join("doc", "runbook", "deployment.md"):                         "narrates the 2026-04-23 retirement of lurus-api/ns lurus-system as history",
	filepath.Join("doc", "runbook", "seam-s1-activation.md"):                 "cycle-13 L10 rewrote the stale commands and kept the old ns/pod names in dated corrective sentences",
	filepath.Join("doc", "uat-handbook.md"):                                  "section 2 is struck through under a 2026-09-20 RETIRED banner that names lurus-staging precisely to say it never existed",
	filepath.Join("doc", "runbook", "industrial-readiness-gated-actions.md"): "2026-06 gated-action audit record; every lurus-pg-1 mention is already followed by the in-line correction naming lurus-pg-0",
	filepath.Join("doc", "runbook", "database.md"):                           "one retirement sentence recording that scripts/pg-restore-drill.sh was deleted 2026-09-19",
	filepath.Join("doc", "runbook", "pg-restore.md"):                         "same retirement sentence, English side",
	filepath.Join("deploy", "k8s", "r6-stage", "README.md"):                  "corrective record: each mention is of the form \"an earlier revision said X, which was wrong\"",
	filepath.Join("doc", "seam-s1-stage-worklog-2026-06-21.md"):              "dated 2026-06-21 worklog; the 100.98.57.55 mentions ARE the record of the R1-vs-R6 misidentification the same page then corrects",
	// The wiring pass's (b) entries — real stale references it found in files
	// it did not own (oidc-troubleshooting.md's kubectl selector,
	// stage-rollback.sh's Mechanism: header, docker-compose.yml's drill
	// comment) — were fixed in the operator hand-finish and their rows
	// removed; TestRetiredIdentifierAllowlistIsNotStale is what forces that
	// removal. doc/process.md's row went the same way on 2026-09-22: the
	// entries that named the retired drill script were part of the
	// 2026-02→05 progress log that the doc slim deleted, so the file is back
	// under the gate with no exemption.
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
	files := retiredIdentifierScanFiles(t, root)

	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			t.Fatalf("rel path for %s: %v", f, err)
		}
		if _, allowed := retiredIdentifierAllowlist[rel]; allowed {
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

// TestRetiredIdentifierAllowlistIsNotStale: an allowlist entry that no longer
// matches anything is an exemption nobody is paying attention to, and it would
// silently cover the next regression in that file. Delete the entry when the
// file is cleaned up.
func TestRetiredIdentifierAllowlistIsNotStale(t *testing.T) {
	root := repoRoot(t)
	for rel, reason := range retiredIdentifierAllowlist {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s is allowlisted with no reason — an exemption without a reason is a hole", rel)
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("allowlisted file %s cannot be read (%v) — remove the entry if the file is gone", rel, err)
			continue
		}
		matched := false
		for _, chk := range retiredIdentifierChecks {
			if chk.re.Match(data) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s no longer contains any retired identifier — delete its allowlist entry (%q) so the gate covers the file again", rel, reason)
		}
	}
}

// retiredIdentifierScanFiles collects everything TestNoRetiredIdentifiers
// reads: doc/runbook, scripts and deploy recursively, doc/*.md at the top
// level (doc/uat-handbook.md and doc/process.md live there, not under
// doc/runbook/), and the repo root's own top-level files.
func retiredIdentifierScanFiles(t *testing.T, root string) []string {
	t.Helper()
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
	docEntries, err := os.ReadDir(filepath.Join(root, "doc"))
	if err != nil {
		t.Fatalf("read doc/: %v", err)
	}
	docMarkdown := 0
	for _, e := range docEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		docMarkdown++
		files = append(files, filepath.Join(root, "doc", e.Name()))
	}
	if docMarkdown == 0 {
		t.Fatal("no top-level doc/*.md files found — the walk added in cycle 13 is scanning nothing")
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
		t.Fatalf("no files found under doc/runbook, scripts, deploy, doc/*.md or repo root — scan is broken")
	}
	return files
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

// deployEnvValue returns the value of the named container env var in a
// deployment manifest, or "" if the manifest does not set it. The manifests
// are hand-written with one `- name: X` / `value: "Y"` pair per entry (see
// SYNC_FREQUENCY in both); this parses that shape. It is not a YAML parser and
// does not follow valueFrom/secretKeyRef entries, which is fine for the two
// plain-value keys below.
func deployEnvValue(body, name string) string {
	re := regexp.MustCompile(`(?m)^\s*-\s*name:\s*` + regexp.QuoteMeta(name) + `\s*\n\s*value:\s*"?([^"\n]*)"?\s*$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// maxPoolConnectionBudget is the per-manifest ceiling on PostgreSQL
// connections this service may open across all replicas of ONE deployment.
//
// Measured, not guessed (2026-09-20, R6 namespace `database`, pod
// `lurus-pg-0`): max_connections = 100, with 38 in use — newhub 13,
// zitadel 8, newhub_uat 5, identity 3, lucrum 3, other 6. Two newhub
// deployments share
// that server (r6-stage at 3 replicas, r6-uat at 1), so a 40-per-manifest
// ceiling caps this service at 80 of the 100 in the pathological case where
// both deployments saturate at once, and today's real arithmetic is far
// under it: 3 x 12 = 36 for r6-stage and 1 x 12 = 12 for r6-uat.
//
// Whether to raise the server's max_connections at all is owner item O-pool;
// this number does not depend on that answer.
const maxPoolConnectionBudget = 40

// deployPoolCount reports how many connection pools ONE replica of this
// manifest opens. The code opens a second pool only when LOG_SQL_DSN is set:
// InitLogDB aliases LOG_DB to DB otherwise (internal/adapter/repo). The
// previous version of this gate multiplied by 2 unconditionally, which made
// every manifest look twice as expensive as it is and was the stated reason
// the old ceiling had to be so loose.
func deployPoolCount(body string) int {
	if regexp.MustCompile(`(?m)^\s*-\s*name:\s*LOG_SQL_DSN\s*$`).MatchString(body) {
		return 2
	}
	return 1
}

// TestDeploymentPoolBudget: a manifest that declares a container memory limit
// is a manifest whose resource ceiling somebody thought about, and it must
// declare its database-connection ceiling too rather than inherit the code
// default (1000 per pool, internal/adapter/repo/main.go currentPoolSettings),
// plus its heap ceiling (GOMEMLIMIT — without it the Go heap grows with no
// idea of the cgroup limit it is OOM-killed against). The replica count enters
// only the budget arithmetic: keying the requirement on replicas>1 would let
// the single-replica UAT manifest keep the 1000 default against the same
// PostgreSQL instance production uses.
func TestDeploymentPoolBudget(t *testing.T) {
	root := repoRoot(t)
	manifests := []string{
		"deploy/k8s/r6-stage/deployment.yaml",
		"deploy/k8s/r6-uat/deployment.yaml",
	}
	replicasRe := regexp.MustCompile(`(?m)^\s*replicas:\s*(\d+)\s*$`)
	memLimitRe := regexp.MustCompile(`(?s)limits:.{0,160}memory:`)

	for _, rel := range manifests {
		body := readFile(t, filepath.Join(root, filepath.FromSlash(rel)))

		rm := replicasRe.FindStringSubmatch(body)
		if rm == nil {
			t.Errorf("%s: no replicas: line found — this gate is measuring nothing", rel)
			continue
		}
		replicas, err := strconv.Atoi(rm[1])
		if err != nil {
			t.Errorf("%s: replicas: %q does not parse: %v", rel, rm[1], err)
			continue
		}

		declaresLimits := memLimitRe.MatchString(body)
		open := deployEnvValue(body, "SQL_MAX_OPEN_CONNS")
		if declaresLimits && open == "" {
			t.Errorf("%s: declares a container memory limit but does not set SQL_MAX_OPEN_CONNS, so each replica may open up to the code default (1000) against a PostgreSQL instance shared with the other deployment, platform and the IdP", rel)
		}
		if declaresLimits && deployEnvValue(body, "GOMEMLIMIT") == "" {
			t.Errorf("%s: sets a container memory limit but no GOMEMLIMIT, so the Go heap grows with no idea of the cgroup ceiling it is OOM-killed against", rel)
		}
		if open == "" {
			continue
		}
		n, cerr := strconv.Atoi(open)
		if cerr != nil {
			t.Errorf("%s: SQL_MAX_OPEN_CONNS=%q does not parse: %v", rel, open, cerr)
			continue
		}
		pools := deployPoolCount(body)
		if worst := replicas * pools * n; worst > maxPoolConnectionBudget {
			t.Errorf("%s: replicas(%d) x pools(%d) x SQL_MAX_OPEN_CONNS(%d) = %d, over the %d per-manifest budget (measured server ceiling: max_connections=100, 38 already in use)",
				rel, replicas, pools, n, worst, maxPoolConnectionBudget)
		}

		// Idle must equal open. ConnMaxIdleTime already reaps connections that
		// go quiet, so a lower idle cap does not save the server anything — it
		// only makes the pool close and redial connections under the steady
		// load these replicas produce, and a redial is exactly what a saturated
		// server refuses.
		idle := deployEnvValue(body, "SQL_MAX_IDLE_CONNS")
		if idle == "" {
			t.Errorf("%s: sets SQL_MAX_OPEN_CONNS but not SQL_MAX_IDLE_CONNS", rel)
			continue
		}
		if idle != open {
			t.Errorf("%s: SQL_MAX_IDLE_CONNS=%s but SQL_MAX_OPEN_CONNS=%s — they must match; ConnMaxIdleTime handles reclamation, a smaller idle cap only buys reconnect churn", rel, idle, open)
		}
	}
}

// startupProbeFieldRe pulls one numeric field out of the startupProbe block.
// The block is short and hand-written in both manifests (httpGet, then the
// four numbers), so a bounded scan from the `startupProbe:` key is enough and
// the module still needs no YAML dependency.
func startupProbeField(t *testing.T, rel, body, field string) (int, bool) {
	t.Helper()
	idx := strings.Index(body, "startupProbe:")
	if idx < 0 {
		t.Errorf("%s: no startupProbe: block", rel)
		return 0, false
	}
	// Stop at the next probe so a livenessProbe/readinessProbe field cannot be
	// mistaken for a startupProbe one.
	block := body[idx:]
	for _, next := range []string{"livenessProbe:", "readinessProbe:"} {
		if e := strings.Index(block, next); e > 0 {
			block = block[:e]
		}
	}
	m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(field) + `:\s*(\d+)\s*$`).FindStringSubmatch(block)
	if m == nil {
		t.Errorf("%s: startupProbe has no %s", rel, field)
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Errorf("%s: startupProbe %s=%q does not parse: %v", rel, field, m[1], err)
		return 0, false
	}
	return n, true
}

// startupProbeBudgetSeconds is how long a boot migration may run before
// kubelet SIGKILLs the pod. It matters because migration 039 runs CREATE
// INDEX CONCURRENTLY, which by definition cannot be inside a transaction: a
// kill part-way leaves an INVALID index on the logs table that a human has to
// find and drop. 600s is the budget doc/runbook/database.md publishes.
const startupProbeBudgetSeconds = 600

// TestStartupProbeBudgetCoversAMigration: initialDelaySeconds +
// failureThreshold*periodSeconds must cover startupProbeBudgetSeconds in BOTH
// manifests. The previous values (5 + 30x5 = 155s) were under it.
func TestStartupProbeBudgetCoversAMigration(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		"deploy/k8s/r6-stage/deployment.yaml",
		"deploy/k8s/r6-uat/deployment.yaml",
	} {
		body := readFile(t, filepath.Join(root, filepath.FromSlash(rel)))
		initial, ok1 := startupProbeField(t, rel, body, "initialDelaySeconds")
		period, ok2 := startupProbeField(t, rel, body, "periodSeconds")
		threshold, ok3 := startupProbeField(t, rel, body, "failureThreshold")
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		if budget := initial + threshold*period; budget < startupProbeBudgetSeconds {
			t.Errorf("%s: startup budget = initialDelaySeconds(%d) + failureThreshold(%d)*periodSeconds(%d) = %ds, under the %ds a boot migration needs — a CREATE INDEX CONCURRENTLY killed part-way leaves an INVALID index (doc/runbook/database.md)",
				rel, initial, threshold, period, budget, startupProbeBudgetSeconds)
		}
	}
}

var terminationGracePeriodSecondsRe = regexp.MustCompile(`(?m)^\s*terminationGracePeriodSeconds:\s*(\d+)\s*$`)
var preStopSleepRe = regexp.MustCompile(`(?s)preStop:.{0,160}?sleep",\s*"(\d+)"`)

// TestGracefulShutdownBudget locks cycle-13 L11's shutdown timing budget:
// terminationGracePeriodSeconds must leave room for the preStop sleep (the
// endpoint-removal window) plus GRACEFUL_SHUTDOWN_TIMEOUT (how long
// http.Server.Shutdown waits) plus a margin for the rest of run()'s cleanup
// — DB close, tracing shutdown, the quota_data flush. Under-budgeting it
// means SIGKILL lands while requests are still being drained, which is the
// exact failure the drain was built to avoid. doc/runbook/graceful-drain.md.
func TestGracefulShutdownBudget(t *testing.T) {
	root := repoRoot(t)
	const marginSeconds = 10
	for _, rel := range []string{
		"deploy/k8s/r6-stage/deployment.yaml",
		"deploy/k8s/r6-uat/deployment.yaml",
	} {
		body := readFile(t, filepath.Join(root, filepath.FromSlash(rel)))

		tm := terminationGracePeriodSecondsRe.FindStringSubmatch(body)
		if tm == nil {
			t.Errorf("%s: no terminationGracePeriodSeconds line", rel)
			continue
		}
		tgps, err := strconv.Atoi(tm[1])
		if err != nil {
			t.Errorf("%s: terminationGracePeriodSeconds=%q: %v", rel, tm[1], err)
			continue
		}

		graceful := deployEnvValue(body, "GRACEFUL_SHUTDOWN_TIMEOUT")
		gracefulDur, err := time.ParseDuration(graceful)
		if graceful == "" || err != nil {
			t.Errorf("%s: GRACEFUL_SHUTDOWN_TIMEOUT=%q does not parse as a duration: %v", rel, graceful, err)
			continue
		}

		pm := preStopSleepRe.FindStringSubmatch(body)
		if pm == nil {
			t.Errorf("%s: no preStop sleep command — without it the pod stops answering before kube-proxy has dropped it from the Service", rel)
			continue
		}
		preStop, err := strconv.Atoi(pm[1])
		if err != nil {
			t.Errorf("%s: preStop sleep %q: %v", rel, pm[1], err)
			continue
		}

		if need := preStop + int(gracefulDur.Seconds()) + marginSeconds; tgps < need {
			t.Errorf("%s: terminationGracePeriodSeconds=%d < preStop(%ds) + GRACEFUL_SHUTDOWN_TIMEOUT(%ds) + %ds margin = %d — SIGKILL would land mid-drain",
				rel, tgps, preStop, int(gracefulDur.Seconds()), marginSeconds, need)
		}
	}
}

// goCIPathDependencies are the non-Go directories a Go test in this module
// reads by literal path. A change under one of them can turn Go CI red
// without touching a single .go file, so each must appear in go-ci.yml's
// `paths:` filters or the workflow simply never runs for that change — the
// PR goes green because nothing looked.
//
// evidence is a file that demonstrably performs the read; the test asserts
// the literal is really in it, so this table cannot decay into a claim about
// nothing.
var goCIPathDependencies = []struct {
	prefix   string // as written in the workflow's paths: list
	evidence string // repo-relative Go file that reads the directory
	literal  string // the substring in that file which does the reading
}{
	{"deploy/**", "deploy/k8s/deploy_consistency_test.go", `"deploy"`},
	{"scripts/**", "deploy/k8s/deploy_consistency_test.go", `"scripts"`},
	{"doc/runbook/**", "deploy/k8s/deploy_consistency_test.go", `"doc/runbook"`},
	{"web/src/**", "internal/adapter/handler/router/frontend_route_contract_test.go", `"web", "src"`},
	{"migrations/**", "migrations/embed.go", "//go:embed *.sql"},
	// Not a directory: TestManifestEnvVarsAreDocumented (this file) reads
	// .env.example, so editing it alone can turn Go CI red.
	{".env.example", "deploy/k8s/deploy_consistency_test.go", `".env.example"`},
}

// TestGoCIPathsCoverWhatGoTestsRead: every directory in the table above must
// be in BOTH of go-ci.yml's paths: filters (push and pull_request). Before
// cycle 13 the filters listed only **/*.go, go.mod, go.sum, .golangci.yml and
// the workflow itself, so a PR that broke deploy_consistency_test.go by
// editing a manifest or a runbook never ran Go CI at all.
func TestGoCIPathsCoverWhatGoTestsRead(t *testing.T) {
	root := repoRoot(t)
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "go-ci.yml"))

	// Both trigger blocks carry their own paths: list, so count occurrences
	// rather than merely finding one.
	const wantLists = 2
	if got := strings.Count(workflow, "    paths:\n"); got != wantLists {
		t.Fatalf("go-ci.yml has %d `paths:` lists, want %d (push + pull_request) — this gate is reading the wrong shape", got, wantLists)
	}

	var missing []string
	for _, dep := range goCIPathDependencies {
		evidence := readFile(t, filepath.Join(root, filepath.FromSlash(dep.evidence)))
		if !strings.Contains(evidence, dep.literal) {
			t.Errorf("%s no longer contains %s — the claim that it reads %s is stale; fix the table or drop the row",
				dep.evidence, dep.literal, dep.prefix)
			continue
		}
		entry := "- '" + dep.prefix + "'"
		if got := strings.Count(workflow, entry); got < wantLists {
			missing = append(missing, dep.prefix+" (found in "+strconv.Itoa(got)+" of "+strconv.Itoa(wantLists)+" paths lists; read by "+dep.evidence+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("go-ci.yml paths: does not cover %s — a change there would skip Go CI entirely, so the PR goes green because nothing ran", m)
	}
}

// manifestEnvDocAllowlist: names a manifest sets that .env.example is not
// expected to carry, because they are not read by this codebase at all —
// GOMEMLIMIT is a Go runtime knob and TZ is a libc one. Everything else a
// manifest sets is application configuration an operator should be able to
// find in .env.example.
var manifestEnvDocAllowlist = map[string]string{
	"GOMEMLIMIT": "Go runtime soft memory limit; read by the runtime, not by this codebase",
	"TZ":         "libc timezone; read by the C library, not by this codebase",
}

var manifestEnvNameRe = regexp.MustCompile(`(?m)^\s+- name: ([A-Z][A-Z0-9_]*)\s*$`)

// TestManifestEnvVarsAreDocumented: every env name either manifest sets must
// appear in .env.example. The manifests are the only place several knobs are
// configured at all (ERROR_LOG_ENABLED, CREDIT_POOL_REQUIRED, the NATS trio,
// …), so an operator reading .env.example to find out what this service can
// be told had no way to learn they existed.
func TestManifestEnvVarsAreDocumented(t *testing.T) {
	root := repoRoot(t)
	envExample := readFile(t, filepath.Join(root, ".env.example"))

	seen := map[string]string{}
	for _, rel := range []string{
		"deploy/k8s/r6-stage/deployment.yaml",
		"deploy/k8s/r6-uat/deployment.yaml",
	} {
		body := readFile(t, filepath.Join(root, filepath.FromSlash(rel)))
		for _, m := range manifestEnvNameRe.FindAllStringSubmatch(body, -1) {
			if _, ok := seen[m[1]]; !ok {
				seen[m[1]] = rel
			}
		}
	}
	if len(seen) < 30 {
		t.Fatalf("found only %d env names across both manifests — the scan is broken, so a green run here would mean nothing", len(seen))
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if _, allowed := manifestEnvDocAllowlist[name]; allowed {
			continue
		}
		// Documented = assigned on a line of its own, commented out or not.
		declared := regexp.MustCompile(`(?m)^#?\s*` + regexp.QuoteMeta(name) + `=`)
		if !declared.MatchString(envExample) {
			t.Errorf("%s is set in %s but never appears in .env.example — add a line documenting it (or, if it is not this codebase's configuration, add it to manifestEnvDocAllowlist with the reason)", name, seen[name])
		}
	}
}
