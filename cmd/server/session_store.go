package main

import (
	"strconv"
	"strings"

	"github.com/gin-contrib/sessions"
	sessionredis "github.com/gin-contrib/sessions/redis"
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

// newRedisSessionStore builds a Redis-backed gin session store from a
// REDIS_CONN_STRING-shaped URL. It resolves addr/password/db via
// parseRedisSessionTarget and connects on that db — NOT a hardcoded "0" —
// so the session store lands on the same Redis logical DB the rest of the
// deployment's REDIS_CONN_STRING points at (prod DB 2, UAT DB 3; see
// parseRedisSessionTarget's doc comment for why this matters). It returns
// the resolved addr and db alongside the store so the caller can log them,
// and a non-nil error if the underlying redis connection pool could not be
// created — the caller decides whether to fall back to a cookie store.
func newRedisSessionStore(redisURL string, secret []byte) (sessions.Store, string, int, error) {
	addr, password, db := parseRedisSessionTarget(redisURL)
	store, err := sessionredis.NewStoreWithDB(10, "tcp", addr, "", password, strconv.Itoa(db), secret)
	return store, addr, db, err
}
