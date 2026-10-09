package handler

// POST /api/v2/:tenant_slug/channels/import
//
// Bulk account import with a mandatory-by-default dry run. Input is multi-line
// keys and/or a JSON array of items; each item becomes one result row
// {status: ready|duplicate|invalid|probe_failed, error_code, fingerprint}.
// Duplicates are found by key fingerprint (first 16 hex of SHA-256) against
// every key already held by the caller's tenant and inside the batch. A real
// import may probe each ready key first: probes run one at a time with a
// random 300-1500 ms pause so a batch of fresh accounts does not look like a
// burst to the upstream's abuse detection. Key plaintext never reaches the
// audit trail, the logs or the response.
//
// Two shapes: with channel_id the keys are appended to that multi-key
// channel; without it every item becomes its own single-key channel (type and
// models then come from the request or the item). The staged dry-run / probe /
// confirm flow follows gpt-load internal/control/credential_import_batch.go
// (MIT); the code is original.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

const maxImportItems = 500

// Import result statuses and error codes.
const (
	importReady       = "ready"
	importDuplicate   = "duplicate"
	importInvalid     = "invalid"
	importProbeFailed = "probe_failed"
)

// importJitter returns the pause before each probe after the first. A var so
// tests run without sleeping.
var importJitter = func() time.Duration {
	return time.Duration(300+rand.Intn(1201)) * time.Millisecond // #nosec G404 -- pacing, not security
}

// stringList accepts either a JSON array of strings or one comma-separated string.
type stringList []string

func (s *stringList) UnmarshalJSON(b []byte) error {
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*s = arr
		return nil
	}
	var one string
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	for _, p := range strings.Split(one, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

type importItem struct {
	Key       string          `json:"key"`
	Name      string          `json:"name"`
	BaseURL   string          `json:"base_url"`
	Models    stringList      `json:"models"`
	PlanKind  string          `json:"plan_kind"`
	ExpiresAt json.RawMessage `json:"expires_at"` // RFC3339 string or Unix seconds
	Proxy     string          `json:"proxy"`
	Weight    *int            `json:"weight"`
	Tags      stringList      `json:"tags"`
}

// UnmarshalJSON lets a bare JSON string stand for an item with only a key.
func (it *importItem) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*it = importItem{Key: s}
		return nil
	}
	type plain importItem
	return json.Unmarshal(b, (*plain)(it))
}

type importRequest struct {
	DryRun    *bool        `json:"dry_run"` // default true
	Probe     bool         `json:"probe"`
	ChannelID int          `json:"channel_id"`
	Type      int          `json:"type"`
	Group     string       `json:"group"`
	BaseURL   string       `json:"base_url"`
	Models    stringList   `json:"models"`
	Proxy     string       `json:"proxy"`
	Keys      string       `json:"keys"` // multi-line keys, or a JSON array
	Items     []importItem `json:"items"`
}

type importProbeResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

type importResult struct {
	Index             int                `json:"index"`
	Name              string             `json:"name,omitempty"`
	Status            string             `json:"status"`
	ErrorCode         string             `json:"error_code,omitempty"`
	Fingerprint       string             `json:"fingerprint,omitempty"`
	ExistingChannelID int                `json:"existing_channel_id,omitempty"`
	Probe             *importProbeResult `json:"probe,omitempty"`
	Imported          bool               `json:"imported"`
	ChannelID         int                `json:"channel_id,omitempty"`
	KeyIndex          *int               `json:"key_index,omitempty"`

	item importItem
	meta entity.KeyMeta
}

