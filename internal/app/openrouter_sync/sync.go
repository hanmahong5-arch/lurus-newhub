package openrouter_sync

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider/openrouter"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
)

// Engine is the OpenRouter free-model sync engine. It is the single writer
// to the target channel's models list; per-job writes are NOT supported.
type Engine struct {
	HTTPClient FetcherFunc                                    // injectable for tests; default uses http.DefaultClient
	UsageFn    func() ([]Stat, error)                         // injectable for tests; default reads from DB
	Now        func() time.Time                               // injectable for tests; default time.Now
	BaseURL    string                                         // OpenRouter base URL; default "https://openrouter.ai/api"
	UpdateAbs  func(channel *repo.Channel, tx *gorm.DB) error // injectable for tests; default channel.UpdateAbilities
}

// FetcherFunc is the abstract OpenRouter /v1/models fetcher.
type FetcherFunc func(ctx context.Context) ([]openrouter.Model, error)

// NewEngine returns an Engine with production defaults.
func NewEngine() *Engine {
	return &Engine{
		HTTPClient: defaultFetcher(""),
		UsageFn:    LoadUsageStats,
		Now:        time.Now,
		BaseURL:    "https://openrouter.ai/api",
		UpdateAbs:  func(c *repo.Channel, tx *gorm.DB) error { return c.UpdateAbilities(tx) },
	}
}

func defaultFetcher(baseURL string) FetcherFunc {
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api"
	}
	return func(ctx context.Context) ([]openrouter.Model, error) {
		return openrouter.FetchModels(ctx, baseURL, nil)
	}
}

// RunResult summarizes one Run() invocation, suitable for logging or API response.
type RunResult struct {
	FetchedTotal    int      `json:"fetched_total"`
	FreeTotal       int      `json:"free_total"`
	ManagedNewCount int      `json:"managed_new_count"`
	Added           []string `json:"added"`
	Removed         []string `json:"removed"`
	// RegionBlocked lists still-listed free models the per-model prober found
	// refused for regional reasons (ReasonRegionBlocked). They are not offered
	// as new pool members and hold no TopN slot; one already in the pool stays
	// on the channel only so the prober can notice when it recovers.
	RegionBlocked    []string `json:"region_blocked,omitempty"`
	CircuitBreakerOn bool     `json:"circuit_breaker_on"`
	Skipped          bool     `json:"skipped"` // true if no due jobs and not forced
	SkipReason       string   `json:"skip_reason,omitempty"`
}

// CircuitBreakerError is returned when the fetch result is suspiciously small.
// The caller can inspect it to surface a structured warning to admins.
type CircuitBreakerError struct {
	Got, Baseline int
}

func (e *CircuitBreakerError) Error() string {
	return fmt.Sprintf("circuit breaker tripped: fetched %d, baseline %d (threshold = max(10, baseline*0.5))", e.Got, e.Baseline)
}

// circuitBreakerThreshold computes the minimum acceptable fetch size
// given the last-known successful baseline.
func circuitBreakerThreshold(baseline int) int {
	half := baseline / 2
	if half > 10 {
		return half
	}
	return 10
}

