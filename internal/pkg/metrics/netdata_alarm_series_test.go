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
// TestDeclaredSeriesHaveAProductionWriter (declared_series_written_test.go,
// declaredMetricVarRe shared directly) rather than trusting netdata itself to
// notice: an alarm on a Prometheus series nobody emits — a typo, or a metric
// renamed out from under the alarm that referenced it — is silent forever.
// Netdata will happily install and evaluate an alarm whose `on:` chart never
// has data; it just never fires, which looks identical to "the condition
// hasn't happened yet" from every vantage point except this test.
//
// netdata itself only ever reads the `on:` line (the chart a template/alarm
// attaches to) — never a comment. Every check in this file is therefore
// anchored on the parsed `on:` line, not on the human-authored "# series:"
// annotation above it; the annotation is checked as a required CROSS-CHECK
// (it must equal what `on:` resolves to), not as the thing netdata evaluates.
//
// A block preceded by a "# DEAD (<reason>, <date>)" comment (instead of
// "# series: <name>") is skipped by every check here — it is expected to be
// non-live, and README.md must list it. Nothing under health.d/ today uses
// this; it exists because a future alarm whose structural checks below
// cannot pass (e.g. a chart-label filter on a key the vector does not
// declare) has to go somewhere other than silently staying broken.

// healthDDir is the repo-relative directory this test scans.
const healthDDir = "deploy/r6-host-netdata/health.d"

// netdataBlockHeaderRe matches a "template: <name>" or "alarm: <name>" line
// — the start of one alarm definition. Netdata's own grammar: "template:"
// defines a rule instantiated once per matching chart, "alarm:" defines a
// rule bound to exactly one named chart; both are checked identically here.
var netdataBlockHeaderRe = regexp.MustCompile(`(?m)^(template|alarm):\s*(\S+)\s*$`)

// netdataOnLineRe matches the "on:" line inside a block — the chart context
// the daemon actually attaches the rule to. Format on the live host:
// "on: prometheus.newhub.<metric>" (context = prometheus.<job>.<metric>, one
// chart per label-set, single dimension named after the metric — verified
// against the R6 host 2026-09-15).
var netdataOnLineRe = regexp.MustCompile(`(?m)^\s*on:\s*(\S+)\s*$`)

// netdataChartLabelsLineRe matches an optional "chart labels: k=v[,k2=v2]"
// line that narrows a template to charts carrying specific label values
// (e.g. "chart labels: status=429"). Netdata cannot be asked from a repo
// checkout whether any chart currently HAS that label VALUE — that is live,
// data-dependent state. What a static check can prove is narrower but still
// useful: that the label KEY named in the filter is one the underlying
// metric's label vector actually declares, so a renamed or typo'd label key
// is caught even though a missing label VALUE cannot be.
var netdataChartLabelsLineRe = regexp.MustCompile(`(?m)^\s*chart labels:\s*(\S+)\s*$`)

// netdataSeriesAnnotationRe matches a "# series: <name>" comment line — the
// human-authored cross-check for the "on:" line, required immediately above
// every non-dead block.
var netdataSeriesAnnotationRe = regexp.MustCompile(`^\s*#\s*series:\s*(\S+)\s*$`)

// netdataDeadMarkerRe matches a "# DEAD (...)" comment line — the opt-out for
// a block this oracle cannot prove live (see file header).
var netdataDeadMarkerRe = regexp.MustCompile(`^\s*#\s*DEAD\b`)

// netdataRunbookCommentRe matches a "# runbook: <path>" comment line, the
// preferred (machine-parseable) way a block names its runbook page.
var netdataRunbookCommentRe = regexp.MustCompile(`^\s*#\s*runbook:\s*(\S+)\s*$`)

// netdataInfoRunbookRe matches a "Runbook: doc/runbook/xxx.md" fragment
// inside an `info:` line — the fallback a block's info text can use instead
// of (or in addition to) the "# runbook:" comment.
var netdataInfoRunbookRe = regexp.MustCompile(`Runbook:\s*(doc/runbook/\S+\.md)`)

// netdataAlarmBlock is one parsed template:/alarm: definition.
type netdataAlarmBlock struct {
	file           string
	name           string
	dead           bool
	annotation     string // "" if missing
	hasAnnotation  bool
	onMetric       string // "" if no "on:" line found
	chartLabelKeys []string
	runbook        string // repo-relative path, "" if none named
}

