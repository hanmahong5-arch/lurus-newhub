package metrics

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// --- Reverse honesty gate (cycle-13 L10): a Go comment that self-describes
// a series as alertable must actually be bound by some on: line. -----------
//
// The three checks above all start from the conf file and ask "does this
// alarm point at something real". This one starts from the OTHER end: a
// series this package declares that says it needs alerting must be the
// target of some non-dead conf block. Two inputs, in order of authority:
//   1. an explicit `// ALERTABLE: <wire name>` marker in the doc comment —
//      machine-checkable, carried today by the money/conservation counters
//      (see alertableMarkerFloor), and the reason the repair round added
//      it: prose cannot be enumerated in advance.
//   2. prose, as a fallback: a doc comment that asserts in the
//      imperative/declarative (not a hedged "an alert on X must..." caveat,
//      and not a quoted-and-retracted claim like the ⚠️ NOT ALERTED blocks
//      elsewhere in metrics.go) that the series should page or be alerted
//      on.
// Before this test, three such comments (CreditPoolDebitLostTotal's "alert
// on any increase", migrations.go's "the condition to page on", and
// PanicsRecovered's "should page") sat unbound for at least one prior
// cycle each; a fourth (BillingTaskRefundWalletUnreversedTotal) got past
// the prose-only first cut of this gate, which is what the marker fixes.

// netdataAlertableMarkerRe matches an explicit machine-readable
// "ALERTABLE: <wire name>" marker in a Go doc comment — the primary input
// to the reverse gate as of the cycle-13 L10 repair round (D-L10-4). The
// prose scan below stays (it still catches a comment written without the
// marker), but prose is the fallback, not the contract: the first cut of
// this gate matched three hand-picked phrasings and therefore could not see
// BillingTaskRefundWalletUnreversedTotal, whose comment says every
// increment is "a real, uncompensated wallet overcharge" and had no alarm
// bound to it. A marker cannot be missed by rephrasing.
//
// The wire name is spelled out in the marker rather than inferred from the
// declaration below it, so a marker keeps working on a comment that sits
// over a var() block, over a helper func, or anywhere else the positional
// association would guess wrong.
var netdataAlertableMarkerRe = regexp.MustCompile(`ALERTABLE:\s*([a-zA-Z_][a-zA-Z0-9_]*)`)

// alertableMarkerFloor is the number of ALERTABLE markers this package
// carries today (the money/conservation counters listed in the cycle-13
// L10 repair decision D-L10-4: credit-pool debit loss and lookup error,
// advisory meter loss, zero-amount wallet charge, unreversed task refund,
// settlement failure, permanently failed billing outbox entry, and stranded
// open topups). The floor is what stops the gate from being satisfied by
// DELETING a marker instead of wiring the alarm it demands: dropping one
// below this number fails here even though every remaining marker is bound.
// Retiring a counter legitimately means lowering this number in the same
// change, which is a reviewable line in the diff.
const alertableMarkerFloor = 8

// netdataSelfClaimRe matches the specific imperative/declarative phrasings
// this package's own comments use today for "this needs an alert" — not a
// bare "alert"/"page" (which also appears in ⚠️ NOT ALERTED disclaimers,
// var names like CreditPoolAlertTotal, and hedged caveats like instance.go's
// "an alert on this series must not blanket-qualify with..."). Deliberately
// narrow (first cut, like several thresholds in newhub.conf itself) — a
// future comment phrased differently needs a pattern added here, same as
// any other textual heuristic in this file.
var netdataSelfClaimRe = regexp.MustCompile(`(?i)\balert on any\b|\bcondition to page on\b|\bshould page\b`)