// collectImportItems merges the multi-line/JSON "keys" field with "items".
func collectImportItems(req *importRequest) ([]importItem, error) {
	var items []importItem
	if raw := strings.TrimSpace(req.Keys); raw != "" {
		if strings.HasPrefix(raw, "[") {
			var arr []importItem
			if err := json.Unmarshal([]byte(raw), &arr); err != nil {
				return nil, fmt.Errorf("keys is not a valid JSON array: %w", err)
			}
			items = append(items, arr...)
		} else {
			for _, line := range strings.Split(raw, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					items = append(items, importItem{Key: line})
				}
			}
		}
	}
	items = append(items, req.Items...)
	if len(items) == 0 {
		return nil, fmt.Errorf("no keys supplied")
	}
	if len(items) > maxImportItems {
		return nil, fmt.Errorf("at most %d keys per import", maxImportItems)
	}
	return items, nil
}

// parseImportExpiry reads an RFC3339 string or Unix seconds; empty means never.
func parseImportExpiry(raw json.RawMessage) (int64, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == `""` {
		return 0, nil
	}
	if n, err := strconv.ParseInt(strings.Trim(s, `"`), 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("negative")
		}
		return n, nil
	}
	var str string
	if err := json.Unmarshal(raw, &str); err != nil {
		return 0, err
	}
	t, err := time.Parse(time.RFC3339, str)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}

// existingKeyIndex maps fingerprint -> owning channel id for every key the
// tenant already holds.
func existingKeyIndex(tenantID string) (map[string]int, error) {
	var chs []repo.Channel
	if err := repo.DB.Where("tenant_id = ?", tenantID).Find(&chs).Error; err != nil {
		return nil, err
	}
	idx := make(map[string]int, len(chs))
	for i := range chs {
		for _, k := range chs[i].GetKeys() {
			if strings.TrimSpace(k) != "" {
				idx[keyFingerprint(k)] = chs[i].Id
			}
		}
	}
	return idx, nil
}

