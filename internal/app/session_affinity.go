package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// Session affinity — keep a multi-turn conversation on the channel that served
// its first turn.
//
// WHY: every upstream that supports prompt caching (Anthropic, OpenAI, Gemini,
// DeepSeek) keys its cache per account/endpoint. Round-robining turn N+1 of a
// long agent conversation onto a different channel throws away a cache that the
// user already paid to populate — the re-read costs full input price and adds
// seconds of TTFT. Weighted-random selection is right for INDEPENDENT requests
// and wrong for a conversation.
//
// SAFETY MODEL: affinity only reorders a choice among channels that are ALREADY
// eligible (repo.GetSatisfiedChannelByID re-checks group+model+enabled on every
// hit). It never widens reach, never resurrects a disabled channel, and never
// survives a failover. Every failure path — no Redis, malformed record, expired
// binding, ineligible channel — falls through to normal weighted selection.
//
// SCOPE: bindings are (caller, group, model) scoped and HMAC-hashed, so a
// session id guessed or reused by another tenant cannot read or steer someone
// else's binding, and the raw id never lands in Redis.
const (
	affinityRedisPrefix = "session_affinity:"
	// A conversation that has been idle for an hour has almost certainly lost
	// its upstream prompt cache anyway (vendor TTLs are 5m–1h), so holding the
	// binding longer only costs load-balance quality.
	affinityDefaultTTLSeconds = 3600
	// Bound the no-Redis fallback map. Affinity is an optimisation: dropping
	// bindings under pressure degrades to today's behaviour, whereas an
	// unbounded map is an OOM in a gateway that sees unique session ids.
	affinityMemMaxEntries = 50000
)

// SessionAffinityEnabled gates the whole feature. Default on: the safety model
// above means the worst case is a slightly less even load spread.
func SessionAffinityEnabled() bool {
	return common.GetEnvOrDefaultBool("SESSION_AFFINITY_ENABLED", true)
}

func affinityTTL() time.Duration {
	secs := common.GetEnvOrDefault("SESSION_AFFINITY_TTL", affinityDefaultTTLSeconds)
	if secs <= 0 {
		return time.Duration(affinityDefaultTTLSeconds) * time.Second
	}
	return time.Duration(secs) * time.Second
}

// DeriveSessionAffinityKey extracts a caller-stable conversation identifier and
// returns it hashed, or "" when the request carries no usable identifier (the
// common case for one-shot API calls — those keep pure weighted selection).
//
// Sources, highest precedence first. All three are stable for the LIFETIME of a
// conversation; per-request ids (x-request-id, previous_response_id) are
// deliberately NOT used — they would mint a new binding every turn, which is
// all of the bookkeeping and none of the cache benefit.
//
//  1. X-Session-Id header — explicit, operator/SDK controlled.
//  2. prompt_cache_key (OpenAI) — vendor field whose documented purpose is
//     exactly this: steering requests that share a prompt prefix.
//  3. metadata.user_id (Anthropic Messages) — coarser (user, not conversation)
//     but the only stable handle the Messages API offers.
func DeriveSessionAffinityKey(c *gin.Context, request dto.Request) string {
	if c == nil || !SessionAffinityEnabled() {
		return ""
	}

	raw := strings.TrimSpace(c.GetHeader("X-Session-Id"))
	if raw == "" {
		raw = extractRequestAffinityID(request)
	}
	if raw == "" {
		return ""
	}
	// Cap absurd values before hashing — a caller should not be able to make us
	// hash a megabyte per request.
	if len(raw) > 512 {
		raw = raw[:512]
	}

	// Scope: token id (falls back to user id) isolates callers; group+model keep
	// a binding from being reused for a model the pinned channel cannot serve.
	scope := strconv.Itoa(c.GetInt("token_id")) + "|" +
		strconv.Itoa(c.GetInt("id")) + "|" +
		c.GetString("group") + "|" +
		common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

	return common.GenerateHMAC(scope + "|" + raw)
}

// extractRequestAffinityID pulls a stable conversation id out of the parsed
// request body. Unknown request shapes yield "" (no affinity, no error).
func extractRequestAffinityID(request dto.Request) string {
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		return strings.TrimSpace(r.PromptCacheKey)
	case *dto.OpenAIResponsesRequest:
		// Typed as json.RawMessage here; accept only a JSON string.
		var s string
		if len(r.PromptCacheKey) > 0 && json.Unmarshal(r.PromptCacheKey, &s) == nil {
			return strings.TrimSpace(s)
		}
	case *dto.ClaudeRequest:
		var meta dto.ClaudeMetadata
		if len(r.Metadata) > 0 && json.Unmarshal(r.Metadata, &meta) == nil {
			return strings.TrimSpace(meta.UserId)
		}
	}
	return ""
}