// parseNetdataAlarmBlocks scans every *.conf file under health.d/ and
// returns one netdataAlarmBlock per template:/alarm: definition.
func parseNetdataAlarmBlocks(t *testing.T, root string) []netdataAlarmBlock {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(healthDDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	var blocks []netdataAlarmBlock
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		fileBody, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		lines := strings.Split(string(fileBody), "\n")

		// Find every block header line and its extent (up to the next header
		// or EOF).
		var headerIdx []int
		for i, line := range lines {
			if netdataBlockHeaderRe.MatchString(line) {
				headerIdx = append(headerIdx, i)
			}
		}

		for hi, idx := range headerIdx {
			m := netdataBlockHeaderRe.FindStringSubmatch(lines[idx])
			name := m[2]
			end := len(lines)
			if hi+1 < len(headerIdx) {
				end = headerIdx[hi+1]
			}
			blockBody := strings.Join(lines[idx:end], "\n")

			blk := netdataAlarmBlock{file: e.Name(), name: name}

			// Preceding non-header line: either "# series: X" or "# DEAD ...".
			if idx > 0 {
				prev := lines[idx-1]
				if netdataDeadMarkerRe.MatchString(prev) {
					blk.dead = true
				} else if am := netdataSeriesAnnotationRe.FindStringSubmatch(prev); am != nil {
					blk.hasAnnotation = true
					blk.annotation = am[1]
				}
			}

			// Scan the contiguous leading-comment run above the header line
			// (stopping at the first blank line, which is what separates one
			// block's comments from the previous block's tail) for a
			// "# runbook:" pointer.
			for j := idx - 1; j >= 0; j-- {
				if strings.TrimSpace(lines[j]) == "" {
					break
				}
				if rm := netdataRunbookCommentRe.FindStringSubmatch(lines[j]); rm != nil {
					blk.runbook = rm[1]
					break
				}
			}

			if om := netdataOnLineRe.FindStringSubmatch(blockBody); om != nil {
				onVal := om[1]
				// The metric name is the segment after the last '.' in the
				// context (prometheus.newhub.<metric> -> <metric>).
				if dot := strings.LastIndex(onVal, "."); dot >= 0 {
					blk.onMetric = onVal[dot+1:]
				} else {
					blk.onMetric = onVal
				}
			}

			if clm := netdataChartLabelsLineRe.FindStringSubmatch(blockBody); clm != nil {
				for _, pair := range strings.Split(clm[1], ",") {
					if k, _, ok := strings.Cut(pair, "="); ok {
						blk.chartLabelKeys = append(blk.chartLabelKeys, k)
					}
				}
			}

			if blk.runbook == "" {
				if im := netdataInfoRunbookRe.FindStringSubmatch(blockBody); im != nil {
					blk.runbook = im[1]
				}
			}

			blocks = append(blocks, blk)
		}
	}
	return blocks
}

// netdataMetricInfo is what productionMetricInfo derives for one
// promauto-declared series: its wire name (as it would appear on /metrics)
// and, for a *Vec, its declared label keys.
type netdataMetricInfo struct {
	wireName string
	labels   []string // nil for a non-Vec metric
	written  bool
}

// netdataNameFieldRe matches a `Name: "xxx"` field inside a promauto Opts
// literal.
var netdataNameFieldRe = regexp.MustCompile(`Name:\s*"([a-zA-Z0-9_]+)"`)

// netdataNamespaceFieldRe / netdataSubsystemFieldRe match a
// `Namespace:`/`Subsystem:` field whose value is either a quoted literal
// (e.g. "billing") or the bare package-level identifier
// `namespace`/`subsystem` (resolved to the actual `namespace`/`subsystem`
// consts declared in metrics.go, not a hard-coded copy of their current
// values — so this test tracks those consts if they ever change instead of
// silently drifting from them).
var netdataNamespaceFieldRe = regexp.MustCompile(`Namespace:\s*(?:"([a-zA-Z0-9_]+)"|(namespace))`)
var netdataSubsystemFieldRe = regexp.MustCompile(`Subsystem:\s*(?:"([a-zA-Z0-9_]+)"|(subsystem))`)
var netdataLabelSliceRe = regexp.MustCompile(`\[\]string\{([^}]*)\}`)
var netdataQuotedRe = regexp.MustCompile(`"([^"]*)"`)

