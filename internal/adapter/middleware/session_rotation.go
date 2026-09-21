package middleware

import (
	"errors"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	gsessions "github.com/gorilla/sessions"
)

// sessionRotatedAtKey records when the session id under this cookie was last
// minted. It has two jobs, and the second is the load-bearing one:
//
//  1. it is a readable artefact for an operator reading a session dump;
//  2. gin-contrib/sessions writes a session its Session interface has seen a
//     Set/Delete/Clear on (Save() is a no-op on a "clean" session), and
//     assigning the underlying gorilla ID does NOT go through that
//     interface. Without a value write here the Save below would do nothing
//     and the rotation would be a no-op.
const sessionRotatedAtKey = "session_rotated_at"

// sessionRotationDoneKey is a gin-context (per-request) marker, not a session
// value: it says "this request already minted a fresh id". Two authenticating
// middlewares on one chain, or a handler that runs behind UserAuth and
// rotates again, would otherwise mint a second id and delete the first: each
// rotation costs a Redis write plus a Set-Cookie, and the browser keeps only
// the last one. See TestRotateSessionID_AtMostOncePerRequest.
const sessionRotationDoneKey = "lurus_session_rotated"

// errSessionRotationUnsupported is returned when the session object behind
// sessions.Default(c) is not the gin-contrib implementation and therefore
// exposes no way to reach the underlying gorilla session id.
var errSessionRotationUnsupported = errors.New("session store does not expose a rotatable session id")

// rotateUnsupportedOnce throttles the SysError for the case above to one per
// process: it is a deployment-shaped fact, not a per-request event, and a
// line per login would be the outage.
var rotateUnsupportedOnce sync.Once

// RotateSessionID mints a fresh session id for the caller's session,
// carrying the existing session values across, and destroys the store's
// record of the OLD id. It is called before an authenticated identity is
// written into a browser session, at the four places that do that write:
//
//	handler.OIDCCallback   (oauth.go)          — GET  /api/v2/oauth/callback
//	handler.ZitaBootstrap  (zita_bootstrap.go) — POST /api/v2/auth/zita-bootstrap
//	handler.BridgeExchange (v2_bridge.go)      — POST /api/v2/bridge/exchange
//	resolveSessionIdentity (auth.go)           — the SDK-bridge self-heal arm
//
// That list is not maintained by hand:
// session_identity_write_sites_test.go parses both packages and fails when
// an identity write does not call RotateSessionID earlier in the same
// function. It recognises the two shapes this code base writes —
// `s.Set("id", …)` on a value bound from sessions.Default(c), and
// `sessions.Default(c).Set("id", …)` written inline — and reports a receiver
// or a key it cannot resolve as a failure rather than skipping it, so a
// fifth site has to be written in a shape nobody here uses AND get past the
// unclassifiable arm to land unrotated.
//
// Why: each of those sites wrote the user's identity into whatever session
// the incoming cookie already named, and redistore keeps the id it was
// handed (auth.go's revoked-session branch says so in as many words). So an
// attacker who can plant a session cookie in a victim's browser before the
// victim authenticates — any XSS on a sibling *.lurus.cn host, a shared or
// kiosk machine, a stale cookie on a handed-on device — ends up holding a
// cookie that, once the victim signs in, is the victim's authenticated
// session. That is session fixation, and this is its standard remedy: do not
// keep a pre-authentication id past the authentication.
//
// The mechanics: gin-contrib/sessions' concrete type exposes the underlying
// *gorilla/sessions.Session, whose ID we blank; redistore's Save mints a new
// alphanumeric id whenever it sees an empty one (redistore.go:384-386) and
// writes the new cookie. The old Redis key is then deleted.
//
// The rotation error is returned rather than swallowed, so a caller that
// cannot rotate decides for itself whether to continue (the four above do,
// after logging); the fallback for an unrecognised session type is Clear(),
// which costs the attacker the planted values even though the id survives.
// The two cleanup steps AFTER the new id exists (Redis DEL, registry row)
// are best-effort and log instead: neither failure undoes the fresh id the
// browser was just handed.
//
// Scope, stated rather than implied:
//   - one rotation per request (sessionRotationDoneKey above); a second call
//     on the same *gin.Context returns nil without minting anything.
//   - POST /api/v2/bridge/exchange called WITHOUT a cookie (the usual e2e
//     shape) still calls RotateSessionID — the call is unconditional at the
//     three login sites — but there is no prior id to retire: the rotation's
//     own Set+Save mints the first id and the identity write's Save keeps it.
//   - the switch/lutu service callers authenticate with a bearer token
//     rather than a hub session cookie, so their response shapes are
//     untouched by this change.
//   - residue: several requests arriving CONCURRENTLY under the same planted
//     cookie each rotate, since they do not coordinate and each still sees
//     the planted id. The browser keeps the last cookie and the earlier new
//     ids linger in Redis until the store's TTL expires them. They do not
//     become registry rows on the SDK-bridge path: authHelper's
//     repo.UpsertUserSessionSeen call sits behind `!useAccessToken`, which
//     that arm sets. The planted id is deleted by each of them, which is the
//     property that matters here.
func RotateSessionID(c *gin.Context) error {
	if done, exists := c.Get(sessionRotationDoneKey); exists {
		if rotated, _ := done.(bool); rotated {
			return nil
		}
	}

	s := sessions.Default(c)

	holder, ok := s.(interface{ Session() *gsessions.Session })
	if !ok {
		s.Clear()
		rotateUnsupportedOnce.Do(func() {
			common.SysError("session rotation: the configured session store does not expose the underlying session id; " +
				"logins clear the pre-authentication session instead of rotating it, which leaves a planted cookie VALUE dead but its id alive")
		})
		return errSessionRotationUnsupported
	}

	gs := holder.Session()
	oldID := gs.ID
	gs.ID = ""
	s.Set(sessionRotatedAtKey, common.GetTimestamp())

	if err := s.Save(); err != nil {
		return err
	}
	c.Set(sessionRotationDoneKey, true)

	// Nothing to retire: a cookie-backed store carries no id at all, and a
	// first-ever visit had none either. Guarding on "the id actually
	// changed" also keeps a store that ignored the blanking from deleting
	// the session it just wrote.
	newID := s.ID()
	if oldID == "" || oldID == newID {
		return nil
	}

	deleteStoreSessionKey(c, oldID)

	// The per-device registry (cycle-7, SESSION_REGISTRY_ENABLED) keys rows
	// by session id. The new id registers itself on the next authenticated
	// request (authHelper -> repo.UpsertUserSessionSeen); the old row would
	// otherwise linger as an active "device" that cannot make a request
	// again, both inflating the session cap and lying to the sessions page.
	if repo.SessionRegistryEnabled() {
		if err := repo.RevokeUserSessionByKey(oldID, entity.SessionRevokeReasonRotated); err != nil {
			common.SysLog("session rotation: could not retire the old registry row for " + oldID + ": " + err.Error())
		}
	}
	return nil
}

// deleteStoreSessionKey removes the session store's own Redis key for
// sessionKey. Best-effort: a rotation whose DEL fails still handed the
// browser a new id, and the old key expires with the store's TTL.
func deleteStoreSessionKey(c *gin.Context, sessionKey string) {
	if sessionKey == "" || !common.RedisEnabled || common.RDB == nil {
		return
	}
	if err := common.RedisDel(c.Request.Context(), common.SessionStoreKeyPrefix+sessionKey); err != nil {
		common.SysLog("session rotation: RedisDel failed for " + common.SessionStoreKeyPrefix + sessionKey + ": " + err.Error())
	}
}