// netdataBacktickWireNameRe matches a backtick-introduced `lurus_..._...`
// literal in a comment — the highest-confidence association: the author
// named the exact wire name themselves (migrations.go's block comment does
// this, since its claim sits above a two-gauge var() block rather than
// immediately over the one gauge it is actually about). Deliberately does
// NOT require an immediate closing backtick: migrations.go's comment writes
// the wire name as part of a larger backtick-quoted EXPRESSION
// ("`lurus_gateway_schema_migrations_pending > 0`"), so anchoring on the
// closing backtick would miss it.
var netdataBacktickWireNameRe = regexp.MustCompile("`(lurus_[a-zA-Z0-9_]+)")

// netdataCommentRun is one maximal contiguous run of "//"-prefixed comment
// lines in a Go source file, with its text joined into one string (so a
// phrase wrapped across lines, e.g. panic.go's "...should\npage,...", reads
// as adjacent text — the same reason productionMetricInfo's Name-field scan
// does not simply grep line by line).
type netdataCommentRun struct {
	file      string
	startLine int // 0-indexed, inclusive
	endLine   int // 0-indexed, inclusive
	joined    string
	// afterOffset is the byte offset, into the SAME full file text
	// commentRunsIn was called with, where the first non-comment line after
	// this run begins ("" text / -1 if the run reaches EOF). Kept as an
	// offset into the full text — not a standalone extracted line — because
	// resolving a declaration's wire name needs to scan hundreds of bytes
	// AHEAD of that line for its Namespace:/Subsystem:/Name: fields, which
	// live on the lines that follow it, not on the line itself.
	afterOffset int
	afterLine   string // just the first non-comment line's own text, for the declaredMetricVarRe pre-check
}

// commentRunsIn splits body into its maximal contiguous "//" comment runs.
func commentRunsIn(file, body string) []netdataCommentRun {
	lines := strings.Split(body, "\n")
	// lineOffset[i] is the byte offset of lines[i] within body.
	lineOffset := make([]int, len(lines)+1)
	off := 0
	for i, l := range lines {
		lineOffset[i] = off
		off += len(l) + 1 // +1 for the '\n' strings.Split consumed
	}
	lineOffset[len(lines)] = off

	var runs []netdataCommentRun
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "//") {
			i++
			continue
		}
		start := i
		var parts []string
		for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "//") {
			parts = append(parts, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "//")))
			i++
		}
		after, afterOffset := "", -1
		if i < len(lines) {
			after = lines[i]
			afterOffset = lineOffset[i]
		}
		runs = append(runs, netdataCommentRun{
			file: file, startLine: start, endLine: i - 1,
			joined: strings.Join(parts, " "), afterLine: after, afterOffset: afterOffset,
		})
	}
	return runs
}

