package middleware

// Platform-root shadow check (report-only).
//
// Purpose: identity.lurus.cn is the cross-product source of truth for
// "platform admin" (accounts.is_platform_admin). newhub's local root
// (users.role >= common.RoleRootUser) is the global-operations layer and is
// semantically the same population. Before tying the two together, this
// middleware step only RECONCILES them: for every request that RootAuth has
// already admitted, it asks the platform whether the same person is a
// platform admin and logs the comparison. It never writes a response, never
// aborts, never touches keys on the gin context, so admission is identical
// with the switch on or off. The check runs synchronously on the request
// (bounded: 2s platform timeout, 60s per-user cache, so at most one short
// stall per root user per minute) rather than in a goroutine: a goroutine
// would outlive the request and read process globals (repo.DB, the identity
// URL) while tests or shutdown rewrite them (data race seen under -race).
//
// Result values (log line prefix "platform_root_shadow", grep-able):
//
//	match           local root AND platform is_platform_admin=true
//	mismatch        local root but platform says is_platform_admin=false
//	not_found       local root, platform has no account for the IDP subject
//	no_sub          no active user->IDP subject mapping locally
//	unavailable     platform unreachable / non-200 / bad body
//	not_configured  identity service URL empty
//	disabled        PLATFORM_ROOT_SHADOW=0|false
//
// Switch: env PLATFORM_ROOT_SHADOW (default on).
//
// Follow-up (separate PR, only after the logs reconcile to match): tighten
// RootAuth to  local role >= root  AND  platform is_platform_admin. admin
// (role 10, tenant administrator) is deliberately out of scope.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

const (
	shadowMatch         = "match"
	shadowMismatch      = "mismatch"
	shadowNotFound      = "not_found"
	shadowNoSub         = "no_sub"
	shadowUnavailable   = "unavailable"
	shadowNotConfigured = "not_configured"
	shadowDisabled      = "disabled"

	shadowCacheTTL      = 60 * time.Second
	shadowCacheMax      = 1024
	shadowLookupTimeout = 2 * time.Second
)

type shadowEntry struct {
	result  string
	expires time.Time
}

var (
	shadowMu    sync.Mutex
	shadowCache = map[int]shadowEntry{}
	shadowGroup singleflight.Group
)

func shadowEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PLATFORM_ROOT_SHADOW"))) {
	case "0", "false":
		return false
	}
	return true
}

func shadowCacheGet(userID int) (string, bool) {
	shadowMu.Lock()
	defer shadowMu.Unlock()
	e, ok := shadowCache[userID]
	if !ok || time.Now().After(e.expires) {
		return "", false
	}
	return e.result, true
}

func shadowCachePut(userID int, result string) {
	shadowMu.Lock()
	defer shadowMu.Unlock()
	if len(shadowCache) >= shadowCacheMax {
		shadowCache = map[int]shadowEntry{}
	}
	shadowCache[userID] = shadowEntry{result: result, expires: time.Now().Add(shadowCacheTTL)}
}

func shadowResetCache() {
	shadowMu.Lock()
	defer shadowMu.Unlock()
	shadowCache = map[int]shadowEntry{}
}

// shadowCheckPlatformRoot compares local root user userID with the platform
// roster and returns the result label. Results are cached per user for 60s
// and concurrent lookups are coalesced.
func shadowCheckPlatformRoot(ctx context.Context, userID int) string {
	if !shadowEnabled() {
		return shadowDisabled
	}
	if r, ok := shadowCacheGet(userID); ok {
		return r
	}
	v, _, _ := shadowGroup.Do(fmt.Sprintf("u%d", userID), func() (interface{}, error) {
		r := shadowCompute(ctx, userID)
		shadowCachePut(userID, r)
		return r, nil
	})
	return v.(string)
}

func shadowCompute(ctx context.Context, userID int) string {
	mapping, err := repo.GetUserMappingByLurusUserIDAnyTenant(userID)
	if err != nil || mapping == nil || mapping.IDPSubject == "" {
		common.SysLog(fmt.Sprintf("platform_root_shadow result=%s user_id=%d", shadowNoSub, userID))
		return shadowNoSub
	}
	sub := mapping.IDPSubject

	lctx, cancel := context.WithTimeout(ctx, shadowLookupTimeout)
	defer cancel()
	acct, lerr := common.LookupAccountByIDPSubject(lctx, sub)

	var result string
	switch {
	case errors.Is(lerr, common.ErrIdentityNotConfigured):
		result = shadowNotConfigured
	case lerr != nil:
		result = shadowUnavailable
	case acct == nil:
		result = shadowNotFound
	case acct.IsPlatformAdmin:
		result = shadowMatch
	default:
		result = shadowMismatch
	}
	if result == shadowMismatch || result == shadowNotFound {
		common.SysLog(fmt.Sprintf("platform_root_shadow result=%s user_id=%d sub=%s", result, userID, sub))
	} else {
		common.SysLog(fmt.Sprintf("platform_root_shadow result=%s user_id=%d", result, userID))
	}
	return result
}

// platformRootShadow runs the check for an already-admitted root request. It
// must stay inert with respect to the request: no response writes, no Abort,
// no c.Set; a panic inside the check is contained and only logged.
func platformRootShadow(c *gin.Context) {
	v, ok := c.Get("id")
	if !ok {
		return
	}
	var userID int
	switch t := v.(type) {
	case int:
		userID = t
	case int64:
		userID = int(t)
	default:
		return
	}
	func() {
		// The shadow must never be able to turn into a 500 for the root request.
		defer func() {
			if r := recover(); r != nil {
				common.SysLog(fmt.Sprintf("platform_root_shadow result=panic user_id=%d err=%v", userID, r))
			}
		}()
		shadowCheckPlatformRoot(context.Background(), userID)
	}()
}

// RootAuth admits role >= root exactly as authHelper would, then fires the
// report-only shadow check. Lives here (not auth.go) so the shadow and the
// only call site that uses it stay in one file.
func RootAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		if resolveSessionIdentity(c, common.RoleRootUser) {
			platformRootShadow(c)
			c.Next()
		}
	}
}