// Run executes one cycle of the sync engine.
//
//   - manualOnlyJobIDs (if non-empty): only these jobs are treated as "due"
//     for LastRunAt updates; all enabled jobs still contribute to the managed
//     model union (so unrelated jobs' models are preserved).
//   - force: skip the circuit breaker. Use when an admin has verified an
//     upstream change (e.g. OpenRouter actually deprecated 60% of free models).
func (e *Engine) Run(ctx context.Context, manualOnlyJobIDs []int, force bool) (*RunResult, error) {
	if e.HTTPClient == nil {
		e.HTTPClient = defaultFetcher(e.BaseURL)
	}
	if e.UsageFn == nil {
		e.UsageFn = LoadUsageStats
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.UpdateAbs == nil {
		e.UpdateAbs = func(c *repo.Channel, tx *gorm.DB) error { return c.UpdateAbilities(tx) }
	}

	now := e.Now()

	// 1. Load all enabled jobs and decide which are "due".
	jobs, err := repo.ListEnabledOpenRouterSyncJobs()
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	if len(jobs) == 0 {
		return &RunResult{Skipped: true, SkipReason: "no enabled jobs"}, nil
	}

	manualSet := make(map[int]struct{}, len(manualOnlyJobIDs))
	for _, id := range manualOnlyJobIDs {
		manualSet[id] = struct{}{}
	}

	dueJobs := make([]*repo.OpenRouterSyncJob, 0, len(jobs))
	if len(manualSet) > 0 {
		for _, j := range jobs {
			if _, ok := manualSet[j.Id]; ok {
				dueJobs = append(dueJobs, j)
			}
		}
	} else {
		for _, j := range jobs {
			if j.ShouldRun(now) {
				dueJobs = append(dueJobs, j)
			}
		}
	}
	if len(dueJobs) == 0 {
		return &RunResult{Skipped: true, SkipReason: "no due jobs (and no manual override)"}, nil
	}

	// 2. Group due jobs by target channel id. We do one fetch & one transaction per channel.
	channelGroups := groupByChannel(jobs, dueJobs)

	// 3. Fetch upstream once (shared across all channel groups in this run).
	fetched, err := e.HTTPClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch openrouter models: %w", err)
	}

	freeAll := make([]openrouter.Model, 0, len(fetched))
	for _, m := range fetched {
		if m.IsFree() {
			freeAll = append(freeAll, m)
		}
	}

	// 4. Load usage stats (best-effort; nil-safe).
	usage := tryLoadUsage(e.UsageFn, now)

	result := &RunResult{
		FetchedTotal: len(fetched),
		FreeTotal:    len(freeAll),
	}

	// 5. Apply per-channel transactions.
	for channelID, grp := range channelGroups {
		added, removed, blocked, breakerTripped, applyErr := e.applyChannel(ctx, channelID, grp, freeAll, len(fetched), usage, force)
		if applyErr != nil {
			// Persist LastError on every due job tied to this channel and continue with other channels.
			for _, dj := range grp.dueJobs {
				dj.LastError = applyErr.Error()
				_ = repo.UpdateOpenRouterSyncJob(dj)
			}
			if breakerTripped {
				result.CircuitBreakerOn = true
			}
			common.SysLog(fmt.Sprintf("openrouter sync: channel %d failed: %s", channelID, applyErr.Error()))
			continue
		}
		result.Added = append(result.Added, added...)
		result.Removed = append(result.Removed, removed...)
		result.RegionBlocked = append(result.RegionBlocked, blocked...)
		result.ManagedNewCount += len(added)

		// Mark due jobs as run.
		for _, dj := range grp.dueJobs {
			t := now
			dj.LastRunAt = &t
			dj.LastError = ""
			if err := repo.UpdateOpenRouterSyncJob(dj); err != nil {
				common.SysLog(fmt.Sprintf("openrouter sync: failed to persist job %d: %s", dj.Id, err.Error()))
			}
		}
	}

	return result, nil
}

// channelGroup pairs the set of all enabled jobs for a channel with the subset
// that is due in this run. The full set computes the managed-models union
// (so non-due jobs preserve their models); the due subset gets LastRunAt updates.
type channelGroup struct {
	allJobs []*repo.OpenRouterSyncJob
	dueJobs []*repo.OpenRouterSyncJob
}

func groupByChannel(allEnabled, due []*repo.OpenRouterSyncJob) map[int]*channelGroup {
	groups := make(map[int]*channelGroup)
	dueSet := make(map[int]struct{}, len(due))
	for _, j := range due {
		dueSet[j.Id] = struct{}{}
	}
	for _, j := range allEnabled {
		g, ok := groups[j.TargetChannelId]
		if !ok {
			g = &channelGroup{}
			groups[j.TargetChannelId] = g
		}
		g.allJobs = append(g.allJobs, j)
		if _, isDue := dueSet[j.Id]; isDue {
			g.dueJobs = append(g.dueJobs, j)
		}
	}
	// Drop channels that have no due jobs (nothing to do this tick).
	for id, g := range groups {
		if len(g.dueJobs) == 0 {
			delete(groups, id)
		}
	}
	return groups
}

