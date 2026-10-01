package main

import (
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/boj/redistore"
	"github.com/gin-contrib/sessions"
)

// parseRedisSessionTarget parses REDIS_CONN_STRING (redis://[password@]host:port[/db])
// into the pieces sessionredis.NewStoreWithDB needs. The db path segment used
// to be parsed off and thrown away (addr/password only), so the session
// store always connected on Redis DB 0 no matter what REDIS_CONN_STRING said
// — silently colliding with whatever else lived on DB 0 while the rest of
// the app (channel cache, quota sync) correctly followed the configured DB
// (SESS-DB). db defaults to 0 when the URL carries no /<n> path segment.
func parseRedisSessionTarget(redisURL string) (addr, password string, db int) {
	addr = "127.0.0.1:6379"
	if !strings.HasPrefix(redisURL, "redis://") {
		return addr, password, db
	}
	parsed := strings.TrimPrefix(redisURL, "redis://")
	// Handle redis://password@host:port[/db] or redis://host:port[/db]
	if atIdx := strings.LastIndex(parsed, "@"); atIdx >= 0 {
		password = parsed[:atIdx]
		addr = parsed[atIdx+1:]
	} else {
		addr = parsed
	}
	if slashIdx := strings.Index(addr, "/"); slashIdx >= 0 {
		if n, err := strconv.Atoi(addr[slashIdx+1:]); err == nil {
			db = n
		}
		addr = addr[:slashIdx]
	}
	return addr, password, db
}

// redisSessionStore is gin-contrib/sessions/redis's store minus its
// constructor's "ping failed => no store" rule. redistore builds a lazily
// dialling pool and pings once; the gin-contrib wrapper throws the store away
// when that ping fails, so a pod that boots during a Redis blip used to fall
// back to a cookie store for its whole life while its siblings kept Redis
// sessions — every other request bounced between two session formats. Keeping
// the store means sessions fail while Redis is down (exactly like a Redis
// outage on an already-running pod) and work again once it is back.
type redisSessionStore struct {
	*redistore.RediStore
}

func (s *redisSessionStore) Options(options sessions.Options) {
	s.RediStore.Options = options.ToGorillaOptions()
}

// newRedisSessionStore builds a Redis-backed gin session store from a
// REDIS_CONN_STRING-shaped URL. It resolves addr/password/db via
// parseRedisSessionTarget and connects on that db — NOT a hardcoded "0" —
// so the session store lands on the same Redis logical DB the rest of the
// deployment's REDIS_CONN_STRING points at (prod DB 2, UAT DB 3; see
// parseRedisSessionTarget's doc comment for why this matters). It returns
// the resolved addr and db alongside the store so the caller can log them.
// The store is always usable; a non-nil error only means Redis did not answer
// the initial ping (see redisSessionStore).
func newRedisSessionStore(redisURL string, secret []byte) (sessions.Store, string, int, error) {
	addr, password, db := parseRedisSessionTarget(redisURL)
	rs, err := redistore.NewRediStoreWithDB(10, "tcp", addr, "", password, strconv.Itoa(db), secret)
	// Say the Redis key prefix out loud instead of inheriting boj/redistore's
	// default: three other places delete session keys by name
	// (middleware.deleteStoreSessionKey, handler.redisDeleteSessionKey,
	// repo.deleteCappedSessionKeys) and they build the key from
	// common.SessionStoreKeyPrefix. Same value as the default today, so this
	// changes no behaviour; it makes the agreement checkable (see
	// internal/adapter/middleware/session_identity_write_sites_test.go).
	rs.SetKeyPrefix(common.SessionStoreKeyPrefix)
	return &redisSessionStore{rs}, addr, db, err
}
