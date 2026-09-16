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
// The repository carries one alerting rule file —
// deploy/k8s/r6-stage/newhub-prometheus-rule.yaml (20 rules) — and it is not
// deployed anywhere. It is not in the r6-stage kustomization's `resources:`
// list, and adding it would not help: it is a `PrometheusRule` custom
// resource, and R6 runs no Prometheus Operator, so the apply would be
// rejected for an unknown kind. R6's monitoring is host netdata. (A second
// rule file, deploy/grafana/newhub-alerts.yaml, existed for an undeployed
// Grafana stack and was deleted 2026-09-07 along with the rest of
// deploy/grafana/ — same story, one less file to keep honest.)
//
// A comment in metrics.go used to read as an operational guarantee: "Alert:
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

// alertNamesIn extracts every `alert: Name` rule name declared in a rule
// file's YAML body.
var alertNameLineRe = regexp.MustCompile(`(?m)^\s*-?\s*alert:\s*(\w+)`)

func alertNamesIn(body []byte) []string {
	var names []string
	for _, m := range alertNameLineRe.FindAllSubmatch(body, -1) {
		names = append(names, string(m[1]))
	}
	return names
}

// quotedSpanRe strips `"..."` spans (dotall) before the fires/pages sentence
// scan below, so a comment that QUOTES a historical false claim — documenting
// what NOT to say, e.g. PoolExhaustedRejections' own doc comment above — is
// not itself flagged as making that claim. The whole point of quoting the old
// wording is to show it is retracted; only unquoted prose asserts something.
var quotedSpanRe = regexp.MustCompile(`(?s)"[^"]*"`)

// fireOrPageWordRe matches "fires" or "pages" as a whole word.
var fireOrPageWordRe = regexp.MustCompile(`\b(fires|pages)\b`)

