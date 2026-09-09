// Package tenantpolicy holds the per-tenant model allow-list (Bifrost
// virtual-key `allowed_models` / LiteLLM team-models vocabulary): a root
// operator can restrict which models a tenant's relay callers may reach,
// observe-first with a typed 403 under enforce. It is a sibling of
// internal/app (not that package itself) because the five untouchable
// coverage_* files in internal/app must not be perturbed by this cycle's
// work.
package tenantpolicy

import (
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

// ModelAllowlistConfigKey is the tenant_configs.config_key this package
// reads and the admin endpoint writes. No migration: it rides the existing
// generic tenant_configs table (config_type json).
const ModelAllowlistConfigKey = "models.allowlist"

// ModeEnforce is the only TENANT_MODEL_ALLOWLIST_MODE value that enforces
// denial; anything else (unset, empty, "Enforce", garbage) observes.
const ModeEnforce = "enforce"

// ModeObserve is what Mode() returns for every non-enforce value.
const ModeObserve = "observe"

// modeEnvVar is read fresh on every Mode() call — same posture as
// creditPoolResetModeEnv (internal/app/credit_pool_reset.go) — so an
// operator's env change takes effect on the next request, not the next
// restart.
const modeEnvVar = "TENANT_MODEL_ALLOWLIST_MODE"

// cacheTTL bounds how long a replica can keep serving a stale allow-list
// after the admin endpoint writes a new one on a different replica. The PUT
// handler reports this to the caller as propagation_seconds.
const cacheTTL = 30 * time.Second

type cacheEntry struct {
	list       []string
	configured bool
	err        error
	expiresAt  time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cacheEntry{}
)

// LoadModelAllowlist returns the tenant's configured allow-list.
//
//   - No row for the tenant (never configured): (nil, false, nil) — the
//     caller treats this as unrestricted, not a fault.
//   - A row exists: (list, true, nil), where an empty list means deny-all
//     (the caller distinguishes "unrestricted" from "restricted to nothing"
//     by the configured bool, not by len(list)).
//   - Any other read failure (DB down, malformed JSON): (nil, false, err) —
//     the caller fails OPEN and logs, the same posture the rate limiters use
//     on a degraded backend.
//
// Results are cached per tenant for cacheTTL; Invalidate drops the entry so
// the admin endpoint's own write is visible on ITS replica immediately (other
// replicas still converge within the TTL).
func LoadModelAllowlist(tenantID string) ([]string, bool, error) {
	if tenantID == "" {
		return nil, false, nil
	}

	cacheMu.Lock()
	if e, ok := cache[tenantID]; ok && time.Now().Before(e.expiresAt) {
		cacheMu.Unlock()
		return e.list, e.configured, e.err
	}
	cacheMu.Unlock()

	list, configured, err := loadFromRepo(tenantID)

	cacheMu.Lock()
	cache[tenantID] = cacheEntry{list: list, configured: configured, err: err, expiresAt: time.Now().Add(cacheTTL)}
	cacheMu.Unlock()

	return list, configured, err
}

func loadFromRepo(tenantID string) ([]string, bool, error) {
	var raw []string
	err := repo.GetTenantConfigJSON(tenantID, ModelAllowlistConfigKey, &raw)
	if err != nil {
		if errors.Is(err, repo.ErrTenantConfigNotFound) {
			// Unconfigured is not a fault: the tenant simply has no
			// allow-list row yet. A nil error here is load-bearing — the
			// middleware's fail-open log branch must never fire for the
			// overwhelmingly common "never configured" case.
			return nil, false, nil
		}
		return nil, false, err
	}
	return raw, true, nil
}

// LoadModelAllowlistUncached bypasses the per-replica TTL cache and reads
// the row directly. Callers that must see their own just-completed write
// immediately — the admin GET handler, which can be served by a different
// process/goroutine than the PUT within the same request lifecycle in tests,
// and in production must never echo a stale pre-write list back to the
// operator who just set it — use this instead of LoadModelAllowlist. The
// relay path (distributor.go) intentionally keeps using the cached call:
// a 30s-stale allow-list on the hot path is the documented trade-off.
func LoadModelAllowlistUncached(tenantID string) ([]string, bool, error) {
	if tenantID == "" {
		return nil, false, nil
	}
	return loadFromRepo(tenantID)
}

// Invalidate drops the cached entry for tenantID, forcing the next
// LoadModelAllowlist call on this replica to re-read the row. Called by the
// admin endpoint after every PUT/DELETE.
func Invalidate(tenantID string) {
	cacheMu.Lock()
	delete(cache, tenantID)
	cacheMu.Unlock()
}

// ModelAllowed reports whether model is permitted by list. Each list entry
// is either an exact model name or a prefix ending in "*"; model is
// normalised with ratio_setting.FormatMatchingModelName first — the same
// normalisation the token-level model gate applies (distributor.go) — so a
// caller requesting "gemini-2.5-flash-thinking-4096" matches an allow-list
// entry written against the base "gemini-2.5-flash-thinking-*" family
// exactly the way the token gate would. An empty list denies every model.
func ModelAllowed(list []string, model string) bool {
	normalized := ratio_setting.FormatMatchingModelName(model)
	for _, entry := range list {
		e := strings.TrimSpace(entry)
		if e == "" {
			continue
		}
		if strings.HasSuffix(e, "*") {
			if strings.HasPrefix(normalized, strings.TrimSuffix(e, "*")) {
				return true
			}
			continue
		}
		if e == normalized {
			return true
		}
	}
	return false
}

// Mode reads TENANT_MODEL_ALLOWLIST_MODE fresh on every call — no caching —
// so a config change takes effect on the next request without a restart.
// Only the literal "enforce" enforces; everything else observes.
func Mode() string {
	if os.Getenv(modeEnvVar) == ModeEnforce {
		return ModeEnforce
	}
	return ModeObserve
}
