package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// AffinityKeyResponseHeader carries the HMAC-derived affinity key (the same
// value affinityLoad/affinityStore key their binding by) back to the caller,
// but ONLY when the request carried a usable affinity source — a one-shot
// call with no session id gets no header at all, never an empty one. An
// operator or admin tool copies this value verbatim into
// DELETE /api/v2/admin/routing/affinity/:key to purge that one binding.
// Documented in docs/openapi/relay.json; exposed cross-origin via
// middleware.CORSExposedHeaders (L5, console-ux-30).
const AffinityKeyResponseHeader = "X-Lurus-Affinity-Key"

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
//
// Two callers derive the key for one request and must agree on it: the
// distributor middleware, before the first channel selection (the only
// selection that consults the pin — retries skip the lookup), from the header
// and the raw body's prompt_cache_key / metadata.user_id; and the relay
// handler, after the typed body is parsed, through this function. Both go
// through DeriveSessionAffinityKeyFromRaw with the same precedence
// (header, then body id) and the same scope, so the key the distributor
// stored is the key the handler echoes in the response header and the key an
// operator purges (TestDistribute_SessionAffinity_FirstSelectionUsesPin).
func DeriveSessionAffinityKey(c *gin.Context, request dto.Request) string {
	if c == nil || !SessionAffinityEnabled() {
		return ""
	}

	raw := strings.TrimSpace(c.GetHeader("X-Session-Id"))
	if raw == "" {
		raw = extractRequestAffinityID(request)
	}
	return DeriveSessionAffinityKeyFromRaw(c, raw)
}

// DeriveSessionAffinityKeyFromRaw scopes and hashes an already-extracted
// conversation id (see DeriveSessionAffinityKey for the sources) and sets the
// response header. An empty raw id yields "" and sets nothing.
func DeriveSessionAffinityKeyFromRaw(c *gin.Context, raw string) string {
	if c == nil || !SessionAffinityEnabled() {
		return ""
	}
	raw = strings.TrimSpace(raw)
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

	key := common.GenerateHMAC(scope + "|" + raw)
	// Set on every request that carries an affinity source, not only the one
	// that ends up pinning a channel — the header is "does this conversation
	// participate in affinity at all", not "did this specific turn hit".
	c.Header(AffinityKeyResponseHeader, key)
	return key
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
	case *dto.OpenAIResponsesCompactionRequest:
		// POST /v1/responses/compact (cycle-8 L6 repair-round finding B-F3):
		// same field, same JSON-string-only decode, as *dto.OpenAIResponsesRequest
		// above — a compact request carrying the same prompt_cache_key under
		// the same (token, user, group, model) scope must derive the SAME
		// affinity key as the plain /v1/responses request that started the
		// conversation, so a multi-turn compact call actually pins the
		// channel that produced the previous_response_id it is continuing.
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

	// A binding recorded before the caller's tenant is known to be resolved
	// (param.TenantID == "") is not re-checked here — same as the rest of this
	// file, an unresolved tenant means no tenant filtering. But once we DO
	// have a caller tenant, a pin that points at a channel owned by a
	// DIFFERENT, non-shared tenant must never be honoured: the whole point of
	// affinity is to bias the choice among channels the caller could already
	// reach, never to widen reach. Treat it exactly like any other
	// invalidation and fall through to normal (now tenant-filtered) selection.
	if param.TenantID != "" {
		owner := channel.TenantId
		if owner != "" && owner != "default" && owner != param.TenantID {
			recordAffinityOutcome("stale")
			logger.LogDebug(param.Ctx, "session affinity stale: channel #%d belongs to tenant %s, caller is tenant %s",
				channel.Id, owner, param.TenantID)
			return nil, ""
		}
	}

	recordAffinityOutcome("hit")
	logger.LogDebug(param.Ctx, "session affinity hit: channel #%d (group %s)", channel.Id, rec.Group)
	return channel, rec.Group
}

// recordAffinityOutcome keeps the counter names in one place. Besides the
// Prometheus counter, it keeps a plain in-process atomic triple: the admin
// routing panel (GET /api/v2/admin/routing/affinity, L5) reads THIS, not
// Prometheus, so it works identically whether or not a scrape pipeline is
// wired up. These three counters — and MemEntries — are per-replica
// in-process state, not cluster-wide: production runs 3 replicas behind one
// NodePort (deploy/k8s/r6-stage/deployment.yaml), so a single GET only sees
// whichever replica happened to answer it. Summing across replicas requires
// reading the Prometheus counter (`lurus_gateway_session_affinity_total`)
// instead. The UAT probe (1 replica, deploy/k8s/r6-uat/deployment.yaml) does
// not exercise this gap.
func recordAffinityOutcome(result string) {
	switch result {
	case "hit":
		affinityHitCount.Add(1)
	case "miss":
		affinityMissCount.Add(1)
	case "stale":
		affinityStaleCount.Add(1)
	}
	metrics.RecordSessionAffinity(result)
}

var (
	affinityHitCount   atomic.Int64
	affinityMissCount  atomic.Int64
	affinityStaleCount atomic.Int64
)

// resetAffinityCountersForTest zeroes the hit/miss/stale counters between
// tests, mirroring resetAffinityMemForTest.
func resetAffinityCountersForTest() {
	affinityHitCount.Store(0)
	affinityMissCount.Store(0)
	affinityStaleCount.Store(0)
}

