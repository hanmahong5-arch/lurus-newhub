package repo

// user_session.go — persistence + policy for the per-device session
// registry (L7, auth-security-08/26/29/20). Every write path in this file
// is gated by SessionRegistryEnabled(); the write CALLERS (authHelper,
// the /sessions handlers) are responsible for checking the flag before
// calling in — this file's own functions do not re-check it, so that the
// throttle/mutation-test oracles can drive them directly without needing
// the flag on.

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// sessionLastSeenThrottleWindow is how often a session's last_seen_at may be
// written. Chosen to match the console's own polling cadence order of
// magnitude — frequent enough that "last seen" stays meaningfully fresh,
// rare enough that 3 production replicas sharing one Redis guard cannot
// amplify into one write per request.
const sessionLastSeenThrottleWindow = 60 * time.Second

const sessionTouchThrottleKeyPrefix = "session_touch:"

// SessionRegistryEnabled reports whether the per-device session registry
// (registration in authHelper, list/revoke-by-id/revoke-others, the
// defence-in-depth revoked check) is active. Read fresh from the
// environment on every call — same "no caching, so an operator's env change
// takes effect on the next request, not the next restart" convention as
// CREDIT_POOL_RESET_MODE / TENANT_MODEL_ALLOWLIST_MODE. Default false: with
// the flag off every session endpoint behaves exactly as it did before this
// lane (single synthetic row, /current revoke only).
func SessionRegistryEnabled() bool {
	return os.Getenv("SESSION_REGISTRY_ENABLED") == "true"
}

// sessionMaxActivePerUser returns SESSION_MAX_ACTIVE_PER_USER, defaulting to
// 0 (unlimited) for unset/empty/negative/non-numeric values — a typo in the
// env must never silently cap every user's session count to zero.
func sessionMaxActivePerUser() int {
	v := os.Getenv("SESSION_MAX_ACTIVE_PER_USER")
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// sessionTouchFallback is the in-process last-touch guard used ONLY when
// Redis is unavailable (single-process dev/test deployments). Production
// and UAT always run with Redis, where ShouldThrottleSessionTouch instead
// uses a "SET NX EX 60" guard shared across all 3 replicas — a bare
// in-process map here would let each replica write its own "first sight"
// independently, amplifying the DB write rate by the replica count.
var sessionTouchFallback sync.Map // map[string]time.Time

// ShouldThrottleSessionTouch reports whether a last-seen touch for
// sessionKey should be SKIPPED because one already landed within the last
// 60s — from ANY of the (up to 3) production replicas, not just this
// process. true = skip the write. The Redis path is a single atomic SET NX
// EX: exactly one caller across every replica racing on the same key wins
// it inside the window; every other caller, on any replica, is told to skip
// — this is what keeps N independent replica instances from turning one
// user's request cadence into N times the DB write rate.
func ShouldThrottleSessionTouch(ctx context.Context, sessionKey string) bool {
	if common.RedisEnabled && common.RDB != nil {
		won, err := common.RDB.SetNX(ctx, sessionTouchThrottleKeyPrefix+sessionKey, "1", sessionLastSeenThrottleWindow).Result()
		if err == nil {
			return !won
		}
		// Redis reachable-but-erroring is rare and must fail OPEN (write
		// through) rather than silently stop tracking sessions — the
		// in-process fallback below is not a substitute for the shared
		// guard, so it is not used here either.
		return false
	}
	now := time.Now()
	if v, ok := sessionTouchFallback.Load(sessionKey); ok {
		if last, ok := v.(time.Time); ok && now.Sub(last) < sessionLastSeenThrottleWindow {
			return true
		}
	}
	sessionTouchFallback.Store(sessionKey, now)
	return false
}

// UpsertUserSessionSeen registers or touches one session-registry row. On
// the FIRST sight of sessionKey it creates the row (stamping ip/user_agent/
// auth_method/tenant_id) and enforces SESSION_MAX_ACTIVE_PER_USER; every
// later call within the throttle window is a no-op, and outside the window
// it only bumps last_seen_at — ip/user_agent are a first-sight snapshot, not
// tracked live (a session riding a mobile network's rotating IP should not
// spam updates). Callers (authHelper) MUST check SessionRegistryEnabled()
// themselves before calling in; this function does not gate on the flag so
// the throttle/cap oracles can drive it directly.
func UpsertUserSessionSeen(ctx context.Context, sessionKey string, userId int, tenantId, ip, userAgent, authMethod string) error {
	if sessionKey == "" {
		return nil
	}
	if ShouldThrottleSessionTouch(ctx, sessionKey) {
		return nil
	}
	now := common.GetTimestamp()

	var existing entity.UserSession
	res := DB.Where("session_key = ?", sessionKey).Limit(1).Find(&existing)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return DB.Model(&entity.UserSession{}).Where("id = ?", existing.Id).
			Update("last_seen_at", now).Error
	}

	row := &entity.UserSession{
		SessionKey: sessionKey,
		UserId:     userId,
		TenantId:   tenantId,
		IP:         truncateForColumn(ip, 45),
		UserAgent:  truncateForColumn(userAgent, 255),
		AuthMethod: truncateForColumn(authMethod, 32),
		CreatedAt:  now,
		LastSeenAt: now,
	}
	if err := DB.Create(row).Error; err != nil {
		// Another replica raced this exact session_key between our Find and
		// Create (both admitted the same brand-new cookie in the same
		// throttle window on different replicas) — treat as a touch, not an
		// error; the row it just created is authoritative.
		if isUniqueViolation(err) {
			return DB.Model(&entity.UserSession{}).Where("session_key = ?", sessionKey).
				Update("last_seen_at", now).Error
		}
		return err
	}

	enforceSessionCap(userId)
	return nil
}