// productionMetricInfo scans this package's own non-test .go files for every
// promauto-declared series, derives its real wire name from the parsed
// Namespace/Subsystem/Name literals of that specific declaration (NOT a
// blanket namespace_subsystem_ prefix — billing.go declares Subsystem:
// "billing" and ratelimit.go declares Namespace: "newhub" with no subsystem
// at all; a fabricated lurus_gateway_ prefix would silently mis-resolve
// both), and cross-references the same "written by external production
// code" bar TestDeclaredSeriesHaveAProductionWriter already holds this
// package's vars to.
func productionMetricInfo(t *testing.T) map[string]netdataMetricInfo {
	t.Helper()

	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	varToInfo := map[string]netdataMetricInfo{} // varName -> info (wireName+labels only, written filled below)
	for _, f := range packageNonTestGoFiles(t, pkgDir) {
		body, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("read %s: %v", f, readErr)
		}
		text := string(body)
		for _, m := range declaredMetricVarRe.FindAllStringSubmatchIndex(text, -1) {
			varName := text[m[2]:m[3]]
			windowEnd := len(text)
			if next := strings.Index(text[m[1]:], "promauto.New"); next >= 0 {
				windowEnd = m[1] + next
			}
			if windowEnd-m[1] > 1200 {
				windowEnd = m[1] + 1200
			}
			window := text[m[1]:windowEnd]

			nm := netdataNameFieldRe.FindStringSubmatch(window)
			if nm == nil {
				t.Fatalf("%s: found declared var %q but no Name: field within its promauto block — "+
					"the bounded-window scan assumption broke, fix this test", f, varName)
			}

			var parts []string
			if nsm := netdataNamespaceFieldRe.FindStringSubmatch(window); nsm != nil {
				if nsm[1] != "" {
					parts = append(parts, nsm[1])
				} else {
					parts = append(parts, namespace)
				}
			}
			if subm := netdataSubsystemFieldRe.FindStringSubmatch(window); subm != nil {
				if subm[1] != "" {
					parts = append(parts, subm[1])
				} else {
					parts = append(parts, subsystem)
				}
			}
			parts = append(parts, nm[1])
			wireName := strings.Join(parts, "_")

			var labels []string
			if lm := netdataLabelSliceRe.FindStringSubmatch(window); lm != nil {
				for _, qm := range netdataQuotedRe.FindAllStringSubmatch(lm[1], -1) {
					labels = append(labels, qm[1])
				}
			}

			varToInfo[varName] = netdataMetricInfo{wireName: wireName, labels: labels}
		}
	}
	if len(varToInfo) == 0 {
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

	byWireName := map[string]netdataMetricInfo{}
	for varName, info := range varToInfo {
		written := false
		directRef := regexp.MustCompile(`\bmetrics\.` + regexp.QuoteMeta(varName) + `\b`)
		if directRef.MatchString(joinedExternal) {
			written = true
		} else {
			fieldRef := regexp.MustCompile(`\b` + regexp.QuoteMeta(varName) + `\b`)
			for name, fbody := range funcs {
				if !calledExternally[name] {
					continue
				}
				if fieldRef.MatchString(fbody) {
					written = true
					break
				}
			}
		}
		info.written = written
		byWireName[info.wireName] = info
	}
	return byWireName
}

// TestNetdataAlarmsNameOnlyLiveSeries is the repo oracle: the "on:" line of
// every non-dead template:/alarm: block in health.d/*.conf must resolve to a
// Prometheus series this package both declares AND actually writes in
// production — the same bar TestDeclaredSeriesHaveAProductionWriter holds
// the package itself to — and the "# series:" annotation immediately above
// the block must name that same series (a cross-check, not the thing
// evaluated: netdata itself never reads the annotation, only "on:").
func TestNetdataAlarmsNameOnlyLiveSeries(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	blocks := parseNetdataAlarmBlocks(t, root)
	if len(blocks) == 0 {
		t.Fatal("found zero template:/alarm: blocks under health.d/ — the scan is measuring nothing")
	}
	info := productionMetricInfo(t)

	checked := 0
	for _, b := range blocks {
		if b.dead {
			continue
		}
		if b.onMetric == "" {
			t.Errorf("%s: block %q has no \"on:\" line — netdata cannot attach this rule to any chart",
				b.file, b.name)
			continue
		}
		checked++
		mi, ok := info[b.onMetric]
		if !ok || !mi.written {
			t.Errorf("%s: block %q attaches to series %q via its \"on:\" line, which is not a "+
				"Prometheus series this package both declares and actually writes in production. "+
				"Either the metric was renamed/typo'd, or nothing writes it — an alarm on a series "+
				"nobody emits is silent forever. Fix the \"on:\" line, or wire a real writer.",
				b.file, b.name, b.onMetric)
		}
		if b.hasAnnotation && b.annotation != b.onMetric {
			t.Errorf("%s: block %q's \"# series: %s\" annotation does not match its \"on:\" line "+
				"(resolves to %q) — the two must name the same series or they will silently drift.",
				b.file, b.name, b.annotation, b.onMetric)
		}
	}
	if checked == 0 {
		t.Fatal("every block under health.d/ is marked DEAD — the scan is measuring nothing")
	}
}

