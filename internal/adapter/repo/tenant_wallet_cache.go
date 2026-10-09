package repo

import (
	"sync"
	"time"
)

// tenantWalletCacheTTL bounds how long a replica may serve a stale
// wallet_authoritative flag after another replica flipped it. Writes through
// this process (UpdateTenant) invalidate immediately.
const tenantWalletCacheTTL = 30 * time.Second

type tenantWalletEntry struct {
	value   bool
	fetched time.Time
}

var (
	tenantWalletMu    sync.Mutex
	tenantWalletCache = map[string]tenantWalletEntry{}
	// tenantWalletNow is the clock seam for tests.
	tenantWalletNow = time.Now
)

// tenantWalletLookup returns the cached flag when it is younger than the TTL
// (fresh=true), or the last known value regardless of age (known=true) so a
// failing DB can fall back to it.
func tenantWalletLookup(tenantID string) (value, fresh, known bool) {
	tenantWalletMu.Lock()
	defer tenantWalletMu.Unlock()
	e, ok := tenantWalletCache[tenantID]
	if !ok {
		return false, false, false
	}
	return e.value, tenantWalletNow().Sub(e.fetched) < tenantWalletCacheTTL, true
}

func tenantWalletStore(tenantID string, v bool) {
	tenantWalletMu.Lock()
	tenantWalletCache[tenantID] = tenantWalletEntry{value: v, fetched: tenantWalletNow()}
	tenantWalletMu.Unlock()
}

// InvalidateTenantWalletCache drops the cached flag of one tenant so the next
// request re-reads it. Called after every tenant write in this process.
func InvalidateTenantWalletCache(tenantID string) {
	tenantWalletMu.Lock()
	delete(tenantWalletCache, tenantID)
	tenantWalletMu.Unlock()
}

// ResetTenantWalletCache clears the whole cache (tests, bulk changes).
func ResetTenantWalletCache() {
	tenantWalletMu.Lock()
	tenantWalletCache = map[string]tenantWalletEntry{}
	tenantWalletMu.Unlock()
}
