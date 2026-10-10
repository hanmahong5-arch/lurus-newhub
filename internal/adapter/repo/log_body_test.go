package repo

// log_body_test.go - migration 052: the opt-in body archive. Hermetic SQLite
// tier (AsyncGo is forced inline by TestMain, so a write is visible as soon as
// RecordConsumeLog returns).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app/contentpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func archiveFixture(t *testing.T) (retentionFixture, func()) {
	t.Helper()
	f, done := setupRetentionFixture(t)
	if err := DB.AutoMigrate(&LogBody{}); err != nil {
		t.Fatalf("migrate log_bodies: %v", err)
	}
	InvalidateSedimentationConsentCache()
	return f, func() {
		InvalidateSedimentationConsentCache()
		done()
	}
}

// archiveCtx is a relay-shaped context: covered wire format, a cached
// (already content-ruled) request body and a non-streaming response text.
func archiveCtx(f retentionFixture, reqID, body string) *gin.Context {
	c := newTenantScopeGinCtx("ret-user", f.tenant.Id)
	c.Set(string(constant.ContextKeyRelayFormat), types.RelayFormatOpenAI)
	c.Set(common.KeyRequestBody, []byte(body))
	c.Set(string(constant.ContextKeyResponseText), "the answer")
	c.Set(common.RequestIdKey, reqID)
	return c
}

func recordArchiveConsume(c *gin.Context, f retentionFixture, level string) {
	RecordConsumeLog(c, f.userId, RecordConsumeLogParams{
		ChannelId: 3, ModelName: "model-a", TokenName: f.token.Name, TokenId: f.token.Id,
		Quota: 5, ChannelType: 1, LogDetailLevel: level,
	})
}

