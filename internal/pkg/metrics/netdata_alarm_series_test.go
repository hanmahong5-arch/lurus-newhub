package metrics

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// netdata_alarm_series_test.go — the M4 gate for repo-owned host alerting
// (deploy/r6-host-netdata/health.d/). It reuses the machinery behind
// TestDeclaredSeriesHaveAProductionWriter (declared_series_written_test.go)
// rather than trusting netdata itself to notice: an alarm on a Prometheus
// series nobody emits — a typo, or a metric renamed out from under the alarm
// that referenced it — is silent forever. Netdata will happily install and
// evaluate an alarm whose `on:` chart never has data; it just never fires,
// which looks identical to "the condition hasn't happened yet" from every
// vantage point except this test.
//
// Each alarm block in health.d/*.conf carries a "# series: <name>" comment
// immediately above its `alarm:` line, naming the raw Prometheus series (as
// it appears on /metrics: namespace_subsystem_name) the alarm's `on:` chart
// is derived from. Netdata itself never reads that comment — it exists
// solely for this test to parse.

// alarmSeriesAnnotationRe matches a "# series: <name>" comment line.
var alarmSeriesAnnotationRe = regexp.MustCompile(`(?m)^\s*#\s*series:\s*(\S+)\s*$`)

// alarmNameLineRe matches the `alarm: <name>` line that must immediately
// follow (allowing blank/comment lines are NOT skipped — the annotation is
// required to sit directly above the alarm it documents, so a stale
// annotation orphaned by an edit is caught rather than silently attributed
// to the wrong alarm).
var netdataAlarmNameRe = regexp.MustCompile(`(?m)^alarm:\s*(\S+)\s*$`)

// healthDDir is the repo-relative directory this test scans.
const healthDDir = "deploy/r6-host-netdata/health.d"

// netdataAlarmSeriesRefs returns, for every alarm in every *.conf file under
// health.d/, the series name its "# series:" annotation names, keyed by
// "<file>:<alarm name>".
func netdataAlarmSeriesRefs(t *testing.T, root string) map[string]string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(healthDDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	refs := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			m := alarmSeriesAnnotationRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if i+1 >= len(lines) {
				t.Errorf("%s:%d: \"# series: %s\" is the last line — no alarm follows it", e.Name(), i+1, m[1])
				continue
			}
			an := netdataAlarmNameRe.FindStringSubmatch(lines[i+1])
			if an == nil {
				t.Errorf("%s:%d: \"# series: %s\" must sit immediately above an `alarm:` line; found %q instead",
					e.Name(), i+1, m[1], lines[i+1])
				continue
			}
			key := e.Name() + ":" + an[1]
			refs[key] = m[1]
		}
	}
	return refs
}