// applyChannel computes the new managed set from all enabled jobs targeting
// this channel, runs the circuit breaker, and writes the diff in a single
// transaction with row-level lock.
func (e *Engine) applyChannel(
	ctx context.Context,
	channelID int,
	grp *channelGroup,
	freeAll []openrouter.Model,
	fetchedTotal int,
	usage UsageMap,
	force bool,
) (added, removed, regionBlocked []string, breakerTripped bool, err error) {

	var addedOut, removedOut, blockedOut []string

	txErr := repo.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ch, err := repo.GetChannelForUpdate(tx, channelID)
		if err != nil {
			return fmt.Errorf("load channel for update: %w", err)
		}

		// Circuit breaker (per-channel baseline).
		if !force && fetchedTotal < circuitBreakerThreshold(ch.LastSyncFetchCount) {
			breakerTripped = true
			return &CircuitBreakerError{Got: fetchedTotal, Baseline: ch.LastSyncFetchCount}
		}

		managedOld := setFromSlice(ch.GetManagedModelsBySync())
		current := setFromSlice(ch.GetModels())

		// Models the prober found region-blocked on this channel. They are
		// kept out of the ranking so a blocked model neither enters the pool
		// nor holds a TopN slot a working model could use.
		// Only rows for models the channel still lists count: the prober
		// re-checks listed models only, so a row for an unlisted model (delisted
		// then relisted, or removed by an admin) can never be cleared and must
		// not keep the model out. Such a model is re-added and re-probed.
		blocked := loadRegionBlockedModels(tx, channelID, setUnion(current, managedOld))
		freeIDs := make(map[string]struct{}, len(freeAll))
		for _, m := range freeAll {
			freeIDs[m.ID] = struct{}{}
		}

		// Compute managedNew = union over all enabled jobs targeting this channel.
		managedNew := make(map[string]struct{})
		for _, j := range grp.allJobs {
			cats := j.GetCategories()
			if len(cats) == 0 {
				continue
			}
			subset := make([]openrouter.Model, 0, len(freeAll))
			for _, m := range freeAll {
				if _, isBlocked := blocked[m.ID]; isBlocked {
					continue
				}
				if MatchesAny(m.Architecture.InputModalities, m.Architecture.OutputModalities, cats) {
					subset = append(subset, m)
				}
			}
			ranked := RankAndTrim(subset, usage, j.TopN)
			for _, m := range ranked {
				managedNew[m.ID] = struct{}{}
			}
		}

		// A blocked model already in the pool stays on the channel: the
		// prober only re-checks models the channel lists, so removing it
		// would make the block permanent. Its abilities are already off
		// (prober auto-disable), so it is not routed to. A model the
		// catalogue no longer lists is NOT kept - delisting always removes.
		for id := range blocked {
			if _, listed := freeIDs[id]; !listed {
				continue
			}
			blockedOut = append(blockedOut, id)
			if _, wasManaged := managedOld[id]; wasManaged {
				managedNew[id] = struct{}{}
			}
		}
		sort.Strings(blockedOut)

		// manual = current − managedOld (preserve admin-added models)
		manual := setDifference(current, managedOld)
		// finalSet = manual ∪ managedNew
		finalSet := setUnion(manual, managedNew)

		// Compute diff for telemetry.
		addedOut = sortedDiff(managedNew, managedOld)
		removedOut = sortedDiff(managedOld, managedNew)

		// Serialize to CSV + JSON.
		newCSV := joinCSV(setToSortedSlice(finalSet))
		managedJSON, err := json.Marshal(setToSortedSlice(managedNew))
		if err != nil {
			return fmt.Errorf("encode managed set: %w", err)
		}

		if err := repo.UpdateChannelSyncState(tx, channelID, newCSV, string(managedJSON), fetchedTotal); err != nil {
			return fmt.Errorf("update channel sync state: %w", err)
		}

		// Sync abilities table within the same transaction.
		ch.Models = newCSV
		if err := e.UpdateAbs(ch, tx); err != nil {
			return fmt.Errorf("update abilities: %w", err)
		}

		return nil
	})

	if txErr != nil {
		return nil, nil, nil, breakerTripped, txErr
	}

	// Auto-create model metadata entries for newly added models (best-effort, outside the channel tx).
	if len(addedOut) > 0 {
		ensureModelMetadataEntries(addedOut)
	}

	return addedOut, removedOut, blockedOut, false, nil
}

