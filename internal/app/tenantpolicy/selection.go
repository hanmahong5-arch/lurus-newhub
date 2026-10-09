package tenantpolicy

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// SelectionConfigKey is the tenant_configs.config_key holding the list a
// tenant admin narrowed their own tenant to. It rides the same generic
// tenant_configs table as ModelAllowlistConfigKey (no migration). The
// platform-set allow-list stays the ceiling: a selection can only shrink it.
const SelectionConfigKey = "models.tenant_selection"

var (
	selCacheMu sync.Mutex
	selCache   = map[string]cacheEntry{}
)

// LoadSelection returns the tenant's self-selected list with the same
// (list, configured, err) contract as LoadModelAllowlist, cached for cacheTTL.
func LoadSelection(tenantID string) ([]string, bool, error) {
	if tenantID == "" {
		return nil, false, nil
	}
	selCacheMu.Lock()
	if e, ok := selCache[tenantID]; ok && time.Now().Before(e.expiresAt) {
		selCacheMu.Unlock()
		return e.list, e.configured, e.err
	}
	selCacheMu.Unlock()

	list, configured, err := LoadSelectionUncached(tenantID)

	selCacheMu.Lock()
	selCache[tenantID] = cacheEntry{list: list, configured: configured, err: err, expiresAt: time.Now().Add(cacheTTL)}
	selCacheMu.Unlock()
	return list, configured, err
}

// LoadSelectionUncached reads the row directly (used by the admin GET/PUT so a
// replica never echoes a stale list back to the admin who just wrote it).
func LoadSelectionUncached(tenantID string) ([]string, bool, error) {
	if tenantID == "" {
		return nil, false, nil
	}
	var raw []string
	if err := repo.GetTenantConfigJSON(tenantID, SelectionConfigKey, &raw); err != nil {
		if errors.Is(err, repo.ErrTenantConfigNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return raw, true, nil
}

// InvalidateSelection drops the cached selection for tenantID.
func InvalidateSelection(tenantID string) {
	selCacheMu.Lock()
	delete(selCache, tenantID)
	selCacheMu.Unlock()
}

// Covered reports whether entry (exact name or trailing-"*" prefix) grants
// nothing beyond what ceiling grants. An exact entry is covered when the
// ceiling allows that model; a wildcard entry only when some ceiling wildcard
// is a prefix of it (so "gpt-4*" is covered by "gpt-*" but not by "gpt-4o").
func Covered(ceiling []string, entry string) bool {
	e := strings.TrimSpace(entry)
	if !strings.HasSuffix(e, "*") {
		return ModelAllowed(ceiling, e)
	}
	p := strings.TrimSuffix(e, "*")
	for _, c := range ceiling {
		c = strings.TrimSpace(c)
		if strings.HasSuffix(c, "*") && strings.HasPrefix(p, strings.TrimSuffix(c, "*")) {
			return true
		}
	}
	return false
}

// Effective is the intersection of the platform ceiling and the tenant's
// selection. Unconfigured platform = unrestricted; unconfigured selection =
// no further narrowing. Both unconfigured yields ["*"].
func Effective(platform []string, platformConfigured bool, selected []string, selConfigured bool) []string {
	switch {
	case !platformConfigured && !selConfigured:
		return []string{"*"}
	case platformConfigured && !selConfigured:
		return append([]string{}, platform...)
	case !platformConfigured:
		return append([]string{}, selected...)
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range selected {
		if Covered(platform, s) {
			add(s)
		}
	}
	for _, p := range platform {
		if Covered(selected, p) {
			add(p)
		}
	}
	return out
}
