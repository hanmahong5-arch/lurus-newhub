package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// Business TPM (tokens-per-minute) sliding windows — the usage side of
// middleware.BusinessRateLimit.
//
// Layering: the RPM window lives entirely inside the middleware because there
// the admission itself is the counted event. TPM instead counts SETTLED usage,
// which only surfaces on the settlement path (PostConsumeQuota, quota.go) in
// this package. middleware already imports app (cost_spike.go, distributor.go,
// auth.go), so app cannot import middleware back — the window store therefore
// lives here and both sides meet in app, exactly how cost_spike resolved the
// same cycle (recorder in app/cost_spike.go, reader middleware/cost_spike.go).
//
// Writers: PostConsumeQuota → RecordBusinessTPMUsage (fire-and-forget,
// fail-soft — recording must never fail or slow a settlement).
// Readers: middleware.BusinessRateLimit → QueryBusinessTPMTokenWindow /
// QueryBusinessTPMTenantWindow (backend errors surface so the middleware can
// fail open).
//
// Backends mirror the middleware's RPM windows: Redis ZSET (score = record
// unix-milli, member carries the token count) when common.RedisEnabled, else
// an in-process window pruned on access (single-node semantics, fine for the
// Redis-less dev/test tier).

const (
	// BizTPMWindow is the tokens-per-minute window span. Exported so the
	// middleware's Retry-After derivation uses the same span as the store.
	BizTPMWindow = time.Minute

	bizTPMTokenKeyPrefix  = "rl:biz:tpm:tok:"
	bizTPMTenantKeyPrefix = "rl:biz:tpm:tenant:"
	bizTPMModelKeyPrefix  = "rl:biz:tpm:model:"
	// bizTPMTTL keeps idle Redis keys from lingering; slack past the window
	// matches the middleware's RPM ZSET expiry.
	bizTPMTTL = BizTPMWindow + 10*time.Second
	// bizTPMRedisTimeout bounds the async record write (RecordCostSpikeWindow
	// uses the same budget). 3s, not 1s: measured live 2026-08-29 — after an
	// idle gap the first token-dimension write blocked for the full 1s and
	// died on context-deadline ("business tpm record failed for
	// rl:biz:tpm:tok:1: context deadline exceeded") while the tenant-dimension
	// write on the SAME goroutine, issued 1ms later, succeeded instantly. The
	// 1s was being spent establishing a cold pooled connection (dial + DNS),
	// not talking to Redis. See cost_spike.go for the identical failure and
	// the shared rationale.
	bizTPMRedisTimeout = 3 * time.Second
)

// BizTPMNow is the window clock. A variable (like AsyncGo above and the
// middleware's bizNow) only so cross-package test binaries can freeze it and
// drive window sliding deterministically; production never reassigns it.
var BizTPMNow = time.Now

// bizTPMTenantOf resolves the token's tenant for the tenant-dimension window.
// Seam for hermetic tests. Any failure degrades to "" (record the token
// dimension only) — resolution is best-effort, never fatal.
var bizTPMTenantOf = func(tokenID int) string {
	if repo.DB == nil {
		return ""
	}
	tok, err := repo.GetTokenById(tokenID)
	if err != nil || tok == nil {
		return ""
	}
	return tok.TenantId
}

// ---------------------------------------------------------------------------
// In-memory fallback (Redis disabled): pruned on every touch, no background
// goroutine — bounded by the set of active keys, nothing leaks on shutdown.
// ---------------------------------------------------------------------------

type bizTPMEntry struct {
	ms     int64 // record time, unix milli
	tokens int64
}

type bizTPMMemWindow struct {
	mu      sync.Mutex
	entries map[string][]bizTPMEntry // ascending by ms
}

var bizTPMMem = bizTPMMemWindow{entries: make(map[string][]bizTPMEntry)}

func (w *bizTPMMemWindow) record(key string, tokens int64, now time.Time) {
	nowMs := now.UnixMilli()
	cutoff := nowMs - BizTPMWindow.Milliseconds()

	w.mu.Lock()
	defer w.mu.Unlock()
	ts := w.entries[key]
	i := 0
	for i < len(ts) && ts[i].ms <= cutoff {
		i++
	}
	w.entries[key] = append(ts[i:], bizTPMEntry{ms: nowMs, tokens: tokens})
}

func (w *bizTPMMemWindow) query(key string, now time.Time) (total int64, oldestMs int64) {
	nowMs := now.UnixMilli()
	cutoff := nowMs - BizTPMWindow.Milliseconds()

	w.mu.Lock()
	defer w.mu.Unlock()
	ts := w.entries[key]
	i := 0
	for i < len(ts) && ts[i].ms <= cutoff {
		i++
	}
	ts = ts[i:]
	w.entries[key] = ts
	for _, e := range ts {
		total += e.tokens
	}
	if len(ts) > 0 {
		oldestMs = ts[0].ms
	}
	return total, oldestMs
}

// ---------------------------------------------------------------------------
// Redis backend: ZSET per key, member "<nano>:<tokens>:<seq>".
// ---------------------------------------------------------------------------

// bizTPMMemberSeq disambiguates members recorded in the same nanosecond —
// a duplicate ZADD member would silently collapse two usages into one (same
// reasoning as the middleware's bizMemberSeq).
var bizTPMMemberSeq atomic.Uint64

