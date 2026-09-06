package router

// pg_chain_mount_test.go — L1: the /pg playground group must carry the same
// six enforcement gates every other billed relay route (/v1) carries before
// Distribute (PoolBalanceCheck, CostSpikeLimit, EntitlementCheck,
// ModelRequestRateLimit, BusinessRateLimit, RelayConcurrencyLimit). Before
// this fix the group mounted only PlaygroundAuth()+Distribute() — none of
// those six gates applied to playground traffic at all.
//
// This drives the REAL engine built by SetRelayRouter and inspects the
// actual assembled gin handler chain via gin.Context.HandlerNames(), which
// reflects every handler gin combined for the matched route at dispatch
// time (group Use() calls plus the route's own handlers) — not a
// hand-copied guess at what relay-router.go registers. gin.RouteInfo itself
// only exposes the LAST handler's name (gin.go's RouteInfo.Handler), so a
// probe middleware registered FIRST (via engine.Use, before SetRelayRouter
// adds its own global middlewares) is the only way to see the whole chain
// without reflecting into gin's unexported route trees.
//
// Assertions match on name SUFFIXES, not exact strings: gin reports closure
// names as "<pkg>.PoolBalanceCheck.func1", so a substring/Contains check is
// what survives that mangling.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const pgChainPath = "/pg/chat/completions"

// pgChainProbe registers a capturing middleware ahead of SetRelayRouter's own
// registrations and returns a function that, after one request to path, hands
// back the full ordered handler-name chain gin assembled for it.
func pgChainProbe(t *testing.T) (engine *gin.Engine, capture func(path string) []string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine = gin.New()

	var captured []string
	engine.Use(func(c *gin.Context) {
		captured = c.HandlerNames()
		c.Abort() // never actually run PlaygroundAuth/Distribute — no DB wired for this test
		c.String(http.StatusOK, "probed")
	})

	SetRelayRouter(engine)

	capture = func(path string) []string {
		captured = nil
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return captured
	}
	return engine, capture
}

// pgIndexOfSuffix returns the first index whose handler name contains
// substr, or -1 if none does.
func pgIndexOfSuffix(names []string, substr string) int {
	for i, n := range names {
		if strings.Contains(n, substr) {
			return i
		}
	}
	return -1
}

func TestSetRelayRouter_PlaygroundChain_CarriesFullEnforcementGates(t *testing.T) {
	engine, capture := pgChainProbe(t)

	// Fail fast if the walk finds zero playground routes — otherwise a typo'd
	// path below would make every assertion vacuously pass.
	found := false
	for _, rt := range engine.Routes() {
		if rt.Method == http.MethodPost && strings.Contains(rt.Path, "/pg/") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no POST /pg/... route registered by SetRelayRouter — scan target does not exist")
	}

	names := capture(pgChainPath)
	if len(names) == 0 {
		t.Fatalf("captured zero handler names for %s — probe wiring broken", pgChainPath)
	}

	distributeIdx := pgIndexOfSuffix(names, "Distribute")
	if distributeIdx < 0 {
		t.Fatalf("Distribute not found in playground chain: %v", names)
	}

	gates := []string{
		"PoolBalanceCheck",
		"CostSpikeLimit",
		"EntitlementCheck",
		"ModelRequestRateLimit",
		"BusinessRateLimit",
		"RelayConcurrencyLimit",
	}
	for _, gate := range gates {
		idx := pgIndexOfSuffix(names, gate)
		if idx < 0 {
			t.Errorf("%s not mounted on the /pg playground chain at all: %v", gate, names)
			continue
		}
		if idx >= distributeIdx {
			t.Errorf("%s mounted at index %d, AFTER Distribute (index %d) — must run before channel selection: %v", gate, idx, distributeIdx, names)
		}
	}
}
