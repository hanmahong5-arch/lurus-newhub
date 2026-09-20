package common

// SessionStoreKeyPrefix is the prefix the browser session store puts in front
// of a session id when it writes that session to Redis, i.e. a live session
// with id <sid> lives at Redis key SessionStoreKeyPrefix+<sid>.
//
// The value is boj/redistore's own default (redistore.go: keyPrefix
// "session_"), which is what cmd/server/main.go's store runs with today.
// It lives here because four places need to agree on it and they sit in
// packages that cannot import each other:
//
//	cmd/server/session_store.go              — builds the store (package main)
//	middleware.deleteStoreSessionKey         — drops the id a login retired
//	handler.redisDeleteSessionKey            — drops a remotely revoked id
//	repo.deleteCappedSessionKeys             — drops ids evicted by the cap
//
// internal/pkg/common is the one package all four already import, so this is
// the constant they can share instead of a literal each. Changing it here
// changes where the deleters look; it changes where the STORE writes only
// once the store is told about it (sessionredis.SetKeyPrefix in
// cmd/server/session_store.go), so the two must move together — the drift
// gate in internal/adapter/middleware/session_identity_write_sites_test.go
// fails when a copy that still spells the prefix by hand disagrees with this
// value.
const SessionStoreKeyPrefix = "session_"