// truncateForColumn bounds s to n bytes so a caller-supplied header (IP
// parse result, User-Agent) can never overflow the column width — Postgres
// errors on overflow instead of truncating, so this must happen in Go.
func truncateForColumn(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// enforceSessionCap revokes the oldest active sessions of userId beyond
// SESSION_MAX_ACTIVE_PER_USER (0 = unlimited, the default — no-op). Called
// only from the first-sight branch of UpsertUserSessionSeen: the cap is
// evaluated when a NEW device shows up, not on every touch of an existing
// one. Best-effort — a lookup/update failure here must never block the
// login this row belongs to.
func enforceSessionCap(userId int) {
	maxActive := sessionMaxActivePerUser()
	if maxActive <= 0 {
		return
	}
	var active []entity.UserSession
	if err := DB.Where("user_id = ? AND revoked_at = 0", userId).
		Order("last_seen_at DESC").Find(&active).Error; err != nil {
		return
	}
	if len(active) <= maxActive {
		return
	}
	now := common.GetTimestamp()
	for _, s := range active[maxActive:] {
		_ = DB.Model(&entity.UserSession{}).Where("id = ?", s.Id).Updates(map[string]any{
			"revoked_at":    now,
			"revoke_reason": entity.SessionRevokeReasonCapExceeded,
		}).Error
	}
}

// sessionActiveWindow bounds ListActiveUserSessions to sessions seen in the
// last 30 days — mirrors ListSessionsV2's existing request_count window
// (v2_sessions.go), so "active" means the same thing everywhere on this
// page.
const sessionActiveWindow = 30 * 24 * time.Hour

// ListActiveUserSessions returns userId's non-revoked sessions seen within
// the last 30 days, most-recently-seen first. Filters by UserId only, not
// TenantId: a user account belongs to exactly one tenant for its whole
// life in this system, so UserId is already a stricter isolation key than
// TenantId would add.
func ListActiveUserSessions(userId int) ([]entity.UserSession, error) {
	cutoff := common.GetTimestamp() - int64(sessionActiveWindow/time.Second)
	var rows []entity.UserSession
	err := DB.Where("user_id = ? AND revoked_at = 0 AND last_seen_at > ?", userId, cutoff).
		Order("last_seen_at DESC").Find(&rows).Error
	return rows, err
}

// GetUserSessionByID returns the raw row for any user — callers MUST check
// UserId ownership themselves and 404 (not 403) on mismatch, the same
// not-found-shaped IDOR pattern ConsumeTenantInvite/project.go use, so a
// cross-account probe cannot distinguish "not yours" from "does not exist".
func GetUserSessionByID(id int) (*entity.UserSession, error) {
	var row entity.UserSession
	res := DB.Where("id = ?", id).Limit(1).Find(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &row, nil
}

// RevokeUserSessionRow sets revoked_at/revoke_reason on one row by numeric
// id. Idempotent: revoking an already-revoked row is a harmless no-op (the
// revoked_at timestamp is NOT overwritten with a later value, preserving
// the original revoke time for audit).
func RevokeUserSessionRow(id int, reason string) error {
	return DB.Model(&entity.UserSession{}).
		Where("id = ? AND revoked_at = 0", id).
		Updates(map[string]any{
			"revoked_at":    common.GetTimestamp(),
			"revoke_reason": reason,
		}).Error
}

// RevokeUserSessionByKey is RevokeUserSessionRow's session_key-keyed twin,
// used by RevokeCurrentSessionV2 (which only knows its own session.ID(),
// not the numeric row id) and by the defence-in-depth admin path. A miss
// (no row for this key — e.g. cookie-store deployments, which never
// register one) is not an error.
func RevokeUserSessionByKey(sessionKey, reason string) error {
	if sessionKey == "" {
		return nil
	}
	return DB.Model(&entity.UserSession{}).
		Where("session_key = ? AND revoked_at = 0", sessionKey).
		Updates(map[string]any{
			"revoked_at":    common.GetTimestamp(),
			"revoke_reason": reason,
		}).Error
}

// RevokeOtherUserSessions revokes every active session of userId EXCEPT the
// one whose session_key is currentSessionKey, and returns the rows it
// revoked (so the caller can delete each one's Redis key too). Passing an
// empty currentSessionKey revokes ALL active sessions (used by the admin
// "revoke every session of this compromised user" path via
// RevokeAllUserSessions below, which is a thin wrapper over this).
func RevokeOtherUserSessions(userId int, currentSessionKey, reason string) ([]entity.UserSession, error) {
	q := DB.Where("user_id = ? AND revoked_at = 0", userId)
	if currentSessionKey != "" {
		q = q.Where("session_key <> ?", currentSessionKey)
	}
	var rows []entity.UserSession
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return rows, nil
	}
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.Id)
	}
	now := common.GetTimestamp()
	if err := DB.Model(&entity.UserSession{}).Where("id IN ?", ids).Updates(map[string]any{
		"revoked_at":    now,
		"revoke_reason": reason,
	}).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// RevokeAllUserSessions revokes every active session of userId (used by the
