package handler

import (
	"errors"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	gsessions "github.com/gorilla/sessions"
)

// sessionRotatedAtKey records when the session id under this cookie was last
// minted. It has two jobs, and the second is the load-bearing one:
//
//  1. it is a readable artefact for an operator reading a session dump;
//  2. gin-contrib/sessions only writes a session its Session interface has
//     seen a Set/Delete/Clear on (Save() is a no-op on a "clean" session),
//     and assigning the underlying gorilla ID does NOT go through that
//     interface. Without a value write here the Save below would silently
//     do nothing and the rotation would be a no-op.
const sessionRotatedAtKey = "session_rotated_at"

// sessionRotationRevokeReason is the revoke_reason written onto the OLD
// session-registry row when a login rotates the id. Deliberately distinct
// from "logout" and "user_revoked": nobody signed out and nobody revoked
// anything — the row simply describes a session key that no longer exists,
// and a support reader asking "why did this device disappear" deserves the
// real answer. Fits users_sessions.revoke_reason (varchar(32)).
const sessionRotationRevokeReason = "rotated"

// errSessionRotationUnsupported is returned when the session object behind
// sessions.Default(c) is not the gin-contrib implementation and therefore
// exposes no way to reach the underlying gorilla session id.
var errSessionRotationUnsupported = errors.New("session store does not expose a rotatable session id")

// rotateUnsupportedOnce throttles the SysError for the case above to one per
// process: it is a deployment-shaped fact, not a per-request event, and a
// line per login would be the outage.
var rotateUnsupportedOnce sync.Once

// rotateSessionID mints a fresh session id for the caller's session,
// carrying the existing session values across, and destroys the store's
// record of the OLD id. Every login handler calls it BEFORE writing the
// authenticated identity into the session.
//
// Why: all three logins (OIDCCallback, ZitaBootstrap, BridgeExchange) wrote
// the user's identity into whatever session the incoming cookie already
// named, and redistore keeps the id it was handed (middleware/auth.go:104
// says so in as many words). So an attacker who can plant a session cookie
// in a victim's browser before the victim signs in — any XSS on a sibling
// *.lurus.cn host, a shared or kiosk machine, a stale cookie on a handed-on
// device — ends up holding a cookie that, after the victim logs in, is the
// victim's authenticated session. That is session fixation, and the fix is
// the same one it has always been: never keep a pre-authentication id past
// the authentication.
//
// The mechanics: gin-contrib/sessions' concrete type exposes the underlying
// *gorilla/sessions.Session, whose ID we blank; redistore's Save mints a new
// alphanumeric id whenever it sees an empty one (redistore.go:384-386) and
// writes the new cookie. The old Redis key is then deleted through
// redisDeleteSessionKey (v2_session_revoke.go), which is the single place
// in this package that knows the store's key prefix — deleting it here by
// hand-copying "session_" would be a second copy to keep in sync.
//
// Errors are returned, never swallowed: a caller that cannot rotate must
// decide for itself whether to continue (all three do, after logging), and
// the fallback for an unrecognised session type is Clear() — which costs the
// attacker the planted values even though the id survives.
//
// Out of scope by construction: POST /api/v2/bridge/exchange called WITHOUT
// a cookie (the usual e2e shape) rotates nothing, because there is no prior
// id to retire; and the switch/lutu service callers authenticate with a
// bearer token rather than a hub session cookie, so their response shapes
// are untouched by this change.
func rotateSessionID(c *gin.Context) error {
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

	// Nothing to retire: a cookie-backed store carries no id at all, and a
	// first-ever visit had none either. Guarding on "the id actually
	// changed" also keeps a store that ignored the blanking from deleting
	// the session it just wrote.
	newID := s.ID()
	if oldID == "" || oldID == newID {
		return nil
	}

	redisDeleteSessionKey(c, oldID)

	// The per-device registry (cycle-7, SESSION_REGISTRY_ENABLED) keys rows
	// by session id. The new id registers itself on the next authenticated
	// request (authHelper -> repo.UpsertUserSessionSeen); the old row would
	// otherwise linger as an active "device" that can never make a request
	// again, both inflating the session cap and lying to the sessions page.
	if repo.SessionRegistryEnabled() {
		if err := repo.RevokeUserSessionByKey(oldID, sessionRotationRevokeReason); err != nil {
			common.SysLog("session rotation: could not retire the old registry row for " + oldID + ": " + err.Error())
		}
	}
	return nil
}