// stageImportItems validates every item and dedupes it; no I/O to upstreams.
func stageImportItems(items []importItem, req *importRequest, target *repo.Channel, existing map[string]int) []*importResult {
	seen := map[string]int{}
	out := make([]*importResult, 0, len(items))
	for i, it := range items {
		r := &importResult{Index: i, Name: strings.TrimSpace(it.Name), item: it}
		out = append(out, r)
		key := strings.TrimSpace(it.Key)
		if key == "" {
			r.Status, r.ErrorCode = importInvalid, "empty_key"
			continue
		}
		r.Fingerprint = keyFingerprint(key)
		if it.Weight != nil && !validKeyWeight(*it.Weight) {
			r.Status, r.ErrorCode = importInvalid, "invalid_weight"
			continue
		}
		exp, err := parseImportExpiry(it.ExpiresAt)
		if err != nil {
			r.Status, r.ErrorCode = importInvalid, "invalid_expires_at"
			continue
		}
		proxy := firstNonEmpty(it.Proxy, req.Proxy)
		if err := app.ValidateOutboundProxy(proxy); err != nil {
			r.Status, r.ErrorCode = importInvalid, "proxy_rejected"
			continue
		}
		if target == nil {
			if len(it.Models) == 0 && len(req.Models) == 0 {
				r.Status, r.ErrorCode = importInvalid, "models_required"
				continue
			}
			if err := app.ValidateOutboundURL(effectiveImportBaseURL(it, req)); err != nil {
				r.Status, r.ErrorCode = importInvalid, "base_url_rejected"
				continue
			}
		}
		if cid, dup := existing[r.Fingerprint]; dup {
			r.Status, r.ErrorCode, r.ExistingChannelID = importDuplicate, "duplicate_existing_key", cid
			continue
		}
		if _, dup := seen[r.Fingerprint]; dup {
			r.Status, r.ErrorCode = importDuplicate, "duplicate_in_batch"
			continue
		}
		seen[r.Fingerprint] = i
		r.Status = importReady
		r.meta = entity.KeyMeta{Name: r.Name, PlanKind: strings.TrimSpace(it.PlanKind), ExpiresAt: exp, Tags: it.Tags}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func importModels(it importItem, req *importRequest) []string {
	if len(it.Models) > 0 {
		return it.Models
	}
	return req.Models
}

func effectiveImportBaseURL(it importItem, req *importRequest) string {
	if u := firstNonEmpty(it.BaseURL, req.BaseURL); u != "" {
		return u
	}
	if req.Type > 0 && req.Type < len(constant.ChannelBaseURLs) {
		return constant.ChannelBaseURLs[req.Type]
	}
	return ""
}

// probeImportItems probes the ready items one by one with jitter between them.
func probeImportItems(ctx context.Context, results []*importResult, req *importRequest, target *repo.Channel) {
	first := true
	for _, r := range results {
		if r.Status != importReady {
			continue
		}
		if !first {
			select {
			case <-ctx.Done():
				r.Status, r.ErrorCode = importProbeFailed, "probe_cancelled"
				continue
			case <-time.After(importJitter()):
			}
		}
		first = false
		baseURL, model, proxy := "", "", firstNonEmpty(r.item.Proxy, req.Proxy)
		if target != nil {
			baseURL = target.GetBaseURL()
			if ms := target.GetModels(); len(ms) > 0 {
				model = ms[0]
			}
			if proxy == "" {
				proxy = target.GetSetting().Proxy
			}
		} else {
			baseURL = effectiveImportBaseURL(r.item, req)
			if ms := importModels(r.item, req); len(ms) > 0 {
				model = ms[0]
			}
		}
		latency, errMsg := keyProbeFn(ctx, baseURL, model, strings.TrimSpace(r.item.Key), proxy)
		r.Probe = &importProbeResult{OK: errMsg == "", LatencyMs: latency, Error: errMsg}
		if errMsg != "" {
			r.Status, r.ErrorCode = importProbeFailed, "probe_failed"
		}
	}
}

// ImportChannelsV2 handles the bulk import / dry run.
//
// Route: POST /api/v2/:tenant_slug/channels/import
func ImportChannelsV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return
	}
	if !isPlatformStaff(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Admin role required"})
		return
	}
	var req importRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request parameters"})
		return
	}
	dryRun := req.DryRun == nil || *req.DryRun
	items, err := collectImportItems(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	var target *repo.Channel
	if req.ChannelID > 0 {
		t, err := repo.GetChannelById(req.ChannelID, true)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Channel not found"})
			return
		}
		if t.TenantId != tenantCtx.TenantID {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Access denied"})
			return
		}
		if !t.ChannelInfo.IsMultiKey || strings.HasPrefix(strings.TrimSpace(t.Key), "[") {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Target must be a newline-keyed multi-key channel"})
			return
		}
		if err := validateChannelEgress(t); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
			return
		}
		target = t
	} else if req.Type <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "type (or channel_id) is required"})
		return
	}

	existing, err := existingKeyIndex(tenantCtx.TenantID)
	if err != nil {
		common.SysError("import: load existing keys: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to load existing keys"})
		return
	}
	results := stageImportItems(items, &req, target, existing)

	if !dryRun {
		if enforceChannelSensitiveWriteDecided(c, true, req.ChannelID) {
			return
		}
		if req.Probe {
			probeImportItems(c.Request.Context(), results, &req, target)
		}
		if err := persistImport(c, tenantCtx.TenantID, &req, target, results); err != nil {
			common.SysError("import: persist: " + err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to import keys"})
			return
		}
	}

	summary := map[string]int{importReady: 0, importDuplicate: 0, importInvalid: 0, importProbeFailed: 0, "imported": 0}
	fps := make([]string, 0, len(results))
	for _, r := range results {
		summary[r.Status]++
		if r.Imported {
			summary["imported"]++
			fps = append(fps, r.Fingerprint)
		}
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, tenantCtx.UserID,
		governance.ActionChannelKeysImported, governance.ResourceChannel, req.ChannelID, importAuditDetails(dryRun, req.Probe, summary, fps)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"dry_run": dryRun, "summary": summary, "results": results}})
}

