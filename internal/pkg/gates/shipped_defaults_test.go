package gates

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ---------------------------------------------------------------------------
// Shipped-defaults gate.
//
// WHAT IT IS FOR
//
// A default that a runbook, an .env.example line or a doc comment makes a
// claim about is a contract with the operator. Production carries an
// `options` table with a handful of rows — everything NOT in that table runs
// on the value compiled into the binary, which is the value pinned below.
// That is how `RetryTimes = 0` shipped while
// internal/app/channel_select.go's long doc comment described cross-group,
// cross-priority failover and internal/adapter/handler/relay.go was built
// around it: nothing compared the sentence to the number.
//
// WHAT IT SCANS
//
//   - TestShippedDefaultsMatchTheirDocumentedValues reads the COMPILED
//     package variables of internal/pkg/common in a process that has not
//     called common.InitEnv() and has not loaded the options table.
//   - TestEnvTunableDefaultsMatchTheirSourceLiteral reads the source text of
//     internal/pkg/common/init.go and pins the literal default written in
//     each GetEnvOrDefault* call.
//   - TestRelayRetryBudgetArithmeticStillHolds reads the source text of
//     internal/adapter/handler/relay.go and pins the expressions that turn
//     RetryTimes into an attempt budget — BOTH loops, the synchronous relay
//     and the async task relay.
//   - TestAutoGroupRetryBudgetResetIsStillThere reads the source text of
//     internal/app/channel_select.go and pins the per-auto-group budget reset
//     that makes the bound "RetryTimes+1 per GROUP", not per request.
//   - TestEveryRetryTimesConsumerIsAccountedFor walks every non-test .go file
//     in the tree and requires each file that mentions common.RetryTimes to
//     have a row saying what it does with the budget.
//   - TestEveryPinnedDefaultCitesAFileThatSaysIt opens the file each table-1
//     row names as its claim source and requires that file to actually
//     contain the key.
//
// BLIND SPOTS — what this gate structurally CANNOT see
//
//  1. THE RUNNING DATABASE. Every key below is also an `options` table row
//     key. An operator row wins at runtime over everything pinned here, and
//     this gate reads a source tree, not a database — it can be fully green
//     while production runs a different number for every single row. The one
//     check for that half is the SQL query in
//     doc/runbook/shipped-defaults.md; this gate cannot replace it and does
//     not try to.
//  2. THE ENVIRONMENT. common.InitEnv() is never called in this test binary,
//     so an env var set on a deployment is invisible. Table 2 pins the
//     literal written in the source, not the value any given pod computed.
//  3. DEFAULTS EXPRESSED ANYWHERE ELSE. Table 2's scanner only reads
//     internal/pkg/common/init.go, and only matches a call whose env key is a
//     double-quoted string literal. CHANNEL_TEST_TIMEOUT_SECONDS is the
//     concrete example it misses: its default lives in
//     internal/adapter/handler/channel_probe_policy.go and is read through a
//     named const, so it is out of this gate's reach entirely.
//  4. VALUES IT DOES NOT LIST. A default with no row here can still be
//     changed silently. Adding a row is the only way in.
//  5. WHETHER THE NUMBER IS RIGHT. This is a pin, not a review. A
//     wrong-but-pinned value stays wrong and stays green; the argument for
//     each number lives in the `why` column and in the source comment next to
//     the variable, and the gate only stops a SILENT edit to one of them.
//  6. RUNTIME BEHAVIOUR. Table 3 is a source-text match. It proves the loop
//     headers and the remaining-budget expression still read the way the
//     arithmetic below assumes; it does NOT prove a second upstream call ever
//     happens, and it cannot see that an open circuit breaker consumes an
//     iteration of that same loop (the `continue` in relay.go runs the post
//     statement). A behavioural oracle for failover has to live in
//     internal/adapter/handler, not here.
//  7. WHETHER A CITED CLAIM IS TRUE. The claim check opens the cited file and
//     looks for the key's name. A file can name the key and say something
//     false about it — which is exactly what happened here:
//     doc/runbook/channel-auto-ban.md named ChannelDisableThreshold while
//     asserting a production `options` row for it that the operator's live
//     check found does not exist. Mention is the only thing this gate can
//     verify; truth is a human read.
//  8. WHICH CONSUMER IS THE DANGEROUS ONE. The RetryTimes census counts the
//     literal text `common.RetryTimes` per file. It sees the assignment
//     `retryTimes := common.RetryTimes` in relay.go, but NOT the loop eleven
//     lines below that spends the copy; it cannot see a use inside package
//     common itself (unqualified `RetryTimes`), a dot-import, or a value read
//     back out of common.OptionMap["RetryTimes"] as a string. It answers
//     "who touches this knob", never "what they do with it" — that part is
//     the `why` column, which is prose and can be wrong.
// ---------------------------------------------------------------------------

// shippedDefault is one pinned compiled default.
type shippedDefault struct {
	// name is the Go identifier in internal/pkg/common.
	name string
	// got is the value the compiled package variable holds right now.
	got string
	// want is the pinned value.
	want string
	// claim is the repo-relative path of the file that makes the
	// operator-visible claim about this default — a runbook, .env.example,
	// the console field an operator edits, or the consumer whose doc comment
	// explains the number. A row with no such claim does not belong in this
	// table, so this field may not be empty, and it may not point at
	// shippedDefaultsRunbook (that file was written FROM this table; citing
	// it would let the table certify itself).
	claim string
	// claimText is the literal TestEveryPinnedDefaultCitesAFileThatSaysIt
	// looks for inside claim, for the rows whose claim file spells the knob
	// as an env var rather than as the Go identifier. Empty means "look for
	// name".
	claimText string
	// why names what that claim is and why the number is what it is.
	why string
}