// productionMetricNames returns every full Prometheus series name
// (namespace_subsystem_name) declared in this package's own non-test files,
// mapped to whether a non-test file OUTSIDE this package (or an exported
// helper in this package called from outside it) actually writes it —
// exactly the "written by production code" test declared_series_written_test.go
// already runs, keyed by the exported /metrics name instead of the Go var
// name so an alarm file (which only ever sees the wire name) can be checked
// against it directly.
func productionMetricNames(t *testing.T) map[string]bool {
	t.Helper()

	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	// varName -> declared "Name:" field, scanning a promauto.New*( ... Name:
	// "xxx" ...) block. This is deliberately its OWN regex, not
	// declaredMetricVarRe (declared_series_written_test.go) — that one only
	// matches vars declared inside a `var ( ... )` block (no leading "var "
	// keyword on the line), which misses a standalone `var X = promauto.New...(`
	// statement. r6_rate_limit_degraded.go uses exactly that standalone form,
	// so reusing the block-only regex would silently drop
	// RateLimitDegradedTotal from this scan (found the hard way: it matched
	// zero series here until this regex grew the optional "var " prefix).
	varDeclRe := regexp.MustCompile(`(?m)^\s*(?:var\s+)?(\w+)\s*=\s*promauto\.New\w+\(`)
	varToWireName := map[string]string{}
	nameFieldRe := regexp.MustCompile(`Name:\s*"([a-zA-Z0-9_]+)"`)
	for _, f := range packageNonTestGoFiles(t, pkgDir) {
		body, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("read %s: %v", f, readErr)
		}
		text := string(body)
		for _, m := range varDeclRe.FindAllStringSubmatchIndex(text, -1) {
			varName := text[m[2]:m[3]]
			// Search a bounded window after the declaration for its Name field
			// (the promauto.New*(prometheus.XOpts{...}) block is always a few
			// lines, never spans to the next declared var in practice — bound
			// the window to the next "promauto.New" occurrence, or 800 bytes,
			// whichever is shorter, so a malformed file fails loudly instead of
			// silently matching the wrong var's Name).
			windowEnd := len(text)
			if next := strings.Index(text[m[1]:], "promauto.New"); next >= 0 {
				windowEnd = m[1] + next
			}
			if windowEnd-m[1] > 800 {
				windowEnd = m[1] + 800
			}
			window := text[m[1]:windowEnd]
			nm := nameFieldRe.FindStringSubmatch(window)
			if nm == nil {
				t.Fatalf("%s: found declared var %q but no Name: field within its promauto block — "+
					"the bounded-window scan assumption broke, fix this test", f, varName)
			}
			varToWireName[varName] = namespace + "_" + subsystem + "_" + nm[1]
		}
	}
	if len(varToWireName) == 0 {
		t.Fatal("found zero promauto-declared vars with a Name field — the scan is measuring nothing")
	}

	funcs := exportedFuncBodies(t)
	externalFiles := repoGoFilesOutsidePackage(t, root, pkgDir)
	externalText := make([]string, 0, len(externalFiles))
	for _, f := range externalFiles {
		body, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("read %s: %v", f, readErr)
		}
		externalText = append(externalText, string(body))
	}
	joinedExternal := strings.Join(externalText, "\n")

	calledExternally := map[string]bool{}
	for name := range funcs {
		if regexp.MustCompile(`\bmetrics\.` + regexp.QuoteMeta(name) + `\(`).MatchString(joinedExternal) {
			calledExternally[name] = true
		}
	}

	written := map[string]bool{}
	for varName, wireName := range varToWireName {
		directRef := regexp.MustCompile(`\bmetrics\.` + regexp.QuoteMeta(varName) + `\b`)
		if directRef.MatchString(joinedExternal) {
			written[wireName] = true
			continue
		}
		fieldRef := regexp.MustCompile(`\b` + regexp.QuoteMeta(varName) + `\b`)
		for name, body := range funcs {
			if !calledExternally[name] {
				continue
			}
			if fieldRef.MatchString(body) {
				written[wireName] = true
				break
			}
		}
	}
	return written
}

// TestNetdataAlarmsNameOnlyLiveSeries is the repo oracle: every series a
// health.d/*.conf alarm names via "# series:" must be a Prometheus series
// this package both declares AND actually writes in production — the same
// bar TestDeclaredSeriesHaveAProductionWriter holds the package itself to.
func TestNetdataAlarmsNameOnlyLiveSeries(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	refs := netdataAlarmSeriesRefs(t, root)
	if len(refs) == 0 {
		t.Fatal("found zero \"# series:\" annotations under health.d/ — the scan is measuring nothing")
	}
	written := productionMetricNames(t)

	for key, series := range refs {
		if !written[series] {
			t.Errorf("%s names series %q, which is not a Prometheus series this package both "+
				"declares and actually writes in production. Either the metric was renamed/typo'd "+
				"in the alarm file, or nothing writes it — an alarm on a series nobody emits is "+
				"silent forever. Fix the \"# series:\" annotation, or wire a real writer.",
				key, series)
		}
	}
}