func importAuditDetails(dryRun, probe bool, summary map[string]int, importedFingerprints []string) string {
	b, _ := json.Marshal(map[string]interface{}{
		"dry_run": dryRun, "probe": probe, "summary": summary, "imported_fingerprints": importedFingerprints,
	})
	return string(b)
}

// persistImport writes the ready items: appended keys on target, or one new
// single-key channel per item.
func persistImport(c *gin.Context, tenantID string, req *importRequest, target *repo.Channel, results []*importResult) error {
	if target != nil {
		return appendImportedKeys(target, req, results)
	}
	group := firstNonEmpty(req.Group, "default")
	for _, r := range results {
		if r.Status != importReady {
			continue
		}
		it := r.item
		base := effectiveImportBaseURL(it, req)
		models := importModels(it, req)
		name := r.Name
		if name == "" {
			name = "import-" + r.Fingerprint[:8]
		}
		ch := repo.Channel{
			Name: name, Key: strings.TrimSpace(it.Key), Type: req.Type, Status: common.ChannelStatusEnabled,
			Group: group, TenantId: tenantID, CreatedTime: common.GetTimestamp(),
			Models: strings.Join(models, ","), BaseURL: &base,
		}
		if len(it.Tags) > 0 {
			t := strings.Join(it.Tags, ",")
			ch.Tag = &t
		}
		if it.Weight != nil {
			w := uint(*it.Weight)
			ch.Weight = &w
		}
		if proxy := firstNonEmpty(it.Proxy, req.Proxy); proxy != "" {
			ch.SetSetting(dto.ChannelSettings{Proxy: proxy})
		}
		if r.meta.PlanKind != "" || r.meta.ExpiresAt > 0 || r.meta.Name != "" || len(r.meta.Tags) > 0 {
			ch.ChannelInfo.MultiKeyMeta = map[int]entity.KeyMeta{0: r.meta}
		}
		if err := ch.Insert(); err != nil {
			return err
		}
		r.Imported, r.ChannelID = true, ch.Id
		zero := 0
		r.KeyIndex = &zero
	}
	AsyncGo(func() { repo.InitChannelCache() })
	return nil
}

func appendImportedKeys(target *repo.Channel, req *importRequest, results []*importResult) error {
	lock := repo.GetChannelPollingLock(target.Id)
	lock.Lock()
	defer lock.Unlock()
	keys := target.GetKeys()
	added := 0
	for _, r := range results {
		if r.Status != importReady {
			continue
		}
		idx := len(keys)
		keys = append(keys, strings.TrimSpace(r.item.Key))
		info := &target.ChannelInfo
		if proxy := firstNonEmpty(r.item.Proxy, req.Proxy); proxy != "" {
			if info.MultiKeyProxy == nil {
				info.MultiKeyProxy = map[int]string{}
			}
			info.MultiKeyProxy[idx] = proxy
		}
		if r.item.Weight != nil {
			if info.MultiKeyWeight == nil {
				info.MultiKeyWeight = map[int]int{}
			}
			info.MultiKeyWeight[idx] = *r.item.Weight
		}
		if r.meta.PlanKind != "" || r.meta.ExpiresAt > 0 || r.meta.Name != "" || len(r.meta.Tags) > 0 {
			if info.MultiKeyMeta == nil {
				info.MultiKeyMeta = map[int]entity.KeyMeta{}
			}
			info.MultiKeyMeta[idx] = r.meta
		}
		r.Imported, r.ChannelID = true, target.Id
		i := idx
		r.KeyIndex = &i
		added++
	}
	if added == 0 {
		return nil
	}
	target.Key = strings.Join(keys, "\n")
	target.ChannelInfo.MultiKeySize = len(keys)
	if err := repo.DB.Model(&repo.Channel{}).Where("id = ?", target.Id).Update("key", target.Key).Error; err != nil {
		return err
	}
	if err := target.SaveChannelInfo(); err != nil {
		return err
	}
	AsyncGo(func() { repo.InitChannelCache() })
	return nil
}
