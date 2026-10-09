package handler

// GET /api/v2/public/model-status - anonymous, cacheable model availability.
//
// It answers only "can this model be served right now": one status per model,
// derived from channel routability. No channel id, name, type, group, tenant,
// key, error text or upstream host ever reaches the response. Only
// platform-shared channels count, so a tenant's private model names are not
// listed. This sits next to (not in place of) the uptime-kuma status
// integration, which reports probe results of the deployment as a whole.

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Public model states.
const (
	ModelStatusOperational = "operational"
	ModelStatusDegraded    = "degraded"
	ModelStatusDown        = "down"
)

const publicModelStatusTTL = 30 * time.Second

// modelSummary counts the platform-shared channels offering one model.
type modelSummary struct {
	Total    int // switched-on or auto-disabled channels offering the model
	Routable int // of those, routable right now
}

// summarizeModels counts, per model, the platform-shared channels that offer
// it and how many of them can route right now. Channels an operator switched
// off by hand are not counted at all (a deliberate removal is not an outage).
func summarizeModels(chans []*repo.Channel, now int64) map[string]modelSummary {
	out := map[string]modelSummary{}
	for _, ch := range chans {
		if ch.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		if ch.TenantId != "" && ch.TenantId != "default" {
			continue
		}
		routable := buildChannelHealth(ch, now).Routable
		for _, m := range ch.GetModels() {
			if m == "" {
				continue
			}
			s := out[m]
			s.Total++
			if routable {
				s.Routable++
			}
			out[m] = s
		}
	}
	return out
}

// classifyModel: down when nothing routes, degraded when at most half of the
// offering channels route, otherwise operational.
func classifyModel(s modelSummary) string {
	switch {
	case s.Routable == 0:
		return ModelStatusDown
	case s.Routable*2 <= s.Total && s.Routable < s.Total:
		return ModelStatusDegraded
	}
	return ModelStatusOperational
}

// PublicModelStatusEntry is the whole public record: two fields, by design.
type PublicModelStatusEntry struct {
	Model  string `json:"model"`
	Status string `json:"status"`
}

type publicModelStatusCache struct {
	mu      sync.Mutex
	at      time.Time
	payload []PublicModelStatusEntry
}

var modelStatusCache publicModelStatusCache

// publicModelStatusNow is the clock seam for tests.
var publicModelStatusNow = time.Now

func (c *publicModelStatusCache) get(now time.Time) ([]PublicModelStatusEntry, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.payload != nil && now.Sub(c.at) < publicModelStatusTTL {
		return c.payload, c.at, nil
	}
	chans, err := listChannelsForMonitoring()
	if err != nil {
		if c.payload != nil {
			return c.payload, c.at, nil // stale beats an error page for a status view
		}
		return nil, time.Time{}, err
	}
	sums := summarizeModels(chans, now.Unix())
	list := make([]PublicModelStatusEntry, 0, len(sums))
	for m, s := range sums {
		list = append(list, PublicModelStatusEntry{Model: m, Status: classifyModel(s)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Model < list[j].Model })
	c.payload, c.at = list, now
	return list, now, nil
}

// GetPublicModelStatus serves the public view. The caller's IP rate limit is
// the router-level GlobalV2RateLimit; the 30s cache bounds the work per
// replica regardless of request volume.
func GetPublicModelStatus(c *gin.Context) {
	list, at, err := modelStatusCache.get(publicModelStatusNow())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "status unavailable"})
		return
	}
	c.Header("Cache-Control", "public, max-age=30")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"models": list, "updated_at": at.Unix()},
	})
}
