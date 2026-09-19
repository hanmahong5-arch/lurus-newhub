package k8s_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// absentKindClaims lists the Kubernetes objects that customer-facing and
// operator documents have described as present while no manifest under
// deploy/ declared them. Found on 2026-09-19: doc/enterprise-ai-gateway-
// positioning.md §5 promised "HPA min=max=3 + PDB minAvailable:1 ... topology
// spread" against a deploy/k8s tree that carries Deployment, Service,
// NetworkPolicy, Namespace, Secret, Kustomization, PrometheusRule and ArgoCD
// Application only (deploy/k8s/r6-stage/README.md records the absence of a
// PDB and an HPA as a decision on the single-node cluster).
//
// A doc line may still use a term when it also says the thing is absent —
// one of the negation markers below must appear on the same line. Anything
// else is treated as a claim of presence and fails while the kind is absent.
var absentKindClaims = []struct {
	kind  string // value of `kind:` (or the pod-spec field) that would make the claim true
	terms []*regexp.Regexp
}{
	{kind: "HorizontalPodAutoscaler", terms: []*regexp.Regexp{regexp.MustCompile(`\bHPA\b`), regexp.MustCompile(`HorizontalPodAutoscaler`)}},
	{kind: "PodDisruptionBudget", terms: []*regexp.Regexp{regexp.MustCompile(`\bPDB\b`), regexp.MustCompile(`PodDisruptionBudget`)}},
	{kind: "topologySpreadConstraints", terms: []*regexp.Regexp{regexp.MustCompile(`topologySpread`), regexp.MustCompile(`topology spread`), regexp.MustCompile(`拓扑分布`)}},
}

// negationMarkers: a line carrying one of these is describing the absence of
// the object, not its presence. Chinese single characters are deliberately
// broad (不/无/未 also occur inside ordinary words); the gate is for claims,
// and a false negative here costs less than a gate nobody can write prose under.
var negationMarkers = []string{
	"不", "无", "未", "没有",
	"not ", "no ", "none", "nothing", "gap", "deliberately", "absent", "removed", "retired", "does not", "do not", "n/a",
}

// docsClaimSkip: directories whose files are dated records rather than
// current descriptions. doc/decisions holds ADRs (the 2026-05 HA ADR really
// did decide on a PDB; the runbook that superseded it says it is gone),
// doc/reports holds point-in-time progress reports.
func docsClaimSkip(rel string) bool {
	rel = filepath.ToSlash(rel)
	return strings.HasPrefix(rel, "doc/decisions/") ||
		strings.HasPrefix(rel, "doc/reports/") ||
		strings.Contains(strings.ToLower(filepath.Base(rel)), "archive")
}

func declaredKinds(t *testing.T, root string) map[string]bool {
	t.Helper()
	kinds := map[string]bool{}
	kindRe := regexp.MustCompile(`(?m)^\s*kind:\s*([A-Za-z]+)`)
	err := filepath.Walk(filepath.Join(root, "deploy"), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || (!strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml")) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range kindRe.FindAllStringSubmatch(string(data), -1) {
			kinds[m[1]] = true
		}
		if strings.Contains(string(data), "topologySpreadConstraints:") {
			kinds["topologySpreadConstraints"] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk deploy/: %v", err)
	}
	if !kinds["Deployment"] {
		t.Fatalf("deploy/ walk found no Deployment kind — the manifest scan is broken, not the docs")
	}
	return kinds
}

func hasNegation(line string) bool {
	lower := strings.ToLower(line)
	for _, m := range negationMarkers {
		if strings.Contains(lower, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// TestDocsDoNotClaimAbsentK8sKinds walks doc/**/*.md and fails on every line
// that names an HPA, a PDB or topology spread as present while deploy/ has no
// such object. Mutation that proves it: put the 2026-09-19 positioning line
// "HPA min=max=3 + PDB minAvailable:1" back into any doc under doc/.
func TestDocsDoNotClaimAbsentK8sKinds(t *testing.T) {
	root := repoRoot(t)
	declared := declaredKinds(t, root)

	scanned := 0
	err := filepath.Walk(filepath.Join(root, "doc"), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if docsClaimSkip(rel) {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		scanned++
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
		n := 0
		for sc.Scan() {
			n++
			line := sc.Text()
			for _, claim := range absentKindClaims {
				if declared[claim.kind] {
					continue
				}
				for _, re := range claim.terms {
					if re.MatchString(line) && !hasNegation(line) {
						t.Errorf("%s:%d describes %s as present but deploy/ declares none: %q", filepath.ToSlash(rel), n, claim.kind, strings.TrimSpace(line))
						break
					}
				}
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatalf("walk doc/: %v", err)
	}
	if scanned < 20 {
		t.Fatalf("scanned only %d markdown files under doc/ — the walk is broken, not the docs", scanned)
	}
}

// retiredRestoreCommands are the operator commands of the docker-compose +
// wal-g topology retired in 2026-04. They were still the first thing
// doc/runbook/pg-restore.md told an on-call operator to run until 2026-09-19.
var retiredRestoreCommands = []string{
	"docker compose stop lurus-api",
	"docker exec lurus-postgres",
	"wal-g backup-fetch",
	"lurus-hub_pg_data",
}

// TestRunbooksDoNotTeachTheRetiredRestorePath fails when any runbook tells the
// operator to run a command from the retired topology. Mutation that proves
// it: paste "wal-g backup-fetch" into any file under doc/runbook.
func TestRunbooksDoNotTeachTheRetiredRestorePath(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "doc", "runbook")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		scanned++
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, cmd := range retiredRestoreCommands {
				if strings.Contains(line, cmd) {
					t.Errorf("doc/runbook/%s:%d teaches the retired restore path: %q", e.Name(), i+1, strings.TrimSpace(line))
				}
			}
		}
	}
	if scanned < 10 {
		t.Fatalf("scanned only %d runbooks — the walk is broken, not the docs", scanned)
	}
}