// TestNoAlertNamedAsFiringInGoSource extends the honesty gate above: even
// without citing a rule FILE by name, Go source can still assert that a named
// ALERT fires or pages — metrics.go once said "the CreditPoolBalanceLow alert
// fires under 20% ceiling" without ever mentioning
// newhub-prometheus-rule.yaml, so the file-reference check above could not
// catch it. This scans for an alert name and "fires"/"pages" landing in the
// same sentence of unquoted prose.
func TestNoAlertNamedAsFiringInGoSource(t *testing.T) {
	root := repoRoot(t)
	alertFiles := findAlertRuleFiles(t, root)
	if len(alertFiles) == 0 {
		t.Fatal("found no alerting rule files under deploy/ — the scan is measuring nothing")
	}

	var names []string
	for _, af := range alertFiles {
		body, err := os.ReadFile(filepath.Join(root, af))
		if err != nil {
			t.Fatalf("read %s: %v", af, err)
		}
		names = append(names, alertNamesIn(body)...)
	}
	if len(names) == 0 {
		t.Fatal("found zero alert names in the rule files — the scan is measuring nothing")
	}

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		// Flatten to prose: drop each line's leading `//`/whitespace so a
		// comment paragraph spanning several source lines reads as one
		// run of text, then strip quoted spans (see quotedSpanRe) before
		// splitting into sentences.
		var flat strings.Builder
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "//"))
			flat.WriteString(trimmed)
			flat.WriteString(" ")
		}
		text := quotedSpanRe.ReplaceAllString(flat.String(), " ")

		rel, _ := filepath.Rel(root, path)
		for _, sentence := range strings.Split(text, ".") {
			if !fireOrPageWordRe.MatchString(sentence) {
				continue
			}
			for _, name := range names {
				if regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(sentence) {
					t.Errorf("%s: names alert %q and \"fires\"/\"pages\" in the same sentence:\n%q\n\n"+
						"No kustomization deploys any rule file under deploy/ (R6 has no Prometheus "+
						"Operator). Nothing in this rule file pages anyone — say so, or deploy it.",
						rel, name, strings.TrimSpace(sentence))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan internal/ for alert-name claims: %v", err)
	}
}

// netdataHealthDDir is the repo-owned, HOST-INSTALLED counterpart to the
// reference-only PrometheusRule YAML this file otherwise polices.
const netdataHealthDDir = "deploy/r6-host-netdata/health.d"

// netdataInstallScript is the script that makes files under netdataHealthDDir
// live on the R6 host. Every alarm file must name it, and it must actually
// exist — a reference to a script that was renamed or never committed would
// be exactly the kind of claim this file exists to catch.
const netdataInstallScript = "scripts/install-netdata-alarms.sh"

// netdataInstallStatusLineRe matches a "# STATUS: ... YYYY-MM-DD" comment
// line. Unlike deploy/k8s/r6-stage/newhub-prometheus-rule.yaml (which is
// simply never deployed, so a static "REFERENCE ONLY" marker is always
// true), a file under health.d/ moves between "not yet installed" and
// "installed" as the operator actually runs the install script — a static
// marker would go stale the moment either state changes. Requiring a DATED
// status line instead of forbidding a marker string lets the file say
// exactly what is true today without this test rotting into either a false
// "installed" claim or a permanent "reference-only" one.
var netdataInstallStatusLineRe = regexp.MustCompile(`(?m)^#\s*STATUS:.*\d{4}-\d{2}-\d{2}`)

// TestNetdataDirectoryAssertsInstalledOppositeOfReferenceOnlyYAML is the
// mirror image of TestNoAlertFileClaimsDeploymentItDoesNotHave above. That
// test polices files that describe themselves as live but are not; this one
// polices the opposite failure mode for deploy/r6-host-netdata/health.d/ —
// files that ARE what makes newhub's alerting live once installed, so they
// must say what installs them AND carry a dated status line describing
// whether that has actually happened yet, so a reader isn't left to guess
// whether the copy under version control is the one netdata evaluates.
func TestNetdataDirectoryAssertsInstalledOppositeOfReferenceOnlyYAML(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, filepath.FromSlash(netdataHealthDDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	var confFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".conf") {
			confFiles = append(confFiles, e.Name())
		}
	}
	if len(confFiles) == 0 {
		t.Fatal("found zero .conf files under " + netdataHealthDDir + " — the scan is measuring nothing")
	}

	scriptPath := filepath.Join(root, filepath.FromSlash(netdataInstallScript))
	if _, statErr := os.Stat(scriptPath); statErr != nil {
		t.Errorf("%s names an install script that does not exist at %s: %v",
			netdataHealthDDir, netdataInstallScript, statErr)
	}

	for _, name := range confFiles {
		path := filepath.Join(dir, name)
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		text := string(body)

		if !strings.Contains(text, netdataInstallScript) && !strings.Contains(text, filepath.Base(netdataInstallScript)) {
			t.Errorf("%s/%s does not name the script that installs it (%s) — a reader has no way "+
				"to tell this file apart from the reference-only YAML without that pointer.",
				netdataHealthDDir, name, netdataInstallScript)
		}

		if !netdataInstallStatusLineRe.MatchString(text) {
			t.Errorf("%s/%s has no dated \"# STATUS: ... YYYY-MM-DD\" line — this directory's "+
				"install state changes as the operator actually runs %s, so a static "+
				"\"installed\"/\"not deployed\" claim goes stale; a dated status line at least "+
				"tells a reader how fresh the claim is.",
				netdataHealthDDir, name, netdataInstallScript)
		}
	}

	// The install-time honesty limit: this directory can prove an alarm
	// changes state and is visible in netdata's own alarm API. It cannot
	// prove a human is notified — that depends on health_alarm_notify.conf
	// recipients on the host, which this repo does not own. The README must
	// say so in terms specific enough to check for (not just the word
	// "alert").
	readmePath := filepath.Join(root, filepath.FromSlash("deploy/r6-host-netdata/README.md"))
	readmeBody, readErr := os.ReadFile(readmePath)
	if readErr != nil {
		t.Fatalf("read %s: %v", readmePath, readErr)
	}
	readme := string(readmeBody)
	if !strings.Contains(readme, "health_alarm_notify.conf") {
		t.Error("deploy/r6-host-netdata/README.md must name health_alarm_notify.conf as the " +
			"thing that actually controls whether a human is notified — without it, a reader " +
			"can mistake \"installed and evaluated by netdata\" for \"someone gets paged\".")
	}
	if !strings.Contains(readme, "owner") {
		t.Error("deploy/r6-host-netdata/README.md must say plainly that populating alarm " +
			"recipients is an owner action this lane does not perform.")
	}
}
