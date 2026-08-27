package setting

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ModelRequestRateLimitEnabled arms the per-user relay ceiling read by
// ModelRequestRateLimit (internal/adapter/middleware/model-rate-limit.go:256).
// It is not the only reader: repo/option.go:124 also reads it, to publish the
// value into common.OptionMap for /api/option. It was `false` out of the box,
// which is a live-confirmed gap (2026-08-26 UAT G6): six live tokens all had
// rpm_limit/tpm_limit=0 (the SEPARATE per-token/per-tenant limiter in
// middleware/business_rate_limit.go, gated at :321/:328/:340/:347), and this
// switch being off meant there was no ceiling at all — a leaked key could
// burn upstream quota at unlimited rate.
//
// Now `true`, with ModelRequestRateLimitCount left at 0 and
// ModelRequestRateLimitSuccessCount left at 1000, what actually gets armed
// depends on the backend, and the two backends do NOT arm the same
// dimension:
//   - Redis backend (common.RedisEnabled): exactly the SUCCESSFUL-request
//     dimension — at most 1000 requests with response status<400 per user per
//     ModelRequestRateLimitDurationMinutes (~16.7 rps sustained). See
//     checkRedisRateLimit (model-rate-limit.go:73-77), which treats count==0
//     ("TOTAL count" branch) as "dimension skipped", and recordRedisRequest
//     (model-rate-limit.go:113-123 for the definition, called only from within
//     the `c.Writer.Status() < 400` guard at model-rate-limit.go:212-213).
//   - Memory backend (RedisEnabled=false; the only backend for on-prem
//     deployments without Redis): the "success" check is a *_check key that
//     memoryRateLimitHandler (model-rate-limit.go:237-242) increments on EVERY
//     request regardless of outcome — inMemoryRateLimiter.Request records the
//     attempt as soon as it is allowed through, before c.Next() runs, so
//     failed requests consume the same budget as successful ones. Measured
//     2026-08-26: 3 requests to a handler that always fails, with
//     successMaxCount=2, yield req1=500 req2=500 req3=429 — zero successful
//     requests, yet the limiter tripped on total volume.
//
// The Redis-backend TOTAL-request dimension (including failed requests) stays
// opt-in via the admin option ModelRequestRateLimitCount (plumbed through
// repo/option.go:410-415); per-group overrides for both numbers are read via
// GetGroupRateLimit below and applied at model-rate-limit.go:276-280.
//
// Coverage gap this does NOT close: the /mj, /task and audio-music route
// groups (registerMjRouterGroup, relay-router.go) never mount
// ModelRequestRateLimit at all, so this ceiling only covers the /v1 and
// /v1beta chains (relay-router.go:81, :210).
var ModelRequestRateLimitEnabled = true
var ModelRequestRateLimitDurationMinutes = 1
var ModelRequestRateLimitCount = 0
var ModelRequestRateLimitSuccessCount = 1000
var ModelRequestRateLimitGroup = map[string][2]int{}
var ModelRequestRateLimitMutex sync.RWMutex

func ModelRequestRateLimitGroup2JSONString() string {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	jsonBytes, err := json.Marshal(ModelRequestRateLimitGroup)
	if err != nil {
		common.SysLog("error marshalling model ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateModelRequestRateLimitGroupByJSONString(jsonStr string) error {
	ModelRequestRateLimitMutex.Lock()
	defer ModelRequestRateLimitMutex.Unlock()

	ModelRequestRateLimitGroup = make(map[string][2]int)
	return json.Unmarshal([]byte(jsonStr), &ModelRequestRateLimitGroup)
}

func GetGroupRateLimit(group string) (totalCount, successCount int, found bool) {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	if ModelRequestRateLimitGroup == nil {
		return 0, 0, false
	}

	limits, found := ModelRequestRateLimitGroup[group]
	if !found {
		return 0, 0, false
	}
	return limits[0], limits[1], true
}

func CheckModelRequestRateLimitGroup(jsonStr string) error {
	checkModelRequestRateLimitGroup := make(map[string][2]int)
	err := json.Unmarshal([]byte(jsonStr), &checkModelRequestRateLimitGroup)
	if err != nil {
		return err
	}
	for group, limits := range checkModelRequestRateLimitGroup {
		if limits[0] < 0 || limits[1] < 1 {
			return fmt.Errorf("group %s has negative rate limit values: [%d, %d]", group, limits[0], limits[1])
		}
		if limits[0] > math.MaxInt32 || limits[1] > math.MaxInt32 {
			return fmt.Errorf("group %s [%d, %d] has max rate limits value 2147483647", group, limits[0], limits[1])
		}
	}

	return nil
}
