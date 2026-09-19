package middleware

import (
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// GlobalV2RateLimit is the IP-keyed rate limit for the /api/v2 route group.
// Before this, /api/v2 (api-v2-router.go) ran CORS + body-size + identity
// middleware and nothing else — no rate limit reached it at all.
// GlobalWebRateLimit ("GW") is bound to the SPA document in SetWebRouter
// (web-router.go), and gin.Group() snapshots its parent's middleware chain at the
// moment a child group is created, so a sibling group (which is what
// /api/v2 is — see the mount comment beside
// apiV2.Use(middleware.GlobalV2RateLimit()) in api-v2-router.go) never
// inherits it.
//
// The bucket is keyed by source IP, not by user or session — so it is
// shared by EVERY caller behind one NAT egress: an enterprise office on one
// public IP, or a mobile carrier's CGNAT range, spends the same 600/180s
// budget across however many people (or devices) sit behind it. That is the
// first-customer scenario this cycle is about, not a hypothetical.
//
// Budget: default 600 requests / 180s. Console worst case is roughly 6 XHR
// on page load, plus Gateway polling (12/min), SystemTasks polling (4/min)
// and Log-tail polling (20/min) — about 108 requests per 180s window for
// ONE console session — so 600 leaves roughly 5x headroom over that
// steady-state single-session pattern, not over every session an office's
// shared IP might be running concurrently. This is a soft,
// operator-tunable budget, not a hard capacity ceiling: UAT runs its
// nightly e2e suite from a single IP and widens it via GLOBAL_V2_RATE_LIMIT
// (see deploy/k8s/r6-uat/deployment.yaml).
//
// This budget also covers the cross-repo consumers inside /api/v2, among
// them switch's GET /api/v2/switch/app/releases/latest, GET
// /api/v2/relays/recommended, GET /api/v2/switch/user/info and POST
// /api/v2/switch/user/topup (contracts.md's "newhub → switch" sections),
// and lutu's POST /api/v2/lutu/search plus its token/log calls
// (contracts.md's "lutu → newhub" section) — they share the same per-IP
// bucket as console traffic when they originate from the same address.
//
// A limit of 0 (or negative) means DISABLED — NOT unlimited, even though
// RELAY_MAX_CONCURRENT_PER_TENANT's "0 = unlimited" convention elsewhere in
// this codebase might suggest the opposite (see GLOBAL_V2_RATE_LIMIT's
// .env.example comment). To actually disable this limiter, set
// GLOBAL_V2_RATE_LIMIT_ENABLE=false. A duration of 0 or below is not
// guarded here: on the in-memory backend every window is then already
// expired and every request is admitted
// (TestGlobalV2RateLimit_NonPositiveDurationAdmitsEverything pins that).
//
// mark "GV" is its own bucket, deliberately distinct from GlobalAPIRateLimit's
// "GA" and GlobalWebRateLimit's "GW": TestGlobalV2RateLimit_TripsAtBudgetAndIsOwnBucket
// proves both directions — a burst against /api/v2 does not exhaust the
// budget shared with /api/* (the same IP can still call /api/x after /api/v2
// trips), and a burst against /api/* does not trip /api/v2 (the same IP can
// still call /api/v2 after /api/x trips).
func GlobalV2RateLimit() func(c *gin.Context) {
	if !common.GlobalV2RateLimitEnable || common.GlobalV2RateLimitNum <= 0 {
		return defNext
	}
	return rateLimitFactory(common.GlobalV2RateLimitNum, common.GlobalV2RateLimitDuration, "GV")
}