// affinityRecord is the stored binding. Group travels with the channel because
// auto-group selection can land a request in a group the token did not name,
// and re-validation must use the group the channel was actually chosen under.
type affinityRecord struct {
	ChannelID int
	Group     string
}

func encodeAffinity(r affinityRecord) string {
	return strconv.Itoa(r.ChannelID) + "|" + r.Group
}

func decodeAffinity(s string) (affinityRecord, bool) {
	id, group, ok := strings.Cut(s, "|")
	if !ok {
		return affinityRecord{}, false
	}
	channelID, err := strconv.Atoi(id)
	if err != nil || channelID <= 0 {
		return affinityRecord{}, false
	}
	return affinityRecord{ChannelID: channelID, Group: group}, true
}

// ---- storage: Redis when available, bounded in-process map otherwise ----

type affinityMemEntry struct {
	value   string
	expires time.Time
}

var (
	affinityMemMu sync.Mutex
	affinityMem   = make(map[string]affinityMemEntry)
)

func affinityLoad(c *gin.Context, key string) (affinityRecord, bool) {
	ttl := affinityTTL()

	if common.RedisEnabled {
		val, err := common.RedisGet(c.Request.Context(), affinityRedisPrefix+key)
		if err != nil || val == "" {
			return affinityRecord{}, false
		}
		rec, ok := decodeAffinity(val)
		if !ok {
			return affinityRecord{}, false
		}
		// Sliding window: an active conversation keeps its binding alive.
		// Best-effort — a failed refresh only shortens the binding's life.
		_ = common.RedisSet(c.Request.Context(), affinityRedisPrefix+key, val, ttl)
		return rec, true
	}

	affinityMemMu.Lock()
	defer affinityMemMu.Unlock()
	entry, ok := affinityMem[key]
	if !ok {
		return affinityRecord{}, false
	}
	if time.Now().After(entry.expires) {
		delete(affinityMem, key)
		return affinityRecord{}, false
	}
	rec, ok := decodeAffinity(entry.value)
	if !ok {
		return affinityRecord{}, false
	}
	entry.expires = time.Now().Add(ttl)
	affinityMem[key] = entry
	return rec, true
}

func affinityStore(c *gin.Context, key string, rec affinityRecord) {
	ttl := affinityTTL()
	value := encodeAffinity(rec)

	if common.RedisEnabled {
		if err := common.RedisSet(c.Request.Context(), affinityRedisPrefix+key, value, ttl); err != nil {
			logger.LogDebug(c, "session affinity store failed: %s", err.Error())
		}
		return
	}

	affinityMemMu.Lock()
	defer affinityMemMu.Unlock()
	if len(affinityMem) >= affinityMemMaxEntries {
		pruneAffinityMemLocked()
		if len(affinityMem) >= affinityMemMaxEntries {
			// Still full of live entries — skip the write rather than evict a
			// random victim. Losing a binding costs one cache miss.
			return
		}
	}
	affinityMem[key] = affinityMemEntry{value: value, expires: time.Now().Add(ttl)}
}

// pruneAffinityMemLocked drops expired entries. Caller holds affinityMemMu.
func pruneAffinityMemLocked() {
	now := time.Now()
	for k, v := range affinityMem {
		if now.After(v.expires) {
			delete(affinityMem, k)
		}
	}
}

// resetAffinityMemForTest clears the fallback map between tests.
func resetAffinityMemForTest() {
	affinityMemMu.Lock()
	defer affinityMemMu.Unlock()
	affinityMem = make(map[string]affinityMemEntry)
}

// lookupAffinityChannel resolves a stored binding to a channel that is STILL
// eligible for this request, or nil to fall back to weighted selection.
//
// Returns the group the channel was originally selected under, because callers
// use it as the effective group for the rest of the request (auto-group tokens
// can resolve to a different group than the token's own).
func lookupAffinityChannel(param *RetryParam, affinityKey string) (*repo.Channel, string) {
	rec, ok := affinityLoad(param.Ctx, affinityKey)
	if !ok {
		recordAffinityOutcome("miss")
		return nil, ""
	}

	channel, err := repo.GetSatisfiedChannelByID(rec.Group, param.ModelName, rec.ChannelID)
	if err != nil || channel == nil {
		// Channel was disabled, lost the model, or left the group since we pinned
		// it. Drop the binding so the next turn re-pins cleanly instead of paying
		// this lookup every time.
		recordAffinityOutcome("stale")
		logger.LogDebug(param.Ctx, "session affinity stale: channel #%d no longer serves %s in group %s",
			rec.ChannelID, param.ModelName, rec.Group)
		return nil, ""
	}

	recordAffinityOutcome("hit")
	logger.LogDebug(param.Ctx, "session affinity hit: channel #%d (group %s)", channel.Id, rec.Group)
	return channel, rec.Group
}

// recordAffinityOutcome keeps the counter names in one place.
func recordAffinityOutcome(result string) {
	metrics.RecordSessionAffinity(result)
}