// TestNetdataAlarmsRequireSeriesAnnotation is the converse of the check
// above: every non-dead block must carry a "# series:" annotation
// immediately above it. Without this, an alarm block with no annotation at
// all was invisible to the oracle — silently unchecked rather than failing.
func TestNetdataAlarmsRequireSeriesAnnotation(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	blocks := parseNetdataAlarmBlocks(t, root)
	if len(blocks) == 0 {
		t.Fatal("found zero template:/alarm: blocks under health.d/ — the scan is measuring nothing")
	}
	for _, b := range blocks {
		if b.dead {
			continue
		}
		if !b.hasAnnotation {
			t.Errorf("%s: block %q has no \"# series: <name>\" comment immediately above it — "+
				"every live block must carry one so this oracle can cross-check it against the "+
				"\"on:\" line, or must be marked \"# DEAD (reason, date)\" instead.", b.file, b.name)
		}
	}
}

// TestNetdataAlarmsChartLabelKeysAreDeclared checks the label KEYS named in
// any "chart labels:" filter against the declared label vector of the
// metric the block attaches to. It cannot prove a label VALUE is ever
// emitted (that is live, data-dependent netdata chart state, unreachable
// from a repo checkout) — only that the KEY is one the vector declares at
// all, catching a renamed or typo'd label key.
func TestNetdataAlarmsChartLabelKeysAreDeclared(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	blocks := parseNetdataAlarmBlocks(t, root)
	if len(blocks) == 0 {
		t.Fatal("found zero template:/alarm: blocks under health.d/ — the scan is measuring nothing")
	}
	info := productionMetricInfo(t)

	for _, b := range blocks {
		if b.dead || len(b.chartLabelKeys) == 0 {
			continue
		}
		mi, ok := info[b.onMetric]
		if !ok {
			continue // already reported by TestNetdataAlarmsNameOnlyLiveSeries
		}
		declared := map[string]bool{}
		for _, l := range mi.labels {
			declared[l] = true
		}
		for _, key := range b.chartLabelKeys {
			if !declared[key] {
				t.Errorf("%s: block %q filters \"chart labels: %s=...\" but %q's declared label "+
					"vector is %v — %q is not one of them. A chart-label filter on a key the "+
					"metric does not carry can never match any chart.",
					b.file, b.name, key, b.onMetric, mi.labels, key)
			}
		}
	}
}

// TestNetdataAlarmsHaveARunbookLinkedFromIndex is the runbook gate the lane
// spec calls "a hard gate": every non-dead template:/alarm: block must name
// a doc/runbook/*.md page (via a "# runbook:" comment or a "Runbook:
// doc/runbook/xxx.md" fragment in its info: line), that page must exist,
// and doc/runbook/INDEX.md must link to it. Before this test, the pairing
// was prose only — nothing stopped a fourth alarm from shipping with no
// runbook at all.
func TestNetdataAlarmsHaveARunbookLinkedFromIndex(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	blocks := parseNetdataAlarmBlocks(t, root)
	if len(blocks) == 0 {
		t.Fatal("found zero template:/alarm: blocks under health.d/ — the scan is measuring nothing")
	}

	indexPath := filepath.Join(root, filepath.FromSlash("doc/runbook/INDEX.md"))
	indexBody, readErr := os.ReadFile(indexPath)
	if readErr != nil {
		t.Fatalf("read %s: %v", indexPath, readErr)
	}
	indexText := string(indexBody)

	checked := 0
	for _, b := range blocks {
		if b.dead {
			continue
		}
		checked++
		if b.runbook == "" {
			t.Errorf("%s: block %q names no runbook — every live alarm needs a \"# runbook: "+
				"doc/runbook/xxx.md\" comment (or a \"Runbook: doc/runbook/xxx.md\" fragment in "+
				"its info: line), or must be marked \"# DEAD (reason, date)\" instead.",
				b.file, b.name)
			continue
		}
		rbPath := filepath.Join(root, filepath.FromSlash(b.runbook))
		if _, statErr := os.Stat(rbPath); statErr != nil {
			t.Errorf("%s: block %q names runbook %q, which does not exist: %v",
				b.file, b.name, b.runbook, statErr)
			continue
		}
		base := filepath.Base(b.runbook)
		linkRe := regexp.MustCompile(`\]\(` + regexp.QuoteMeta(base) + `\)`)
		if !linkRe.MatchString(indexText) {
			t.Errorf("%s: block %q's runbook %q exists but is not linked from doc/runbook/INDEX.md "+
				"— an on-call reading the index would never find it.", b.file, b.name, b.runbook)
		}
	}
	if checked == 0 {
		t.Fatal("every block under health.d/ is marked DEAD — the scan is measuring nothing")
	}
}