// wireNameForDeclaredVarLine resolves the Prometheus wire name for a
// "VarName = promauto.NewXxx(" match spanning text[matchStart:matchEnd],
// using the identical Namespace/Subsystem/Name window-scan
// productionMetricInfo uses for every declared var — factored out here so
// this test does not need productionMetricInfo's full byWireName map (keyed
// the wrong direction for this lookup) or a second copy of the window logic.
// text must be the FULL file text (not just the matched line): the
// Namespace:/Subsystem:/Name: fields this looks for live on the lines AFTER
// the match, which only exist in the full text.
func wireNameForDeclaredVarLine(t *testing.T, text string, matchStart, matchEnd int) string {
	t.Helper()
	windowEnd := len(text)
	if next := strings.Index(text[matchEnd:], "promauto.New"); next >= 0 {
		windowEnd = matchEnd + next
	}
	if windowEnd-matchEnd > 1200 {
		windowEnd = matchEnd + 1200
	}
	window := text[matchEnd:windowEnd]

	nm := netdataNameFieldRe.FindStringSubmatch(window)
	if nm == nil {
		return ""
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
	return strings.Join(parts, "_")
}

// selfClaimedWireNames scans this package's own non-test .go files for
// every comment run matching netdataSelfClaimRe and resolves each to the
// wire name(s) it is claiming alertability for, per the two-step
// association in this function's inline comments. Returns a map of wire
// name -> the file:line the claim lives at (for the error message), and a
// slice of runs it could NOT associate with any series at all (a scan bug,
// not a missing alarm — reported separately so it fails loudly instead of
// silently proving nothing).
func selfClaimedWireNames(t *testing.T, pkgDir string) (claims, marked map[string]string, unassociated []netdataCommentRun) {
	t.Helper()
	claims = map[string]string{}
	marked = map[string]string{}
	for _, f := range packageNonTestGoFiles(t, pkgDir) {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		text := string(body)
		base := filepath.Base(f)
		for _, run := range commentRunsIn(base, text) {
			loc := base + ":" + strconv.Itoa(run.startLine+1)

			// Step 0: an explicit ALERTABLE: marker. Unambiguous (the wire
			// name is written out) and unmissable by rephrasing, so it is
			// checked before the prose heuristics below and counted
			// separately for the floor.
			if mm := netdataAlertableMarkerRe.FindAllStringSubmatch(run.joined, -1); len(mm) > 0 {
				for _, m := range mm {
					claims[m[1]] = loc
					marked[m[1]] = loc
				}
				continue
			}

			if !netdataSelfClaimRe.MatchString(run.joined) {
				continue
			}

			// Step 1: a literal `lurus_..._...` named directly in the
			// comment (highest confidence — e.g. migrations.go's block
			// comment, which sits over a two-gauge var() and would
			// otherwise resolve to the wrong/both declarations).
			if bm := netdataBacktickWireNameRe.FindAllStringSubmatch(run.joined, -1); len(bm) > 0 {
				for _, m := range bm {
					claims[m[1]] = loc
				}
				continue
			}

			// Step 2: the comment sits immediately above one declared
			// var's own "VarName = promauto.New...(" line. Pre-check
			// against the isolated line first (cheap, and keeps the
			// "is this even a declaration line" question separate from
			// "where in the full text do I resolve its fields from");
			// the actual resolution re-matches against the FULL file text
			// at the run's real offset, because wireNameForDeclaredVarLine
			// needs the lines AFTER the declaration, which the isolated
			// afterLine string does not contain.
			if run.afterOffset >= 0 && declaredMetricVarRe.MatchString(run.afterLine) {
				if m := declaredMetricVarRe.FindStringSubmatchIndex(text[run.afterOffset:]); m != nil {
					absStart, absEnd := run.afterOffset+m[0], run.afterOffset+m[1]
					wire := wireNameForDeclaredVarLine(t, text, absStart, absEnd)
					if wire != "" {
						claims[wire] = loc
						continue
					}
				}
			}

			unassociated = append(unassociated, run)
		}
	}
	return claims, marked, unassociated
}

// TestNetdataSelfClaimedAlertableSeriesAreBound is the reverse gate: every
// series that says it needs alerting — through an `// ALERTABLE: <wire
// name>` marker, or through the prose fallback ("alert on any X" / "is the
// condition to page on" / "should page") — must be the on: target of some
// non-DEAD conf block, and the number of markers must not fall below
// alertableMarkerFloor. See this section's header comment for the four
// (now covered) real gaps this found.
func TestNetdataSelfClaimedAlertableSeriesAreBound(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	claims, marked, unassociated := selfClaimedWireNames(t, pkgDir)
	if len(claims) == 0 {
		t.Fatal("found zero self-claimed-alertable comments — the scan is measuring nothing " +
			"(expected at least CreditPoolDebitLostTotal, SchemaMigrationsPending, PanicsRecovered)")
	}
	if len(marked) < alertableMarkerFloor {
		t.Errorf("found %d \"ALERTABLE: <wire name>\" markers in this package's doc comments, "+
			"want at least %d (%v). A money counter whose alarm is inconvenient must not be made "+
			"to pass this gate by deleting its marker — see alertableMarkerFloor.",
			len(marked), alertableMarkerFloor, marked)
	}
	for _, u := range unassociated {
		t.Errorf("%s:%d: comment %q matches the self-claim pattern but could not be associated "+
			"with any declared series (no backtick-quoted wire name, and the next line is not a "+
			"promauto declaration) — fix the scan or the comment.", u.file, u.startLine+1, u.joined)
	}

	blocks := parseNetdataAlarmBlocks(t, root)
	bound := map[string]bool{}
	for _, b := range blocks {
		if !b.dead && b.onMetric != "" {
			bound[b.onMetric] = true
		}
	}

	for wire, loc := range claims {
		if !bound[wire] {
			how := "its comment's prose (\"alert on any\"/\"condition to page on\"/\"should page\")"
			if _, isMarked := marked[wire]; isMarked {
				how = "an explicit \"ALERTABLE:\" marker"
			}
			t.Errorf("%s: %s claims series %q needs alerting, but no non-DEAD template:/alarm: "+
				"block in %s targets it via \"on:\" — either wire an alarm, or the claim is stale "+
				"and should be retracted.", loc, how, wire, healthDDir)
		}
	}
}

// --- README count gate (cycle-13 L10) ---------------------------------

// netdataReadmeCountRe matches "currently defines N alarms" in
// deploy/r6-host-netdata/README.md's headline count sentence.
var netdataReadmeCountRe = regexp.MustCompile(`currently defines (\d+) alarms`)

// TestNetdataReadmeAlarmCountMatchesConf pins README.md's headline "health.d/
// newhub.conf currently defines N alarms" sentence to the ACTUAL non-DEAD
// template:/alarm: block count in the conf file, and requires every one of
// those block names to appear somewhere in the README text — the prose
// breakdown a reader relies on to know what the N alarms actually are.
// Before this test the README's own bullet-by-bullet breakdown had to be
// hand-maintained in lockstep with the conf file with nothing checking that
// it still summed to the number in the headline sentence.
func TestNetdataReadmeAlarmCountMatchesConf(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	blocks := parseNetdataAlarmBlocks(t, root)
	if len(blocks) == 0 {
		t.Fatal("found zero template:/alarm: blocks under health.d/ — the scan is measuring nothing")
	}
	wantCount := 0
	var names []string
	for _, b := range blocks {
		if b.dead {
			continue
		}
		wantCount++
		names = append(names, b.name)
	}
	if wantCount == 0 {
		t.Fatal("every block under health.d/ is marked DEAD — the scan is measuring nothing")
	}

	readmePath := filepath.Join(root, filepath.FromSlash("deploy/r6-host-netdata/README.md"))
	readmeBody, readErr := os.ReadFile(readmePath)
	if readErr != nil {
		t.Fatalf("read %s: %v", readmePath, readErr)
	}
	readme := string(readmeBody)

	m := netdataReadmeCountRe.FindStringSubmatch(readme)
	if m == nil {
		t.Fatalf("%s: found no \"currently defines N alarms\" sentence — the README's headline "+
			"count sentence was renamed or removed; this test has nothing to check against", readmePath)
	}
	gotCount, convErr := strconv.Atoi(m[1])
	if convErr != nil {
		t.Fatalf("%s: \"currently defines %s alarms\" — %q is not an integer", readmePath, m[1], m[1])
	}
	if gotCount != wantCount {
		t.Errorf("%s says \"currently defines %d alarms\", but health.d/*.conf has %d non-DEAD "+
			"template:/alarm: blocks — update the README sentence (and its bullet breakdown) to "+
			"match.", readmePath, gotCount, wantCount)
	}

	for _, name := range names {
		if !strings.Contains(readme, name) {
			t.Errorf("%s: alarm block %q is not named anywhere in the README — a reader counting "+
				"\"%d alarms\" has no way to find out what this one is or why it exists.",
				readmePath, name, wantCount)
		}
	}
}