// shippedDefaultsRunbook is this gate's own operator-facing runbook. It is
// refused as a claim source; see shippedDefault.claim.
const shippedDefaultsRunbook = "doc/runbook/shipped-defaults.md"

// defaultValue renders a pinned value in one canonical text form per kind, so
// a row reads the same whether the variable is an int, a bool, a float or a
// duration. Durations are rendered in whole milliseconds because that is the
// unit their env knobs use.
func defaultValue(v any) string {
	switch t := v.(type) {
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case time.Duration:
		return strconv.FormatInt(t.Milliseconds(), 10) + "ms"
	default:
		return fmt.Sprintf("<unsupported kind %T>", v)
	}
}

// shippedDefaults is table 1. Read the blind-spot list above before adding a
// row: a row here is a claim about what the BINARY starts with, never a claim
// about what production is running.
func shippedDefaults() []shippedDefault {
	return []shippedDefault{
		{
			name:  "RetryTimes",
			got:   defaultValue(common.RetryTimes),
			want:  "2",
			claim: "internal/app/channel_select.go",
			why: "Relay failover budget. RelayHandler loops while retry <= RetryTimes, " +
				"so the bound is RetryTimes+1 upstream attempts PER GROUP, not per " +
				"request: for a token whose group is `auto` with cross-group retry, " +
				"internal/app/channel_select.go restarts the counter (param.SetRetry(0) " +
				"+ ResetRetryNextTry) once per auto group, making the worst case " +
				"(RetryTimes+1) x len(autoGroups). 0 (the value that shipped until " +
				"cycle-14 L2) made the cross-group / cross-priority failover that same " +
				"file's doc comment describes unreachable. Three other things read this " +
				"variable — see retryTimesConsumers() for the census and " +
				"internal/pkg/common/constants.go for the full argument, including what " +
				"the budget does to the circuit breaker when a model has ONE candidate " +
				"channel.",
		},
		{
			name:  "ChannelDisableThreshold",
			got:   defaultValue(common.ChannelDisableThreshold),
			want:  "5",
			claim: "doc/runbook/channel-auto-ban.md",
			why: "Seconds a probe may take before the automatic pass counts a latency " +
				"breach. The operator's 2026-09 read of the production `options` table " +
				"found NO ChannelDisableThreshold row, so this compiled 5.0 IS the live " +
				"latency threshold — nothing insulates production from this number. " +
				"(doc/runbook/channel-auto-ban.md said the opposite until cycle-14 L2's " +
				"second pass; cycle-11's open item O7, 'raise the production value to " +
				"60', was written against a row that does not exist.) NOT changed here: " +
				"channel probing belongs to another lane, and this row records the value " +
				"rather than choosing it.",
		},
		{
			name:  "AutomaticDisableChannelEnabled",
			got:   defaultValue(common.AutomaticDisableChannelEnabled),
			want:  "false",
			claim: "doc/runbook/channel-auto-ban.md",
			why: "Master switch for every automatic channel ban. app.ShouldDisableChannel " +
				"(internal/app/channel.go) returns false outright when this is off, and " +
				"handler/channel_probe_policy.go gates both the error-class and the " +
				"latency branch on it. doc/runbook/channel-auto-ban.md's whole decision " +
				"table is conditional on this flag being ON, which the shipped default " +
				"is not.",
		},
		{
			name:  "AutomaticEnableChannelEnabled",
			got:   defaultValue(common.AutomaticEnableChannelEnabled),
			want:  "false",
			claim: "doc/runbook/channel-auto-ban.md",
			why: "Counterpart of the above for re-enabling an auto-disabled channel " +
				"(app.ShouldEnableChannel). Off means an auto-disabled channel stays " +
				"disabled until an operator re-enables it.",
		},
		{
			name:  "PreConsumedQuota",
			got:   defaultValue(common.PreConsumedQuota),
			want:  "500",
			claim: "web/src/pages/Setting/Operation/SettingsCreditLimit.jsx",
			why: "Fallback pre-authorisation amount in quota units. Feeds the pre-auth " +
				"the relay takes ONCE per request (app.PreConsumeQuota's " +
				"PlatformPreAuthID re-entry guard), which is why raising RetryTimes " +
				"cannot multiply a customer's hold.",
		},
		{
			name:  "QuotaRemindThreshold",
			got:   defaultValue(common.QuotaRemindThreshold),
			want:  "1000",
			claim: "web/src/pages/Setting/Operation/SettingsMonitoring.jsx",
			why: "Balance under which the low-quota reminder mail fires " +
				"(internal/app/quota.go). The claim an operator actually meets is the " +
				"console field 额度提醒阈值 in SettingsMonitoring.jsx, whose helper text " +
				"promises mail below this number. doc/runbook/quota-cap-402.md does NOT " +
				"mention this key — the previous version of this row said it did.",
		},
		{
			name:  "QuotaPerUnit",
			got:   defaultValue(common.QuotaPerUnit),
			want:  "500000",
			claim: "doc/billing-system-guide.md",
			why: "Quota-to-currency divisor used by every money-facing surface " +
				"(/api/status, billing_self.go, v2_billing_invoices.go, the LLM span's " +
				"costCNY in relay.go). repo/option.go already refuses a <=0 write for " +
				"this key; this row pins what the binary starts with.",
		},
		{
			name:  "QuotaForNewUser",
			got:   defaultValue(common.QuotaForNewUser),
			want:  "0",
			claim: "web/src/components/settings/QuotaLimitsSettingPage.jsx",
			why: "Free quota granted on registration. 0 is deliberate on this deployment " +
				"— account lifecycle and money belong to platform, newhub is a relying " +
				"party — but it is also the number an operator reading upstream New API " +
				"documentation would least expect, so it is pinned rather than left to " +
				"be rediscovered.",
		},
		{
			name:  "QuotaForInviter",
			got:   defaultValue(common.QuotaForInviter),
			want:  "0",
			claim: "web/src/components/settings/QuotaLimitsSettingPage.jsx",
			why: "Invite reward for the inviter. Same reasoning as QuotaForNewUser: the " +
				"invite flow exists in code and pays nothing by default.",
		},
		{
			name:  "QuotaForInvitee",
			got:   defaultValue(common.QuotaForInvitee),
			want:  "0",
			claim: "web/src/components/settings/QuotaLimitsSettingPage.jsx",
			why:   "Invite reward for the invitee. Same reasoning as QuotaForInviter.",
		},
		{
			name:  "SessionTimeoutMinutes",
			got:   defaultValue(common.SessionTimeoutMinutes),
			want:  "10080",
			claim: "web/src/pages/v2/Admin/Settings/index.jsx",
			why: "Console session lifetime, 7 days. repo/option.go clamps a <=0 write " +
				"back to this same number, so the two must not drift apart.",
		},
		{
			name:  "RelayResponseHeaderTimeout",
			got:   defaultValue(common.RelayResponseHeaderTimeout),
			want:  "90000ms",
			claim: "doc/runbook/channel-auto-ban.md",
			why: "Time-to-first-response-header bound. doc/runbook/channel-auto-ban.md " +
				"and internal/adapter/handler/channel_probe_policy.go's doc comment both " +
				"state '90s' in prose. It is also the per-attempt worst case that bounds " +
				"what RetryTimes costs a waiting customer.",
		},
		{
			name:      "RelayDialTimeout",
			got:       defaultValue(common.RelayDialTimeout),
			want:      "10000ms",
			claim:     ".env.example",
			claimText: "RELAY_DIAL_TIMEOUT",
			why: "TCP connect bound for an upstream call, quoted alongside the header " +
				"timeout in .env.example's Runtime Tuning block.",
		},
		{
			name:  "HealthDBPingTimeout",
			got:   defaultValue(common.HealthDBPingTimeout),
			want:  "1500ms",
			claim: "doc/runbook/db-pool-saturation.md",
			why: "Bounds the /api/health DB ping. constants.go's own comment justifies " +
				"the number against the readinessProbe timeoutSeconds:2 window — if the " +
				"number moves above that window the comment becomes false.",
		},
		{
			name:      "DBConnectRetries",
			got:       defaultValue(common.DBConnectRetries),
			want:      "5",
			claim:     ".env.example",
			claimText: "DB_CONNECT_RETRIES",
			why: "Boot connect-retry budget. constants.go's comment computes a worst " +
				"case of about 40s from this number and asserts it stays well under the " +
				"~120s startupProbe; the arithmetic breaks if the number moves.",
		},
		{
			name:      "DBConnectRetryBaseDelay",
			got:       defaultValue(common.DBConnectRetryBaseDelay),
			want:      "1000ms",
			claim:     ".env.example",
			claimText: "DB_CONNECT_RETRY_DELAY_MS",
			why:       "First backoff step in the same ~40s worst-case arithmetic.",
		},
		{
			name:      "DBConnectRetryMaxDelay",
			got:       defaultValue(common.DBConnectRetryMaxDelay),
			want:      "10000ms",
			claim:     ".env.example",
			claimText: "DB_CONNECT_RETRY_MAX_DELAY_MS",
			why:       "Backoff ceiling in the same ~40s worst-case arithmetic.",
		},
		{
			name:      "DBConnectPingTimeout",
			got:       defaultValue(common.DBConnectPingTimeout),
			want:      "5000ms",
			claim:     ".env.example",
			claimText: "DB_CONNECT_PING_TIMEOUT_MS",
			why: "Per-attempt ping deadline in the same ~40s worst-case arithmetic " +
				"(5 attempts x 5s is its larger half).",
		},
		{
			name:      "CostSpikeProtectionEnabled",
			got:       defaultValue(common.CostSpikeProtectionEnabled),
			want:      "true",
			claim:     ".env.example",
			claimText: "COST_SPIKE_PROTECTION_ENABLED",
			why: "Whether the per-user 5-minute window is recorded at all. " +
				"doc/runbook/cost-spike-429.md is the operator runbook for the limiter " +
				"this switches, but it names only the metric series, so the claim cited " +
				"here is .env.example's COST_SPIKE_PROTECTION_ENABLED block.",
		},
		{
			name:      "CostSpikeHardLimitPer5Min",
			got:       defaultValue(common.CostSpikeHardLimitPer5Min),
			want:      "50000",
			claim:     ".env.example",
			claimText: "COST_SPIKE_HARD_LIMIT_PER_5MIN",
			why: "The cost-spike threshold in quota units. constants.go's comment states " +
				"it has never been crossed by real traffic and uses that to argue for " +
				"observe-only mode; changing the number silently would void the " +
				"argument. .env.example carries the operator-facing spelling.",
		},
		{
			name:      "CostSpikeEnforce",
			got:       defaultValue(common.CostSpikeEnforce),
			want:      "false",
			claim:     ".env.example",
			claimText: "COST_SPIKE_ENFORCE",
			why: "Observe-only. constants.go carries a three-point argument for why this " +
				"is off; flipping it makes a breach auto-disable a paying customer's " +
				"account. .env.example carries the operator-facing spelling.",
		},
		{
			name:      "BillingDegradedSpendCapLB",
			got:       defaultValue(common.BillingDegradedSpendCapLB),
			want:      "50",
			claim:     "doc/runbook/industrial-readiness-gated-actions.md",
			claimText: "BILLING_DEGRADED_SPEND_CAP_LB",
			why: "Per-tenant ceiling of unsecured spend admitted while the platform " +
				"billing breaker is OPEN. doc/runbook/industrial-readiness-gated-actions.md " +
				"lists signing off this number as a gated action and quotes the default; " +
				"constants.go calls the VALUE an owner-signed business-risk decision, " +
				"which is exactly the kind of number that must not move without someone " +
				"noticing. (doc/runbook/platform-billing-breaker-open.md, which the " +
				"previous version of this row cited, never names it.)",
		},
		{
			name:      "BillingDegradedWindowSec",
			got:       defaultValue(common.BillingDegradedWindowSec),
			want:      "3600",
			claim:     ".env.example",
			claimText: "BILLING_DEGRADED_WINDOW_SEC",
			why:       "Rolling window the cap above is measured over.",
		},
		{
			name:  "CriticalRateLimitNum",
			got:   defaultValue(common.CriticalRateLimitNum),
			want:  "20",
			claim: "internal/adapter/middleware/rate-limit.go",
			why: "Requests per CriticalRateLimitDuration on the critical (login / " +
				"password-reset / verification) routes.",
		},
		{
			name:  "CriticalRateLimitDuration",
			got:   defaultValue(common.CriticalRateLimitDuration),
			want:  "1200",
			claim: "internal/adapter/middleware/rate-limit.go",
			why: "Seconds. constants.go's comment above this block states no duration " +
				"here may exceed RateLimitKeyExpirationDuration (1200000ms = 1200s), and " +
				"this row sits exactly ON that bound — the two numbers have to be read " +
				"together.",
		},
		{
			name:  "RateLimitKeyExpirationDuration",
			got:   defaultValue(common.RateLimitKeyExpirationDuration),
			want:  "1200000ms",
			claim: "internal/adapter/middleware/rate-limit.go",
			why: "The bound the comment above the rate-limit block refers to. Lowering " +
				"it below CriticalRateLimitDuration silently breaks that invariant.",
		},
		{
			name:  "InternalApiRateLimitEnable",
			got:   defaultValue(common.InternalApiRateLimitEnable),
			want:  "true",
			claim: "internal/adapter/middleware/rate-limit.go",
			why: "Per-key rate limiting on /internal. constants.go's comment says a " +
				"stolen key must not be usable to DoS the internal plane; that claim " +
				"depends on this being on by default.",
		},
		{
			name:  "InternalApiReadRateLimitNum",
			got:   defaultValue(common.InternalApiReadRateLimitNum),
			want:  "600",
			claim: "internal/adapter/handler/router/internal-api-router.go",
			why:   "General /internal bucket, per authenticated key id.",
		},
		{
			name:  "InternalApiWriteRateLimitNum",
			got:   defaultValue(common.InternalApiWriteRateLimitNum),
			want:  "120",
			claim: "internal/adapter/handler/router/internal-api-router.go",
			why:   "Tighter bucket on /internal write endpoints.",
		},
		{
			name:  "InternalApiProvisionRateLimitNum",
			got:   defaultValue(common.InternalApiProvisionRateLimitNum),
			want:  "60",
			claim: "internal/adapter/handler/router/internal-api-router.go",
			why:   "Tightest bucket: provisioning and credit-pool funding.",
		},
		{
			name:  "LogConsumeEnabled",
			got:   defaultValue(common.LogConsumeEnabled),
			want:  "true",
			claim: "web/src/pages/Setting/Operation/SettingsLog.jsx",
			why: "Whether a consume-log row is written per billable relay. Off would " +
				"silently empty the leaderboard, quota_data and every log-derived " +
				"dashboard the runbooks point at.",
		},
		{
			name:  "RegisterEnabled",
			got:   defaultValue(common.RegisterEnabled),
			want:  "true",
			claim: "deploy/k8s/r6-uat/README.md",
			why: "Self-service registration, shipped ON. The only in-repo evidence of a " +
				"deployment overriding it is deploy/k8s/r6-uat/README.md, which covers " +
				"UAT. PRODUCTION IS UNVERIFIED: the operator's 2026-09 read found six " +
				"rows in the production `options` table and did not record whether " +
				"RegisterEnabled is one of them, so the production console may be open " +
				"to self-service registration on this compiled true. Run the query in " +
				"doc/runbook/shipped-defaults.md before repeating the old claim that " +
				"'both instances turn it off'. The three rows below decide how bad that " +
				"would be.",
		},
		{
			name:  "PasswordRegisterEnabled",
			got:   defaultValue(common.PasswordRegisterEnabled),
			want:  "true",
			claim: "web/src/components/settings/AuthSettingPage.jsx",
			why: "Whether the open registration above accepts a password signup at all. " +
				"Pinned beside RegisterEnabled because the two are only dangerous " +
				"together: RegisterEnabled=true with this false leaves no password " +
				"signup form to abuse.",
		},
		{
			name:  "EmailVerificationEnabled",
			got:   defaultValue(common.EmailVerificationEnabled),
			want:  "false",
			claim: "web/src/components/settings/AuthSettingPage.jsx",
			why: "Whether a registration must prove control of its email address. " +
				"Shipped OFF, directly beside RegisterEnabled=true and " +
				"PasswordRegisterEnabled=true: on an instance with no options rows, " +
				"anyone can create an account with an address they do not own. Pinned " +
				"so the three move together in a reader's mind instead of one at a time.",
		},
		{
			name:  "TurnstileCheckEnabled",
			got:   defaultValue(common.TurnstileCheckEnabled),
			want:  "false",
			claim: "web/src/components/settings/AuthSettingPage.jsx",
			why: "The only bot check in front of that registration form, shipped OFF. " +
				"Same reason as the row above: the exposure is the COMBINATION, and no " +
				"single row was pinned before cycle-14 L2's second pass.",
		},
	}
}