// loadRegionBlockedModels returns the models of one channel whose latest
// probe was refused for regional reasons (see IsRegionBlocked). It fails open:
// a missing table or a read error yields an empty set, so a probe-store
// problem can never shrink the pool - the sync then behaves as it did before
// the prober existed. Rows for models not in onChannel are stale (the prober
// no longer visits them) and are ignored.
func loadRegionBlockedModels(db *gorm.DB, channelID int, onChannel map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{})
	if !db.Migrator().HasTable(&entity.ModelHealth{}) {
		return out
	}
	var rows []entity.ModelHealth
	if err := db.Where("channel_id = ? AND ok = ?", channelID, false).Find(&rows).Error; err != nil {
		common.SysLog(fmt.Sprintf("openrouter sync: channel %d: read model health failed, treating no model as region-blocked: %s", channelID, err.Error()))
		return out
	}
	for _, h := range rows {
		if _, listed := onChannel[h.Model]; !listed {
			continue
		}
		if IsRegionBlocked(h) {
			out[h.Model] = struct{}{}
		}
	}
	return out
}

// ensureModelMetadataEntries auto-creates Model rows for newly imported models
// so they show up in the admin model catalog and pricing flows.
func ensureModelMetadataEntries(newModels []string) {
	if len(newModels) == 0 {
		return
	}
	vendorID, err := repo.GetOrCreateVendorByName("OpenRouter")
	if err != nil {
		common.SysLog("openrouter sync: failed to get/create OpenRouter vendor: " + err.Error())
		return
	}
	created := 0
	for _, name := range newModels {
		exists, err := repo.IsModelNameDuplicated(0, name)
		if err != nil {
			common.SysLog(fmt.Sprintf("openrouter sync: failed to check model %q: %s", name, err.Error()))
			continue
		}
		if exists {
			continue
		}
		m := &repo.Model{
			ModelName:    name,
			VendorID:     vendorID,
			Status:       1,
			NameRule:     repo.NameRuleExact,
			SyncOfficial: 1,
		}
		if err := repo.ModelInsert(m); err != nil {
			common.SysLog(fmt.Sprintf("openrouter sync: failed to create model metadata for %q: %s", name, err.Error()))
			continue
		}
		created++
	}
	if created > 0 {
		common.SysLog(fmt.Sprintf("openrouter sync: auto-created %d model metadata rows", created))
		repo.RefreshPricing()
	}
}

// tryLoadUsage runs UsageFn and converts the result to a UsageMap. Errors
// are logged and the result is nil (cold-start fallback).
func tryLoadUsage(fn func() ([]Stat, error), now time.Time) UsageMap {
	stats, err := fn()
	if err != nil {
		common.SysLog("openrouter sync: load usage stats failed: " + err.Error())
		return nil
	}
	return BuildUsageMap(stats, now)
}

// Preview runs the fetch + classify + rank steps for a single job WITHOUT
// writing to the channel. Used by the admin "preview" endpoint to let users
// see what a job would import.
func (e *Engine) Preview(ctx context.Context, job *repo.OpenRouterSyncJob) ([]openrouter.Model, error) {
	if e.HTTPClient == nil {
		e.HTTPClient = defaultFetcher(e.BaseURL)
	}
	if e.UsageFn == nil {
		e.UsageFn = LoadUsageStats
	}
	if e.Now == nil {
		e.Now = time.Now
	}

	fetched, err := e.HTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	cats := job.GetCategories()
	subset := make([]openrouter.Model, 0, len(fetched))
	for _, m := range fetched {
		if !m.IsFree() {
			continue
		}
		if !MatchesAny(m.Architecture.InputModalities, m.Architecture.OutputModalities, cats) {
			continue
		}
		subset = append(subset, m)
	}
	usage := tryLoadUsage(e.UsageFn, e.Now())
	return RankAndTrim(subset, usage, job.TopN), nil
}

// --- small helpers ---

func setFromSlice(xs []string) map[string]struct{} {
	out := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x != "" {
			out[x] = struct{}{}
		}
	}
	return out
}

func setDifference(a, b map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{})
	for k := range a {
		if _, ok := b[k]; !ok {
			out[k] = struct{}{}
		}
	}
	return out
}

func setUnion(a, b map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

func setToSortedSlice(s map[string]struct{}) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedDiff(a, b map[string]struct{}) []string {
	out := make([]string, 0)
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func joinCSV(xs []string) string {
	return strings.Join(xs, ",")
}