// parseBizTPMMember extracts the token count from a "<nano>:<tokens>:<seq>"
// member. Malformed entries count as 0 — skipping one bad entry is safer than
// failing the whole window read (cost_spike semantics).
func parseBizTPMMember(m string) int64 {
	parts := strings.SplitN(m, ":", 3)
	if len(parts) < 2 {
		return 0
	}
	v, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

func bizTPMRecordRedis(key string, tokens int64, now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), bizTPMRedisTimeout)
	defer cancel()

	member := fmt.Sprintf("%d:%d:%d", now.UnixNano(), tokens, bizTPMMemberSeq.Add(1))
	pipe := common.RDB.Pipeline()
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(now.UnixMilli()), Member: member})
	pipe.Expire(ctx, key, bizTPMTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		common.SysLog(fmt.Sprintf("business tpm record failed for %s: %s", key, err.Error()))
	}
}

func bizTPMRecord(key string, tokens int64, now time.Time) {
	if common.RedisEnabled && common.RDB != nil {
		bizTPMRecordRedis(key, tokens, now)
		return
	}
	bizTPMMem.record(key, tokens, now)
}

// RecordBusinessTPMUsage appends one settlement's usage to the token's and the
// tenant's TPM windows. tenantID may be "" — the token dimension still
// records. Designed to run inside a fire-and-forget goroutine on the
// settlement path: errors are logged, never propagated, and non-positive
// amounts (refunds) are skipped — the same contract as RecordCostSpikeWindow.
func RecordBusinessTPMUsage(tokenID int, tenantID string, tokens int) {
	if tokens <= 0 {
		return
	}
	now := BizTPMNow()
	if tokenID > 0 {
		bizTPMRecord(bizTPMTokenKeyPrefix+strconv.Itoa(tokenID), int64(tokens), now)
	}
	if tenantID != "" {
		bizTPMRecord(bizTPMTenantKeyPrefix+tenantID, int64(tokens), now)
	}
}

// bizTPMModelKey builds the (tenant, model) window key. The model name is
// used verbatim — it may itself contain ':' but the key is never parsed back,
// only matched exactly, so no escaping is needed.
func bizTPMModelKey(tenantID, model string) string {
	return bizTPMModelKeyPrefix + tenantID + ":" + model
}

// RecordBusinessTPMModelUsage appends one settlement's usage to the
// (tenant, model) TPM window — the model dimension counterpart of
// RecordBusinessTPMUsage, same fire-and-forget fail-soft contract. Skips when
// either dimension component is missing: without a tenant or a model name the
// usage cannot be attributed to a (tenant, model) limit.
func RecordBusinessTPMModelUsage(tenantID, model string, tokens int) {
	if tokens <= 0 || tenantID == "" || model == "" {
		return
	}
	bizTPMRecord(bizTPMModelKey(tenantID, model), int64(tokens), BizTPMNow())
}

// QueryBusinessTPMTokenWindow returns the token's settled usage inside the
// last minute plus the oldest in-window record's unix-milli timestamp (0 when
// the window is empty; callers derive Retry-After from it). A non-nil error
// means "backend unusable" — the middleware fails open on it.
func QueryBusinessTPMTokenWindow(ctx context.Context, tokenID int) (total int64, oldestMs int64, err error) {
	return bizTPMQuery(ctx, bizTPMTokenKeyPrefix+strconv.Itoa(tokenID))
}

// QueryBusinessTPMTenantWindow is QueryBusinessTPMTokenWindow for the tenant
// aggregate dimension.
func QueryBusinessTPMTenantWindow(ctx context.Context, tenantID string) (total int64, oldestMs int64, err error) {
	return bizTPMQuery(ctx, bizTPMTenantKeyPrefix+tenantID)
}

// QueryBusinessTPMModelWindow is QueryBusinessTPMTokenWindow for the
// (tenant, model) dimension.
func QueryBusinessTPMModelWindow(ctx context.Context, tenantID, model string) (total int64, oldestMs int64, err error) {
	return bizTPMQuery(ctx, bizTPMModelKey(tenantID, model))
}

func bizTPMQuery(ctx context.Context, key string) (total int64, oldestMs int64, err error) {
	now := BizTPMNow()
	if !common.RedisEnabled || common.RDB == nil {
		total, oldestMs = bizTPMMem.query(key, now)
		return total, oldestMs, nil
	}

	cutoff := strconv.FormatInt(now.UnixMilli()-BizTPMWindow.Milliseconds(), 10)
	if err := common.RDB.ZRemRangeByScore(ctx, key, "-inf", cutoff).Err(); err != nil {
		return 0, 0, fmt.Errorf("business tpm zremrangebyscore: %w", err)
	}
	members, err := common.RDB.ZRangeWithScores(ctx, key, 0, -1).Result()
	if err != nil {
		return 0, 0, fmt.Errorf("business tpm zrange: %w", err)
	}
	for _, m := range members {
		if s, ok := m.Member.(string); ok {
			total += parseBizTPMMember(s)
		}
	}
	if len(members) > 0 {
		oldestMs = int64(members[0].Score)
	}
	return total, oldestMs, nil
}