// TestShippedDefaultsMatchTheirDocumentedValues pins table 1.
//
// Mutation that proves it: change any pinned variable in
// internal/pkg/common/constants.go by one and this fails naming the variable,
// the value found and the value pinned.
func TestShippedDefaultsMatchTheirDocumentedValues(t *testing.T) {
	rows := shippedDefaults()
	if len(rows) < 20 {
		t.Fatalf("shippedDefaults() returned %d rows — the table was gutted, not the defaults", len(rows))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.name] {
			t.Errorf("duplicate row for %s — two rows can disagree and only one of them will be read", row.name)
		}
		seen[row.name] = true
		if strings.TrimSpace(row.why) == "" {
			t.Errorf("%s: no `why` — a pin whose reason is not written down is a number nobody can safely change", row.name)
		}
		if row.got != row.want {
			t.Errorf("shipped default %s = %s, pinned %s\n  why this is pinned: %s\n"+
				"  If the new value is intended, change the row AND the reason in the same edit, and check "+
				"doc/runbook/shipped-defaults.md still tells the truth.",
				row.name, row.got, row.want, row.why)
		}
	}
}

// wordBoundaryRe builds a matcher for text that is NOT part of a longer
// identifier. A plain strings.Contains would let doc/runbook/settlement-failed.md
// satisfy the PreConsumedQuota row through the word FinalPreConsumedQuota, which
// is a different variable in a different sentence.
func wordBoundaryRe(text string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9_])` + regexp.QuoteMeta(text) + `(?:[^A-Za-z0-9_]|$)`)
}

// claimTextFor is the literal to look for in a row's claim file.
func claimTextFor(row shippedDefault) string {
	if row.claimText != "" {
		return row.claimText
	}
	return row.name
}

// TestEveryPinnedDefaultCitesAFileThatSaysIt enforces table 1's own admission
// rule: a row belongs here only because some file an operator or the next
// engineer will read makes a claim about the key.
//
// BLIND SPOT (restating point 7 of the header): this proves the cited file
// CONTAINS the key. It cannot read the sentence around it. A file that names
// the key and lies about it passes here — that is how
// doc/runbook/channel-auto-ban.md kept saying production reads
// ChannelDisableThreshold from an `options` row that does not exist.
func TestEveryPinnedDefaultCitesAFileThatSaysIt(t *testing.T) {
	root := repoRootForGates(t)
	var problems []string
	for _, row := range shippedDefaults() {
		if strings.TrimSpace(row.claim) == "" {
			problems = append(problems, row.name+": no claim file. A number nobody documents is not a contract with the operator; either cite the file that describes it or drop the row.")
			continue
		}
		if row.claim == shippedDefaultsRunbook {
			problems = append(problems, row.name+": cites "+shippedDefaultsRunbook+", which was written FROM this table. A row may not be its own evidence.")
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(row.claim)))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: claim file %s cannot be read: %v", row.name, row.claim, err))
			continue
		}
		text := claimTextFor(row)
		if !wordBoundaryRe(text).Match(data) {
			problems = append(problems, fmt.Sprintf(
				"%s: claim file %s never mentions %q. The row's reason says that file documents this default; it does not. "+
					"Cite the file that really does (a runbook, .env.example, the console field, or the consumer whose comment explains it), "+
					"or set claimText to the spelling that file uses.",
				row.name, row.claim, text))
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("pinned defaults whose cited claim does not exist:\n  %s", strings.Join(problems, "\n  "))
	}
}

// retryTimesConsumer is one file that reads the failover budget.
type retryTimesConsumer struct {
	// file is repo-relative.
	file string
	// refs is how many times the literal `common.RetryTimes` appears in it.
	refs int
	// why says what this file does with the budget. This is the column the
	// cycle-14 L2 review was about: the budget was justified as if
	// RelayHandler were its only reader.
	why string
}

// retryTimesConsumers is the census. Raising RetryTimes from 0 to 2 switched on
// every loop below at once, so every one of them needs a sentence here.
func retryTimesConsumers() []retryTimesConsumer {
	return []retryTimesConsumer{
		{
			file: "internal/adapter/handler/relay.go",
			refs: 4,
			why: "TWO loops, not one. Three references are RelayHandler's synchronous " +
				"failover loop (the header `retryParam.GetRetry() <= common.RetryTimes`, " +
				"the remaining budget handed to shouldRetry, and the same subtraction on " +
				"the channel-selection-error branch). The FOURTH, " +
				"`retryTimes := common.RetryTimes` in RelayTask, is the async task relay: " +
				"at the old default of 0 its loop body never ran, and raising the budget " +
				"switched it on. It re-SUBMITS a video / music / Suno job, which is not " +
				"idempotent, and shouldRetryTaskRelay tests the status code before " +
				"LocalError, so our own local 500s retry too — including " +
				"`insert_task_failed`, which is raised AFTER adaptor.DoRequest has " +
				"already created the job at the vendor. A retry there creates a second " +
				"vendor job. See constants.go for why that is currently accepted and " +
				"what would scope it.",
		},
		{
			file: "internal/app/channel_select.go",
			refs: 2,
			why: "The selector, and the reason the attempt bound is PER GROUP. For an " +
				"`auto`-group token with cross-group retry it compares the in-group " +
				"retry index against the budget and, when the group is exhausted, calls " +
				"param.SetRetry(0) + param.ResetRetryNextTry() so the outer loop starts " +
				"a fresh budget on the next group. Any sentence of the form 'total " +
				"upstream attempts = RetryTimes + 1 per request' is false while these " +
				"two lines exist; TestAutoGroupRetryBudgetResetIsStillThere pins them.",
		},
		{
			file: "internal/adapter/repo/option.go",
			refs: 3,
			why: "The options-table plumbing, not a consumer: InitOptionMap seeds " +
				"OptionMap[\"RetryTimes\"] from the compiled value so the console shows " +
				"what is running, and updateOptionMap's `RetryTimes` case applies a row " +
				"through optionInt (previous value kept on a parse failure). optionInt " +
				"accepts a NEGATIVE integer and RetryTimes is not in " +
				"positiveRangeOptionKinds, so `RetryTimes = -1` makes RelayHandler's " +
				"loop body never execute and every relay answers the all-breakers-open " +
				"503 — see the warning in doc/runbook/shipped-defaults.md.",
		},
	}
}

// retryTimesRefRe is the census selector: the literal qualified reference.
var retryTimesRefRe = regexp.MustCompile(`common\.RetryTimes`)

// TestEveryRetryTimesConsumerIsAccountedFor walks the tree and requires every
// non-test .go file that mentions common.RetryTimes to have a row above.
//
// It exists because the cycle-14 L2 first pass justified the number against
// ONE loop while three other pieces of code read the same variable, and the
// gate that sat beside that justification asserted "the loop header appears
// exactly once in relay.go" — true, exhaustive-looking, and blind to the
// second loop in the same file.
//
// BLIND SPOT (restating point 8 of the header): this counts a literal. A file
// that copies the value into a local and spends it elsewhere is counted ONCE,
// at the copy; a use inside package common itself is unqualified and invisible;
// and the `why` column is prose this test cannot check.
func TestEveryRetryTimesConsumerIsAccountedFor(t *testing.T) {
	root := repoRootForGates(t)
	found := map[string]int{}
	scanned := 0
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if sourceSizeSkipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		scanned++
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		if n := len(retryTimesRefRe.FindAll(data, -1)); n > 0 {
			rel := strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(p, root)), "/")
			found[rel] = n
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if scanned < 500 {
		t.Fatalf("scanned only %d non-test Go files — the walk is broken, not the sources", scanned)
	}
	if len(found) == 0 {
		t.Fatalf("no file mentions %q anywhere. A selector that comes back empty is an unverified gate, not a passing one", retryTimesRefRe.String())
	}

	listed := map[string]retryTimesConsumer{}
	var problems []string
	for _, row := range retryTimesConsumers() {
		if _, dup := listed[row.file]; dup {
			problems = append(problems, row.file+": listed twice in retryTimesConsumers()")
		}
		if strings.TrimSpace(row.why) == "" {
			problems = append(problems, row.file+": no `why`. The point of this census is the sentence, not the count.")
		}
		listed[row.file] = row
	}
	for rel, n := range found {
		row, ok := listed[rel]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"%s reads common.RetryTimes %d time(s) and has no row in retryTimesConsumers(). "+
					"Raising the failover budget switches on EVERY loop that reads it; add a row saying what this one does with it "+
					"(which failures it retries, whether the work it repeats is idempotent) before the number is justified.",
				rel, n))
			continue
		}
		if row.refs != n {
			problems = append(problems, fmt.Sprintf(
				"%s reads common.RetryTimes %d time(s), the census says %d — a reader was added or removed. Update the row AND its reason: %s",
				rel, n, row.refs, row.why))
		}
	}
	for rel := range listed {
		if _, ok := found[rel]; !ok {
			problems = append(problems, rel+": listed in retryTimesConsumers() but reads common.RetryTimes zero times — remove the stale row rather than leaving a reason for code that is gone.")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("failover-budget census:\n  %s", strings.Join(problems, "\n  "))
	}
}

// envDefault is one pinned env-knob default, as written in the source.
type envDefault struct {
	// env is the environment variable name, exactly as the source spells it.
	env string
	// want is the literal default argument, exactly as the source writes it
	// (source TEXT, not an evaluated number — "20*60" stays "20*60").
	want string
	// why names the claim that depends on it.
	why string
}

// envTunableDefaults is table 2: the knobs whose effective default lives ONLY
// in internal/pkg/common/init.go (the package variable has no initialiser, so
// table 1 cannot see it), plus the handful where constants.go and init.go both
// state a number and are required to agree.
func envTunableDefaults() []envDefault {
	return []envDefault{
		{"SYNC_FREQUENCY", "60", "Channel/option cache sync period, and therefore how long a direct options-table write takes to reach every replica. .env.example and deploy/k8s/r6-stage/deployment.yaml both state 60; doc/runbook/shipped-defaults.md tells the operator to wait this long before checking a pod."},
		{"RELAY_TIMEOUT", "0", "Total upstream timeout. constants.go's comment and .env.example both say it is deliberately 0 so a long SSE stream is never cut; the variable itself has no initialiser, so this line IS the default."},
		{"RELAY_RESPONSE_HEADER_TIMEOUT", "90", "Must agree with common.RelayResponseHeaderTimeout in table 1 — InitEnv overwrites the constants.go value with this one at boot, so a divergence would make the constants.go comment false without table 1 noticing."},
		{"RELAY_DIAL_TIMEOUT", "10", "Must agree with common.RelayDialTimeout in table 1, same reason."},
		{"HEALTH_DB_PING_TIMEOUT_MS", "1500", "Must agree with common.HealthDBPingTimeout in table 1, same reason."},
		{"DB_CONNECT_RETRIES", "5", "Must agree with common.DBConnectRetries in table 1, same reason."},
		{"GLOBAL_API_RATE_LIMIT", "180", "Requests per GLOBAL_API_RATE_LIMIT_DURATION on /api/*. GlobalApiRateLimitNum has no initialiser in constants.go, so this literal is the only default that exists."},
		{"GLOBAL_WEB_RATE_LIMIT", "60", "Same, for the web bucket."},
		{"GLOBAL_V2_RATE_LIMIT", "600", ".env.example documents 600 for the /api/v2 group's own GV bucket; GlobalV2RateLimitNum has no initialiser."},
		{"CRITICAL_RATE_LIMIT", "20", "Must agree with common.CriticalRateLimitNum in table 1."},
		{"COST_SPIKE_ENFORCE", "false", "Must agree with common.CostSpikeEnforce in table 1 — this is the line that decides whether a breach disables a paying account."},
		{"COST_SPIKE_HARD_LIMIT_PER_5MIN", "50000", "Must agree with common.CostSpikeHardLimitPer5Min in table 1."},
		{"BILLING_DEGRADED_SPEND_CAP_LB", "50.0", "Must agree with common.BillingDegradedSpendCapLB in table 1."},
		{"STREAMING_TIMEOUT", "300", "Per-chunk stream read bound. Quoted as '300s' in doc/runbook/channel-auto-ban.md and in channel_probe_policy.go's doc comment; constant.StreamingTimeout has no initialiser at all, so this literal is the whole default."},
		{"MAX_REQUEST_BODY_MB", "64", "Decompressed request-body ceiling — the number behind every 413 in the relay path."},
	}
}

// envDefaultCallRe matches a GetEnvOrDefault / ...String / ...Float / ...Bool
// call whose env key is a double-quoted literal, capturing the key and the
// default argument's source text. It deliberately does NOT match a call whose
// key is a named const (see blind spot 3).
var envDefaultCallRe = regexp.MustCompile(`GetEnvOrDefault(?:String|Float|Bool)?\("([A-Z0-9_]+)",\s*([^)]+)\)`)

// envDefaultScanFiles is the exact source this gate reads for table 2. Naming
// the files here rather than walking the tree is the point: the set is small
// enough to state, so "the scanner found nothing" is always a gate failure
// rather than a silent pass.
var envDefaultScanFiles = []string{
	"internal/pkg/common/init.go",
}

// TestEnvTunableDefaultsMatchTheirSourceLiteral pins table 2.
//
// Mutations that prove it: change any default argument in
// internal/pkg/common/init.go and this fails with the key, the literal found
// and the literal pinned; delete the call entirely and it fails with
// "not found in any scanned file", never silently.
func TestEnvTunableDefaultsMatchTheirSourceLiteral(t *testing.T) {
	root := repoRootForGates(t)

	found := map[string][]string{}
	for _, rel := range envDefaultScanFiles {
		p := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v — the scanned file moved; fix envDefaultScanFiles rather than deleting the row", rel, err)
		}
		matches := envDefaultCallRe.FindAllStringSubmatch(string(data), -1)
		if len(matches) == 0 {
			t.Fatalf("%s: the env-default regex matched nothing. A selector that returns empty is an unverified gate, not a passing one — "+
				"either the call shape changed or the file did.", rel)
		}
		for _, m := range matches {
			found[m[1]] = append(found[m[1]], strings.TrimSpace(m[2]))
		}
	}

	rows := envTunableDefaults()
	if len(rows) < 10 {
		t.Fatalf("envTunableDefaults() returned %d rows — the table was gutted, not the defaults", len(rows))
	}
	var missing []string
	for _, row := range rows {
		got, ok := found[row.env]
		if !ok {
			missing = append(missing, fmt.Sprintf("%s: not found in any scanned file (%s)",
				row.env, strings.Join(envDefaultScanFiles, ", ")))
			continue
		}
		if len(got) != 1 {
			t.Errorf("%s: read %d times (%s) — this gate pins one default per key; with two call sites the effective default depends on order",
				row.env, len(got), strings.Join(got, " / "))
			continue
		}
		if got[0] != row.want {
			t.Errorf("env default %s = %s, pinned %s\n  why this is pinned: %s\n"+
				"  If the new value is intended, change the row AND the reason in the same edit.",
				row.env, got[0], row.want, row.why)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("env defaults the scanner could not find — an empty match is NOT evidence the default is unchanged:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

// relayRetryLoopRe is RelayHandler's loop header, the one that turns RetryTimes
// into an attempt budget: the body runs once for retry = 0..RetryTimes
// inclusive, so a single relay makes at most RetryTimes+1 upstream attempts
// WITHIN ONE GROUP (channel_select.go restarts the counter per auto group —
// see TestAutoGroupRetryBudgetResetIsStillThere).
var relayRetryLoopRe = regexp.MustCompile(`retryParam\.GetRetry\(\)\s*<=\s*common\.RetryTimes`)

// relayTaskBudgetRe and relayTaskLoopRe are the SECOND reader of the same knob,
// in the same file: RelayTask copies the budget into a local and spends it on
// re-submitting an async job. The previous version of this gate asserted the
// header above appeared "exactly once" in relay.go and read as though that
// settled what the number does to relay.go — while this loop, written in a
// shape the first regex cannot match, sat 550 lines below it.
var relayTaskBudgetRe = regexp.MustCompile(`retryTimes\s*:=\s*common\.RetryTimes`)
var relayTaskLoopRe = regexp.MustCompile(`shouldRetryTaskRelay\([^)]*\)\s*&&\s*retryParam\.GetRetry\(\)\s*<\s*retryTimes`)

// relayRetryBudgetRe is the remaining-budget argument handed to shouldRetry.
// It is what makes "RetryTimes counts retries, not attempts" true on the
// decision side as well as on the loop side.
var relayRetryBudgetRe = regexp.MustCompile(`common\.RetryTimes\s*-\s*retryParam\.GetRetry\(\)`)

// TestRelayRetryBudgetArithmeticStillHolds checks that the expressions the
// RetryTimes row's justification rests on are still written the way that
// justification assumes — in BOTH of relay.go's loops — and that the shipped
// number lands inside the band the runbook argues for.
//
// BLIND SPOT (restating point 6 above, because this is the test most likely to
// be misread as proof of failover): this is a REGEX OVER SOURCE TEXT. It does
// not start a relay, does not call an upstream, and cannot tell whether a
// second attempt is ever made. It also cannot see that an open circuit breaker
// consumes one iteration of the synchronous loop via `continue`, which means
// the real number of UPSTREAM CALLS is at most RetryTimes+1 per group and can
// be zero. It says nothing about WHICH channel a retry draws: when a model has
// a single candidate, repo.GetRandomSatisfiedChannelForTenant returns that one
// channel again, so the budget buys repetition rather than failover.
func TestRelayRetryBudgetArithmeticStillHolds(t *testing.T) {
	root := repoRootForGates(t)
	rel := "internal/adapter/handler/relay.go"
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	src := string(data)

	if n := len(relayRetryLoopRe.FindAllString(src, -1)); n != 1 {
		t.Fatalf("%s: found the retry-loop header %q %d times, want exactly 1. "+
			"The RetryTimes pin in shippedDefaults() is justified as 'total attempts minus one', "+
			"which is only true while the loop reads `retry <= common.RetryTimes`.",
			rel, relayRetryLoopRe.String(), n)
	}
	if !relayRetryBudgetRe.MatchString(src) {
		t.Fatalf("%s: the remaining-budget expression %q is gone. shouldRetry's retryTimes argument "+
			"is how RetryTimes stops being a bound on the loop only; without it the pinned number "+
			"means something different on the decision side than on the loop side.",
			rel, relayRetryBudgetRe.String())
	}
	if !relayTaskBudgetRe.MatchString(src) || !relayTaskLoopRe.MatchString(src) {
		t.Fatalf("%s: RelayTask's budget copy (%q) or its loop header (%q) is gone. That loop is the "+
			"SECOND reader of common.RetryTimes in this file; it re-submits a non-idempotent async job, "+
			"and it is pinned here so a change to it cannot be made while the RetryTimes justification "+
			"in internal/pkg/common/constants.go still describes only the synchronous loop.",
			rel, relayTaskBudgetRe.String(), relayTaskLoopRe.String())
	}

	// The band, not the number: the exact value is pinned in table 1. This is
	// the property — at least one failover, and a bounded one.
	maxAttempts := common.RetryTimes + 1 // per group; see the auto-group test
	if common.RetryTimes < 1 {
		t.Errorf("common.RetryTimes = %d permits %d upstream attempt(s) per relay per group: a single 5xx "+
			"reaches the customer even when a healthy channel serves the same model, and every "+
			"failover sentence in internal/app/channel_select.go and doc/slo-relay.md is unreachable. "+
			"A NEGATIVE value is worse than 0: RelayHandler's loop body never runs at all and every "+
			"relay answers the all-breakers-open 503.",
			common.RetryTimes, maxAttempts)
	}
	if common.RetryTimes > 3 {
		t.Errorf("common.RetryTimes = %d permits %d upstream attempts per relay per group. Each one is a "+
			"fresh upstream call bounded by RelayResponseHeaderTimeout (%s), so the worst-case wait before "+
			"the customer sees an error is that many times that bound (times the number of auto groups), "+
			"a total-outage window is amplified by the same factor against upstreams that are already "+
			"failing, AND every attempt reports its own outcome to the per-channel circuit breaker, so a "+
			"single-candidate model trips the breaker in ceil(threshold/attempts) failing requests.",
			common.RetryTimes, maxAttempts, common.RelayResponseHeaderTimeout)
	}
}
