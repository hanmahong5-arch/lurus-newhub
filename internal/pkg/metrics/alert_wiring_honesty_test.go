package metrics

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file is the M2 gate: Go source must not describe an alert as if it were
// deployed when it is not.
//
// The repository carries two alerting rule files —
// deploy/k8s/r6-stage/newhub-prometheus-rule.yaml (20 rules) and
// deploy/grafana/newhub-alerts.yaml (17) — and neither is deployed anywhere.
// The first is not in the r6-stage kustomization's `resources:` list, and
// adding it would not help: it is a `PrometheusRule` custom resource, and R6
// runs no Prometheus Operator, so the apply would be rejected for an unknown
// kind. The second is a bare rule file for a Prometheus that does not exist
// either — R6's monitoring is host netdata.
//
// The only place in the whole repository that mentions either file is a comment
// in metrics.go, which reads as an operational guarantee: "Alert:
// NewhubPoolExhaustedRejections fires when rate > 5/min sustained for 5 minutes
// (see deploy/k8s/r6-stage/newhub-prometheus-rule.yaml)". Nothing fires. A
// reader of that comment concludes an exhausted credit pool pages someone.
//
// Two of the rules could not fire even if the stack existed, which is worth
// recording because it shows the files were never run against real data:
//   - HubAvailabilityBelowSLO computes relay availability from
//     lurus_gateway_requests_total, which counts ALL HTTP traffic including the
//     console. Console traffic dominates and dilutes the relay error ratio.
//   - HubOverheadHigh subtracts p95(relay_duration_seconds) from
//     p95(request_duration_seconds) — two different populations, the second
//     measured in seconds against a mostly-sub-millisecond first. The
//     difference is normally negative and can never cross a 0.1 threshold.

// alertFileMarker is the phrase an undeployed rule file must carry so that a
// reader knows what they are looking at before they trust it.
const alertFileMarker = "REFERENCE ONLY — NOT DEPLOYED"

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

// findAlertRuleFiles returns every YAML under deploy/ that declares alerting
// rules (an `alert:` key), relative to the repo root.
func findAlertRuleFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(root, "deploy"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !regexp.MustCompile(`(?m)^\s*-?\s*alert:\s`).Match(body) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("scan deploy/ for alert rules: %v", err)
	}
	sort.Strings(out)
	return out
}

// deployedResources returns the set of files listed in any kustomization's
// `resources:` block — i.e. the files that actually reach a cluster.
func deployedResources(t *testing.T, root string) map[string]bool {
	t.Helper()
	deployed := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "deploy"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "kustomization.yaml" {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		dir := filepath.Dir(path)
		inResources := false
		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "resources:" {
				inResources = true
				continue
			}
			// Any other top-level key ends the block.
			if inResources && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				inResources = false
			}
			if !inResources || !strings.HasPrefix(trimmed, "- ") {
				continue
			}
			entry := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if entry == "" || strings.HasPrefix(entry, "#") {
				continue
			}
			rel, relErr := filepath.Rel(root, filepath.Join(dir, entry))
			if relErr == nil {
				deployed[filepath.ToSlash(rel)] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan kustomizations: %v", err)
	}
	return deployed
}

// TestNoAlertFileClaimsDeploymentItDoesNotHave is the honesty gate. An alerting
// rule file that no kustomization deploys must say so in its own header, and no
// Go source may cite it as an alert that fires.
//
// "Nobody is listening" and "the alarm is broken" look identical from inside
// the process; the difference is only visible in the tree, so the tree is where
// it has to be asserted.
func TestNoAlertFileClaimsDeploymentItDoesNotHave(t *testing.T) {
	root := repoRoot(t)
	alertFiles := findAlertRuleFiles(t, root)
	if len(alertFiles) == 0 {
		t.Fatal("found no alerting rule files under deploy/ — the scan is measuring nothing")
	}
	deployed := deployedResources(t, root)

	// Go sources that name an alert file.
	goRefs := map[string][]string{}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(body)
		rel, _ := filepath.Rel(root, path)
		for _, af := range alertFiles {
			base := filepath.Base(af)
			if strings.Contains(text, af) || strings.Contains(text, base) {
				goRefs[af] = append(goRefs[af], filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan internal/ for alert-file references: %v", err)
	}

	for _, af := range alertFiles {
		isDeployed := deployed[af]
		body, readErr := os.ReadFile(filepath.Join(root, af))
		if readErr != nil {
			t.Fatalf("read %s: %v", af, readErr)
		}
		marked := strings.Contains(string(body), alertFileMarker)

		if isDeployed && marked {
			t.Errorf("%s is deployed by a kustomization but is marked %q — one of the two is wrong",
				af, alertFileMarker)
			continue
		}

		if isDeployed {
			continue
		}

		if !marked {
			t.Errorf("%s declares alerting rules but no kustomization deploys it, and its "+
				"header does not say so.\n\nAdd the marker %q with a line explaining what "+
				"would be required to deploy it. An operator reading a rule file assumes the "+
				"rules run; nothing here does.",
				af, alertFileMarker)
		}

		if refs := goRefs[af]; len(refs) > 0 {
			t.Errorf("%s is not deployed, yet Go source describes it as an active alert: %s\n\n"+
				"Go code asserting a deployment that does not exist is worse than silence — "+
				"the next reader concludes the condition pages someone. Either deploy the "+
				"rules or make the comment state that no alerting backend consumes them.",
				af, strings.Join(refs, ", "))
		}
	}
}

// TestAlertRuleExpressionsAreNotStructurallyImpossible is a narrow check on two
// expressions that could not fire even with a Prometheus behind them. It is
// deliberately specific rather than a general PromQL linter: these two shipped,
// were never evaluated against real data, and are the concrete evidence that a
// rule file nobody runs is a rule file nobody checks.
func TestAlertRuleExpressionsAreNotStructurallyImpossible(t *testing.T) {
	root := repoRoot(t)
	for _, af := range findAlertRuleFiles(t, root) {
		body, err := os.ReadFile(filepath.Join(root, af))
		if err != nil {
			t.Fatalf("read %s: %v", af, err)
		}
		text := string(body)

		// Relay availability must not be computed from the all-HTTP counter:
		// console traffic dominates and dilutes the relay error ratio, so the
		// alert measures something other than what it is named for.
		if strings.Contains(text, "HubAvailabilityBelowSLO") &&
			strings.Contains(text, "lurus_gateway_requests_total") &&
			!strings.Contains(text, alertFileMarker) {
			t.Errorf("%s: HubAvailabilityBelowSLO derives RELAY availability from "+
				"lurus_gateway_requests_total, which counts every HTTP request including "+
				"the console. Use relay_requests_total, or mark the file %q.",
				af, alertFileMarker)
		}

		// Gateway overhead is not p95(all HTTP) − p95(upstream): those are two
		// different populations and the difference is normally negative, so the
		// threshold is unreachable in both directions.
		if strings.Contains(text, "lurus_gateway_request_duration_seconds_bucket") &&
			strings.Contains(text, "lurus_gateway_relay_duration_seconds_bucket") &&
			!strings.Contains(text, alertFileMarker) {
			t.Errorf("%s: gateway overhead is computed by subtracting the p95 of two "+
				"different populations (all HTTP vs upstream calls). The result is normally "+
				"negative and cannot cross a positive threshold. "+
				"lurus_gateway_relay_overhead_duration_seconds already measures this "+
				"directly. Fix the expression, or mark the file %q.",
				af, alertFileMarker)
		}
	}
}