// admin "compromised account" runbook step — no "current" to preserve,
// since the acting admin is a different account entirely).
func RevokeAllUserSessions(userId int, reason string) ([]entity.UserSession, error) {
	return RevokeOtherUserSessions(userId, "", reason)
}

// IsUserSessionRevoked is authHelper's defence-in-depth lookup: it covers
// the window between a revoke's Redis DEL landing and a client's very next
// request racing in on the same (about-to-be-invalid) cookie, plus any
// future non-Redis session store that never gets a DEL at all. A miss
// (unregistered session, e.g. this row predates the flag) is NOT revoked —
// absence of evidence is not evidence of revocation.
func IsUserSessionRevoked(sessionKey string) (bool, error) {
	if sessionKey == "" {
		return false, nil
	}
	var row entity.UserSession
	res := DB.Where("session_key = ?", sessionKey).Limit(1).Find(&row)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	return row.RevokedAt > 0, nil
}

// MaskIP coarsens an IP address before it is ever serialised to a client:
// /24 for IPv4 (the last octet zeroed), /48 for IPv6 (the last 80 bits
// zeroed) — enough to distinguish "same rough network" devices in the
// sessions list without publishing an exact address a client-side script
// could log or exfiltrate. An unparsable input (or an already-masked/empty
// string) is returned as "" rather than echoed back raw — this function is
// the ONLY path ip reaches ListSessionsV2's response, so failing safe here
// means the whitelist test never needs to re-check its output.
func MaskIP(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	if v4 := parsed.To4(); v4 != nil {
		masked := v4.Mask(net.CIDRMask(24, 32))
		return masked.String()
	}
	v6 := parsed.To16()
	if v6 == nil {
		return ""
	}
	masked := v6.Mask(net.CIDRMask(48, 128))
	return masked.String()
}

// uaFamilyMarkers maps a case-insensitive substring of a User-Agent header
// to the coarse family name reported to the client. Checked in order —
// most browser UAs embed multiple engine tokens (e.g. Chrome UAs also
// contain "Safari"), so the FIRST identifying token wins. This is
// deliberately coarse (it answers "which browser/tool", not "which
// version") — precise UA parsing is out of scope; a family this list does
// not recognise falls back to "other", never to the raw string.
var uaFamilyMarkers = []struct {
	token, family string
}{
	{"edg/", "Edge"},
	{"edge/", "Edge"},
	{"chrome/", "Chrome"},
	{"crios/", "Chrome"},
	{"firefox/", "Firefox"},
	{"fxios/", "Firefox"},
	{"opr/", "Opera"},
	{"opera/", "Opera"},
	{"safari/", "Safari"},
	{"curl/", "curl"},
	{"okhttp", "OkHttp"},
	{"postman", "Postman"},
	{"python-requests", "python-requests"},
}

// UserAgentFamily returns a coarse browser/tool family for a User-Agent
// header, never the raw string — the raw UA (which can carry a build
// fingerprint) must never reach ListSessionsV2's response. Unrecognised or
// empty input returns "other" / "unknown" respectively, never "".
func UserAgentFamily(ua string) string {
	if ua == "" {
		return "unknown"
	}
	lower := strings.ToLower(ua)
	for _, m := range uaFamilyMarkers {
		if strings.Contains(lower, m.token) {
			return m.family
		}
	}
	return "other"
}
