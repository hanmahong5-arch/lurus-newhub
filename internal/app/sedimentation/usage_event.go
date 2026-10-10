// Package sedimentation publishes the llm.usage.recorded event for tenants
// that consented to data sedimentation (tenants.sedimentation_consent).
//
// It hangs off repo.OnLogPersisted. The hook itself only copies scalars into a
// bounded queue; the consent lookup, the has_body probe and the publish run on
// one worker goroutine, so the logging path never waits on the database or the
// broker, and a slow broker can only fill (and then drop from) the queue.
package sedimentation

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	hubnats "github.com/LurusTech/lurus-hub/internal/pkg/nats"
)

const (
	// queueSize bounds events held in memory; beyond it events are dropped
	// (delivery is at-most-once by contract).
	queueSize = 4096
	// bodySettle is how long the worker waits after the log row was stored
	// before probing for an archived body: the archive INSERT is dispatched
	// right after the log hook, asynchronously, so probing immediately would
	// almost always say "no body".
	bodySettle = 2 * time.Second
	// consentTTL matches the consent cache in repo (policyCacheTTL): a revoke
	// stops publishing within this window.
	consentTTL = 15 * time.Second
)

// deps are the worker's side effects, injectable for tests.
type deps struct {
	consent func(tenantID string) bool
	hasBody func(tenantID, requestID string) bool
	publish func(ctx context.Context, userID int, p hubnats.LLMUsageRecordedPayload)
	settle  time.Duration
}

type item struct {
	userID  int
	payload hubnats.LLMUsageRecordedPayload
	at      time.Time
}

var (
	startMu sync.Mutex
	started bool
	queue   chan item
)

// RegisterUsageEvents wires the event publisher; call once at boot after NATS
// init. Returns false (and registers nothing) when NATS is disabled, so a
// deployment without NATS pays nothing.
func RegisterUsageEvents(ctx context.Context) bool {
	if !hubnats.Enabled() {
		return false
	}
	startMu.Lock()
	defer startMu.Unlock()
	if started {
		return true
	}
	started = true
	queue = make(chan item, queueSize)
	go runWorker(ctx, queue, defaultDeps())
	repo.OnLogPersisted(onLogPersisted)
	return true
}

func defaultDeps() deps {
	return deps{
		consent: storedConsent,
		hasBody: func(tenantID, requestID string) bool {
			ok, err := repo.LogBodyExists(tenantID, requestID)
			return err == nil && ok
		},
		publish: hubnats.PublishUsageRecorded,
		settle:  bodySettle,
	}
}

// onLogPersisted is the repo hook: cheap, non-blocking, no DB, no ctx.
func onLogPersisted(l *repo.Log) {
	it, ok := buildItem(l)
	if !ok {
		return
	}
	select {
	case queue <- it:
	default:
		metrics.NATSPublishFailedTotal.WithLabelValues("usage").Inc()
	}
}

// buildItem extracts the metadata-only payload from a stored log row. It never
// reads Content or any body: only consume rows with a request id are eligible
// (without the id a consumer could not correlate the event to anything).
func buildItem(l *repo.Log) (item, bool) {
	if l == nil || l.Type != repo.LogTypeConsume || l.TenantId == "" || l.UserId <= 0 {
		return item{}, false
	}
	rid, endUser := otherStrings(l.Other)
	if rid == "" {
		return item{}, false
	}
	return item{
		userID: l.UserId,
		at:     time.Now(),
		payload: hubnats.LLMUsageRecordedPayload{
			TenantID:         l.TenantId,
			ProjectID:        l.ProjectId,
			TokenID:          l.TokenId,
			EndUserHash:      endUser,
			Model:            l.ModelName,
			PromptTokens:     l.PromptTokens,
			CompletionTokens: l.CompletionTokens,
			Quota:            l.Quota,
			ChargedCNY4:      l.ChargedCNY4,
			RequestID:        rid,
			CreatedAt:        l.CreatedAt,
		},
	}, true
}

// storedConsent reads the flag; any error answers false because consent only
// ever loosens, so an unknown state must not publish.
func storedConsent(tenantID string) bool {
	on, err := repo.GetTenantSedimentationConsent(tenantID)
	return err == nil && on
}

func otherStrings(other string) (requestID, endUser string) {
	if other == "" {
		return "", ""
	}
	var m struct {
		RequestID string `json:"request_id"`
		EndUser   string `json:"end_user"`
	}
	if json.Unmarshal([]byte(other), &m) != nil {
		return "", ""
	}
	return m.RequestID, m.EndUser
}

type consentEntry struct {
	on  bool
	exp time.Time
}

func runWorker(ctx context.Context, q <-chan item, d deps) {
	cache := map[string]consentEntry{}
	consents := func(tenantID string) bool {
		now := time.Now()
		if e, ok := cache[tenantID]; ok && now.Before(e.exp) {
			return e.on
		}
		on := d.consent(tenantID)
		cache[tenantID] = consentEntry{on: on, exp: now.Add(consentTTL)}
		return on
	}
	for {
		select {
		case <-ctx.Done():
			return
		case it := <-q:
			handle(ctx, it, d, consents)
		}
	}
}

func handle(ctx context.Context, it item, d deps, consents func(string) bool) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError("usage event worker panicked")
		}
	}()
	// Consent first: a non-consenting tenant costs one cached lookup and
	// never reaches the body probe or the broker.
	if !consents(it.payload.TenantID) {
		return
	}
	if wait := d.settle - time.Since(it.at); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
	it.payload.HasBody = d.hasBody(it.payload.TenantID, it.payload.RequestID)
	d.publish(ctx, it.userID, it.payload)
}