// AffinityStats is the admin-facing snapshot of session-affinity behaviour:
// how often a pin was found and honoured (Hit), found nothing (Miss), or
// found a binding that was no longer eligible (Stale); MemEntries/Backend
// report which storage tier is actually live right now. Enabled/TTLSeconds
// mirror SessionAffinityEnabled()/affinityTTL() so an operator does not have
// to cross-reference env vars to know whether the feature is even live and
// how long a pin survives.
type AffinityStats struct {
	Enabled    bool   `json:"enabled"`
	TTLSeconds int    `json:"ttl_seconds"`
	Hit        int64  `json:"hit"`
	Miss       int64  `json:"miss"`
	Stale      int64  `json:"stale"`
	Backend    string `json:"backend"`     // "redis" or "memory"
	MemEntries int    `json:"mem_entries"` // affinityMem size; 0 and meaningless when Backend=="redis"
}

// AffinityStatsSnapshot reads the counters above plus the live fallback-map
// size. Never touches Redis — MemEntries is the bounded in-process map's own
// size; it and the hit/miss/stale counters are per-process (see
// GetAffinityStatsV2's doc comment), reported regardless of backend so an
// operator can see the fallback map itself drain to 0 (e.g. after a TTL
// sweep) even though RedisEnabled is only ever assigned at boot and cannot
// actually flip while a process is running.
func AffinityStatsSnapshot() AffinityStats {
	backend := "memory"
	if common.RedisEnabled {
		backend = "redis"
	}
	affinityMemMu.Lock()
	memEntries := len(affinityMem)
	affinityMemMu.Unlock()
	return AffinityStats{
		Enabled:    SessionAffinityEnabled(),
		TTLSeconds: int(affinityTTL().Seconds()),
		Hit:        affinityHitCount.Load(),
		Miss:       affinityMissCount.Load(),
		Stale:      affinityStaleCount.Load(),
		Backend:    backend,
		MemEntries: memEntries,
	}
}

// PurgeAffinityKey removes one binding by its HMAC key (the value the caller
// got back via AffinityKeyResponseHeader). found is true only if a binding
// actually existed and was removed. err is non-nil only for a genuine Redis
// failure — callers MUST NOT treat err!=nil as "not found": a Redis outage
// must surface to the caller as a failure, not be reported as a 404 that
// would lead an operator to (wrongly) conclude the pin is already gone
// (L5 repair, finding routing-resilience-limits-11#6/#20/#47).
func PurgeAffinityKey(c *gin.Context, key string) (found bool, err error) {
	if common.RedisEnabled {
		n, err := common.RDB.Del(c.Request.Context(), affinityRedisPrefix+key).Result()
		if err != nil {
			logger.LogDebug(c, "session affinity purge failed: %s", err.Error())
			return false, err
		}
		return n > 0, nil
	}

	affinityMemMu.Lock()
	defer affinityMemMu.Unlock()
	if _, ok := affinityMem[key]; !ok {
		return false, nil
	}
	delete(affinityMem, key)
	return true, nil
}

// affinityPurgeAllScanCount is the SCAN COUNT hint per round — a hint to
// Redis about how much server-side work to do per call, not a hard cap on
// total keys scanned. SCAN itself (unlike KEYS) never blocks the server for
// the duration of the whole keyspace; this just keeps each round small. A
// var, not a const, so tests can shrink it to force a real Redis keyspace to
// span multiple SCAN pages without seeding thousands of keys.
var affinityPurgeAllScanCount = 500

// affinityPurgeAllMaxRounds bounds total SCAN round-trips so a cursor bug or
// an adversarially huge keyspace cannot spin this call forever. Real
// affinity keyspaces are orders of magnitude smaller than what this allows.
// A var, not a const, so TestPurgeAllAffinity_RoundCapHit_ReportsIncomplete
// can force the cap to bite with a small, fast keyspace.
var affinityPurgeAllMaxRounds = 10000

// PurgeAllAffinity drops the session-affinity bindings it can reach on
// whichever backend is currently live within affinityPurgeAllMaxRounds SCAN
// round-trips. The Redis path uses bounded SCAN+UNLINK — never KEYS, which
// blocks the server for the size of the whole keyspace — matching the
// enterprise-acceptance requirement for this purge-all path. Returns the
// number of bindings removed and complete=true only if the scan actually
// reached cursor 0; complete=false (round cap hit, or a scan/unlink error)
// means bindings may remain even though purged rows were removed. The
// pre-repair version of this function silently reported success whenever
// the round cap was hit instead of surfacing the cutoff to the caller (L5
// repair round 3, finding routing-resilience-limits-13#6) — callers must
// not assume complete=true just because err==nil.
func PurgeAllAffinity(c *gin.Context) (purged int, complete bool, err error) {
	if !common.RedisEnabled {
		affinityMemMu.Lock()
		n := len(affinityMem)
		affinityMem = make(map[string]affinityMemEntry)
		affinityMemMu.Unlock()
		return n, true, nil
	}

	ctx := c.Request.Context()
	pattern := affinityRedisPrefix + "*"
	var cursor uint64
	for round := 0; round < affinityPurgeAllMaxRounds; round++ {
		keys, next, scanErr := common.RDB.Scan(ctx, cursor, pattern, int64(affinityPurgeAllScanCount)).Result()
		if scanErr != nil {
			return purged, false, scanErr
		}
		if len(keys) > 0 {
			if unlinkErr := common.RDB.Unlink(ctx, keys...).Err(); unlinkErr != nil {
				return purged, false, unlinkErr
			}
			purged += len(keys)
		}
		cursor = next
		if cursor == 0 {
			return purged, true, nil
		}
	}
	return purged, false, nil
}
