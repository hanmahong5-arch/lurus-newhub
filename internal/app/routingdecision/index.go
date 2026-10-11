package routingdecision

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// IndexTTL bounds how stale a replica's view of the policies can be. Admin
// writes invalidate the local replica immediately; other replicas converge
// within this window (same convention as the content-rule cache).
const IndexTTL = 15 * time.Second

// failureBackoff is how long a failed reload keeps the previous snapshot
// before trying again, so a database blip is not retried on every request.
const failureBackoff = 5 * time.Second

type snapshot struct {
	byKey map[string]*entity.RoutingPolicy
	exp   time.Time
}

// Index is the request path's view of the enabled policies. The whole enabled
// set is held in memory, so a lookup is a map read; the database is touched
// once per TTL per replica, never per request. With no enabled policy the map
// is empty and the lookup returns immediately - the closed state costs one
// atomic load and one map miss.
type Index struct {
	loader func() ([]entity.RoutingPolicy, error)
	now    func() time.Time
	snap   atomic.Pointer[snapshot]
	// loading serialises reloads; a request that finds a reload already
	// running serves the stale snapshot instead of queueing behind it.
	loading sync.Mutex
}

// NewIndex builds an index over loader. now may be nil (time.Now).
func NewIndex(loader func() ([]entity.RoutingPolicy, error), now func() time.Time) *Index {
	if now == nil {
		now = time.Now
	}
	return &Index{loader: loader, now: now}
}

func indexKey(tenantID, model string) string { return tenantID + "\x00" + model }

// Lookup returns the enabled decision policy for (tenant, model), or nil.
func (i *Index) Lookup(tenantID, model string) *entity.RoutingPolicy {
	if tenantID == "" || model == "" {
		return nil
	}
	s := i.snap.Load()
	if s == nil || !i.now().Before(s.exp) {
		s = i.refresh(s)
	}
	if s == nil {
		return nil
	}
	return s.byKey[indexKey(tenantID, model)]
}

// Invalidate drops the snapshot so the next Lookup reloads. Called by the
// admin handlers after a write.
func (i *Index) Invalidate() { i.snap.Store(nil) }

func (i *Index) refresh(stale *snapshot) *snapshot {
	if !i.loading.TryLock() {
		return stale // another request is reloading; never queue the hot path
	}
	defer i.loading.Unlock()
	// Another goroutine may have finished a reload between our check and the
	// lock; reuse it rather than reloading twice.
	if cur := i.snap.Load(); cur != nil && i.now().Before(cur.exp) {
		return cur
	}
	rows, err := i.loader()
	if err != nil {
		common.SysLog("routing policy reload failed, keeping previous view: " + err.Error())
		next := &snapshot{exp: i.now().Add(failureBackoff)}
		if stale != nil {
			next.byKey = stale.byKey
		} else {
			// No previous view: fail open to "no policies" (routing is an
			// optimisation, never a reason to refuse a request).
			next.byKey = map[string]*entity.RoutingPolicy{}
		}
		i.snap.Store(next)
		return next
	}
	m := make(map[string]*entity.RoutingPolicy, len(rows))
	for k := range rows {
		p := rows[k]
		if !p.Enabled || p.Strategy != entity.RoutingStrategyDecision {
			continue
		}
		m[indexKey(p.TenantID, p.PublicModel)] = &p
	}
	next := &snapshot{byKey: m, exp: i.now().Add(IndexTTL)}
	i.snap.Store(next)
	return next
}

// defaultIndex is the process-wide index over the database.
var defaultIndex = NewIndex(func() ([]entity.RoutingPolicy, error) {
	if repo.DB == nil {
		return nil, nil // hermetic tests / boot not finished: no policies
	}
	return repo.ListEnabledRoutingPolicies()
}, nil)

// Lookup is the process-wide lookup used by the request path.
func Lookup(tenantID, model string) *entity.RoutingPolicy {
	return defaultIndex.Lookup(tenantID, model)
}

// Invalidate drops the process-wide snapshot.
func Invalidate() { defaultIndex.Invalidate() }