func countBodies(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := DB.Model(&LogBody{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func consent(t *testing.T, f retentionFixture, on bool) {
	t.Helper()
	if err := SetTenantSedimentationConsent(f.tenant.Id, on); err != nil {
		t.Fatal(err)
	}
}

func TestLogBodyArchive_WritesWhenEveryConditionHolds(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	consent(t, f, true)

	before := time.Now().Unix()
	recordArchiveConsume(archiveCtx(f, "req-ok", `{"messages":[{"content":"masked"}]}`), f, "")

	b, err := GetLogBody(f.tenant.Id, "req-ok")
	if err != nil {
		t.Fatalf("body not archived: %v", err)
	}
	if b.RequestBody != `{"messages":[{"content":"masked"}]}` || b.ResponseText != "the answer" ||
		!b.ResponseCaptured || b.Truncated {
		t.Fatalf("unexpected row: %+v", b)
	}
	if b.TenantId != f.tenant.Id || b.TokenId != int64(f.token.Id) || b.Model != "model-a" {
		t.Fatalf("attribution wrong: %+v", b)
	}
	wantExp := before + int64(logBodyDefaultRetentionDays)*86400
	if b.ExpiresAt < wantExp-5 || b.ExpiresAt > wantExp+86400+5 {
		t.Fatalf("expires_at %d not ~%d (30d default)", b.ExpiresAt, wantExp)
	}
}

// Every gate condition, negated alone, must leave log_bodies empty - while the
// ordinary consume row is still written (the archive never gates the log).
func TestLogBodyArchive_EachConditionAloneBlocks(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f retentionFixture, c *gin.Context) (level string)
	}{
		{"no_tenant_consent", func(t *testing.T, f retentionFixture, c *gin.Context) string {
			consent(t, f, false)
			return ""
		}},
		{"tenant_retention_metadata_only", func(t *testing.T, f retentionFixture, c *gin.Context) string {
			if err := SetTenantContentRetention(f.tenant.Id, contentpolicy.RetentionMetadataOnly); err != nil {
				t.Fatal(err)
			}
			return ""
		}},
		{"tenant_retention_none", func(t *testing.T, f retentionFixture, c *gin.Context) string {
			if err := SetTenantContentRetention(f.tenant.Id, contentpolicy.RetentionNone); err != nil {
				t.Fatal(err)
			}
			return ""
		}},
		{"token_retention_tighter", func(t *testing.T, f retentionFixture, c *gin.Context) string {
			if err := SetTokenContentRetention(f.token.Id, f.tenant.Id, contentpolicy.RetentionMetadataOnly); err != nil {
				t.Fatal(err)
			}
			return ""
		}},
		{"platform_default_tighter", func(t *testing.T, f retentionFixture, c *gin.Context) string {
			m := contentpolicy.RetentionMetadataOnly
			contentpolicy.SetPlatformDefault(&m)
			return ""
		}},
		{"channel_zero_retention", func(t *testing.T, f retentionFixture, c *gin.Context) string {
			c.Set(string(constant.ContextKeyChannelSetting), dto.ChannelSettings{DataCollection: "deny"})
			return ""
		}},
		{"request_denies_collection", func(t *testing.T, f retentionFixture, c *gin.Context) string {
			c.Set(string(constant.ContextKeyProviderFilter), dto.NormalizeProviderFilter("", "deny"))
			return ""
		}},
		{"log_detail_none", func(t *testing.T, f retentionFixture, c *gin.Context) string { return "none" }},
		{"format_not_covered_by_content_rules", func(t *testing.T, f retentionFixture, c *gin.Context) string {
			c.Set(string(constant.ContextKeyRelayFormat), types.RelayFormat(types.RelayFormatEmbedding))
			return ""
		}},
		{"relay_format_unknown", func(t *testing.T, f retentionFixture, c *gin.Context) string {
			c.Set(string(constant.ContextKeyRelayFormat), nil)
			return ""
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, done := archiveFixture(t)
			defer done()
			consent(t, f, true) // the positive baseline; the case negates exactly one thing
			c := archiveCtx(f, "req-"+tc.name, `{"a":1}`)
			level := tc.setup(t, f, c)
			recordArchiveConsume(c, f, level)
			if n := countBodies(t); n != 0 {
				t.Fatalf("%s: %d body rows written, want 0", tc.name, n)
			}
			if tc.name != "log_detail_none" { // none skips the log row itself
				var rows int64
				LOG_DB.Model(&Log{}).Where("type = ?", LogTypeConsume).Count(&rows)
				if rows == 0 {
					t.Fatalf("%s: consume log row must still be written", tc.name)
				}
			}
		})
	}
}

func TestLogBodyArchive_LogDetailFullIsNotRequired(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	consent(t, f, true)
	recordArchiveConsume(archiveCtx(f, "req-full", `{"a":1}`), f, "full")
	if _, err := GetLogBody(f.tenant.Id, "req-full"); err != nil {
		t.Fatalf("level full: %v", err)
	}
	recordArchiveConsume(archiveCtx(f, "req-empty", `{"a":1}`), f, "")
	if _, err := GetLogBody(f.tenant.Id, "req-empty"); err != nil {
		t.Fatalf("level empty must archive too (tenant policy decides): %v", err)
	}
}

func TestLogBodyArchive_StreamingRecordsUncapturedResponse(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	consent(t, f, true)
	c := archiveCtx(f, "req-stream", `{"stream":true}`)
	delete(c.Keys, string(constant.ContextKeyResponseText)) // stream handlers never stash text
	recordArchiveConsume(c, f, "")
	b, err := GetLogBody(f.tenant.Id, "req-stream")
	if err != nil {
		t.Fatal(err)
	}
	if b.ResponseCaptured || b.ResponseText != "" || b.RequestBody != `{"stream":true}` {
		t.Fatalf("stream row wrong: %+v", b)
	}
}

func TestLogBodyArchive_TruncatesEachFieldAt64KB(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	consent(t, f, true)
	// A 3-byte rune straddling the cap must not be split.
	big := strings.Repeat("a", LogBodyFieldMaxBytes-1) + "中" + "tail"
	c := archiveCtx(f, "req-big", big)
	c.Set(string(constant.ContextKeyResponseText), strings.Repeat("r", LogBodyFieldMaxBytes+10))
	recordArchiveConsume(c, f, "")
	b, err := GetLogBody(f.tenant.Id, "req-big")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Truncated {
		t.Fatal("truncated flag not set")
	}
	if len(b.RequestBody) != LogBodyFieldMaxBytes-1 || len(b.ResponseText) != LogBodyFieldMaxBytes {
		t.Fatalf("lens request=%d response=%d", len(b.RequestBody), len(b.ResponseText))
	}
}

func TestLogBodyArchive_SmallBodyNotFlaggedTruncated(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	consent(t, f, true)
	c := archiveCtx(f, "req-exact", strings.Repeat("a", LogBodyFieldMaxBytes))
	recordArchiveConsume(c, f, "")
	b, err := GetLogBody(f.tenant.Id, "req-exact")
	if err != nil {
		t.Fatal(err)
	}
	if b.Truncated {
		t.Fatal("a body exactly at the cap is not truncated")
	}
}

func TestLogBodyArchive_StripsNULAndInvalidUTF8(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	consent(t, f, true)
	recordArchiveConsume(archiveCtx(f, "req-dirty", "a\x00b\xffc"), f, "")
	b, err := GetLogBody(f.tenant.Id, "req-dirty")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(b.RequestBody, 0) || !strings.HasPrefix(b.RequestBody, "ab") {
		t.Fatalf("not sanitized: %q", b.RequestBody)
	}
}

func TestLogBodyArchive_NothingToStoreOrNoRequestIDWritesNothing(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	consent(t, f, true)
	c := archiveCtx(f, "", `{"a":1}`)
	recordArchiveConsume(c, f, "")
	c2 := archiveCtx(f, "req-empty-all", "")
	delete(c2.Keys, string(constant.ContextKeyResponseText))
	recordArchiveConsume(c2, f, "")
	c3 := archiveCtx(f, strings.Repeat("x", 65), `{"a":1}`)
	recordArchiveConsume(c3, f, "")
	if n := countBodies(t); n != 0 {
		t.Fatalf("%d rows, want 0", n)
	}
}

func TestLogBodyArchive_RetentionEnvAndInsertFailure(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	consent(t, f, true)
	t.Setenv("LOG_BODY_RETENTION_DAYS", "7")
	before := time.Now().Unix()
	recordArchiveConsume(archiveCtx(f, "req-7d", `{"a":1}`), f, "")
	b, err := GetLogBody(f.tenant.Id, "req-7d")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.ExpiresAt - before; got < 7*86400-5 || got > 7*86400+5 {
		t.Fatalf("expires in %ds, want ~7d", got)
	}
	t.Setenv("LOG_BODY_RETENTION_DAYS", "0")
	if LogBodyRetentionDays() != logBodyDefaultRetentionDays {
		t.Fatal("non-positive must fall back to the default, a body must always expire")
	}

	// A failing insert (table gone) neither panics nor blocks the log row.
	if err := DB.Migrator().DropTable(&LogBody{}); err != nil {
		t.Fatal(err)
	}
	recordArchiveConsume(archiveCtx(f, "req-fail", `{"a":1}`), f, "")
}

func TestLogBodyTenantIsolationAndExpiry(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	now := common.GetTimestamp()
	mk := func(tenant, rid string, exp int64) {
		if err := DB.Create(&LogBody{RequestId: rid, TenantId: tenant, CreatedAt: now, ExpiresAt: exp, RequestBody: "x"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk("tenant-a", "rid-shared-a", now+1000)
	mk("tenant-b", "rid-b", now+1000)
	mk("tenant-a", "rid-expired", now-1)
	_ = f

	if _, err := GetLogBody("tenant-a", "rid-shared-a"); err != nil {
		t.Fatalf("own tenant read: %v", err)
	}
	if _, err := GetLogBody("tenant-a", "rid-b"); !errors.Is(err, ErrLogBodyNotFound) {
		t.Fatalf("foreign tenant read = %v, want not found", err)
	}
	if _, err := GetLogBody("tenant-a", "rid-expired"); !errors.Is(err, ErrLogBodyNotFound) {
		t.Fatalf("expired read = %v, want not found", err)
	}
	if _, err := GetLogBody("", "rid-b"); !errors.Is(err, ErrLogBodyNotFound) {
		t.Fatalf("empty tenant must fail closed, got %v", err)
	}
	if b, err := GetLogBodyAnyTenant("rid-b"); err != nil || b.TenantId != "tenant-b" {
		t.Fatalf("platform read: %v %+v", err, b)
	}
}

func TestDeleteExpiredLogBodies_BatchesAndKeepsLive(t *testing.T) {
	_, done := archiveFixture(t)
	defer done()
	now := common.GetTimestamp()
	for i := 0; i < 7; i++ {
		DB.Create(&LogBody{RequestId: "old", TenantId: "t", ExpiresAt: now - int64(i) - 1})
	}
	DB.Create(&LogBody{RequestId: "live", TenantId: "t", ExpiresAt: now + 1000})

	if n, err := DeleteExpiredLogBodies(context.Background(), now, 3, 1); err != nil || n != 3 {
		t.Fatalf("first bounded pass = %d,%v want 3 (one batch)", n, err)
	}
	if pending, _ := CountExpiredLogBodies(context.Background(), now); pending != 4 {
		t.Fatalf("pending %d, want 4", pending)
	}
	if n, err := DeleteExpiredLogBodies(context.Background(), now, 3, 10); err != nil || n != 4 {
		t.Fatalf("drain = %d,%v want 4", n, err)
	}
	if left := countBodies(t); left != 1 {
		t.Fatalf("left %d, want only the live row", left)
	}
}

func TestSedimentationConsent_DefaultOffAndCacheInvalidation(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	if tenantConsents(f.tenant.Id) {
		t.Fatal("consent must default to false")
	}
	consent(t, f, true)
	if !tenantConsents(f.tenant.Id) {
		t.Fatal("consent not visible after set (cache not invalidated)")
	}
	consent(t, f, false)
	if tenantConsents(f.tenant.Id) {
		t.Fatal("withdrawal not visible immediately on this replica")
	}
	if err := SetTenantSedimentationConsent("nope", true); err == nil {
		t.Fatal("unknown tenant must error")
	}
	if tenantConsents("") {
		t.Fatal("empty tenant must never consent")
	}
}

func TestOnLogPersisted_FiresOncePerRowAndSurvivesPanics(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	resetLogPersistedHooks()
	defer resetLogPersistedHooks()
	var seen []*Log
	OnLogPersisted(func(l *Log) { panic("hook bug") })
	OnLogPersisted(func(l *Log) { seen = append(seen, l) })
	OnLogPersisted(nil)

	recordArchiveConsume(archiveCtx(f, "req-hook", `{"a":1}`), f, "")
	if len(seen) != 1 || seen[0].Type != LogTypeConsume || seen[0].TokenId != f.token.Id {
		t.Fatalf("hook saw %d rows: %+v", len(seen), seen)
	}
	recordArchiveConsume(archiveCtx(f, "req-hook2", `{"a":1}`), f, "none") // no row => no hook
	if len(seen) != 1 {
		t.Fatalf("hook fired for a skipped log row")
	}
}

func TestShouldArchiveLogBody_Pure(t *testing.T) {
	ok := LogBodyArchiveInput{TenantConsent: true, Retention: contentpolicy.RetentionFull, FormatCovered: true}
	if !ShouldArchiveLogBody(ok) {
		t.Fatal("baseline must allow")
	}
	if ShouldArchiveLogBody(LogBodyArchiveInput{}) {
		t.Fatal("zero value must deny (fail closed)")
	}
}

// The path-level matrix cannot see the log-detail veto (RecordConsumeLog
// returns before the archive for "none"), so the gate is also pinned directly:
// from the all-true baseline, flipping any ONE input must deny.
func TestShouldArchiveLogBody_EachInputAloneDenies(t *testing.T) {
	base := LogBodyArchiveInput{TenantConsent: true, Retention: contentpolicy.RetentionFull, FormatCovered: true}
	flips := map[string]func(*LogBodyArchiveInput){
		"consent":       func(i *LogBodyArchiveInput) { i.TenantConsent = false },
		"retention_md":  func(i *LogBodyArchiveInput) { i.Retention = contentpolicy.RetentionMetadataOnly },
		"retention_non": func(i *LogBodyArchiveInput) { i.Retention = contentpolicy.RetentionNone },
		"retention_inh": func(i *LogBodyArchiveInput) { i.Retention = contentpolicy.RetentionInherit },
		"channel_zdr":   func(i *LogBodyArchiveInput) { i.ChannelZeroRetention = true },
		"request_deny":  func(i *LogBodyArchiveInput) { i.RequestDeniesCollection = true },
		"detail_none":   func(i *LogBodyArchiveInput) { i.LogDetailLevel = "none" },
		"format":        func(i *LogBodyArchiveInput) { i.FormatCovered = false },
	}
	for name, flip := range flips {
		in := base
		flip(&in)
		if ShouldArchiveLogBody(in) {
			t.Errorf("%s alone must deny", name)
		}
	}
	for _, lvl := range []string{"", "full", "anything"} {
		in := base
		in.LogDetailLevel = lvl
		if !ShouldArchiveLogBody(in) {
			t.Errorf("log detail %q must not block (tenant policy decides)", lvl)
		}
	}
}

// Withdrawal is a deletion, not just a stop: the synchronous purge empties the
// tenant, and the sweep's unconsented pass is the backstop that also drops
// rows of tenants that do not (or no longer) exist.
func TestLogBodyPurgeAndUnconsentedSweep(t *testing.T) {
	f, done := archiveFixture(t)
	defer done()
	ctx := context.Background()
	now := common.GetTimestamp()
	mk := func(tenant, rid string) {
		if err := DB.Create(&LogBody{RequestId: rid, TenantId: tenant, CreatedAt: now, ExpiresAt: now + 1000, RequestBody: "x"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	consent(t, f, true)
	mk(f.tenant.Id, "own-1")
	mk(f.tenant.Id, "own-2")
	mk(f.tenant.Id, "own-3")
	mk("ghost-tenant", "ghost-1")
	mk("ghost-tenant", "ghost-2")

	// A consenting tenant's rows survive the sweep; a tenant that is not in
	// the consenting set (here: not in the tenants table at all) does not.
	if n, err := DeleteUnconsentedLogBodies(ctx, 1, 10); err != nil || n != 2 {
		t.Fatalf("unconsented sweep = %d,%v want 2", n, err)
	}
	if left := countBodies(t); left != 3 {
		t.Fatalf("left %d, want the consenting tenant's 3", left)
	}

	if n, err := PurgeTenantLogBodies(ctx, "", 2, 10); err != nil || n != 0 {
		t.Fatalf("empty tenant purge = %d,%v want 0 (never a wildcard)", n, err)
	}
	if n, err := PurgeTenantLogBodies(ctx, f.tenant.Id, 2, 10); err != nil || n != 3 {
		t.Fatalf("purge = %d,%v want 3", n, err)
	}
	if left := countBodies(t); left != 0 {
		t.Fatalf("left %d after purge, want 0", left)
	}

	// Withdrawn consent: whatever lands afterwards (in-flight writes on another
	// replica) is removed by the next sweep.
	mk(f.tenant.Id, "late")
	consent(t, f, false)
	if n, err := DeleteUnconsentedLogBodies(ctx, 10, 10); err != nil || n != 1 {
		t.Fatalf("sweep after withdrawal = %d,%v want 1", n, err)
	}
}
