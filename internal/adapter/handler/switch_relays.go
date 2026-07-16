package handler

import (
	"encoding/json"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// optSwitchRelaysRecommended is the options-table key holding the JSON array
// of relay endpoints this hub recommends to Switch clients. Admin-managed
// through the existing options API; no migration required. Example value:
//
//	[{"id":"lurus-main","name":"Lurus 主线路","kind":"lurus-hub",
//	  "url":"https://hub.lurus.cn","description":"默认推荐"}]
const optSwitchRelaysRecommended = "switch_relays.recommended"

// recommendedRelay is the wire shape consumed by lurus-switch
// internal/relay/cloud.go FetchCloudRelays (a bare JSON array, no envelope).
// The struct is the safety boundary for republishing admin-provided JSON:
// with no api-key field, a credential pasted into the option value can never
// reach clients.
type recommendedRelay struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// GetRecommendedRelays returns the admin-published relay recommendations.
//
// GET /api/v2/relays/recommended
//
//	200: bare JSON array, possibly empty — the Switch client decodes
//	     []RelayEndpoint directly and treats any non-200 as an error, so
//	     "nothing published" and "malformed option value" both serve [].
func GetRecommendedRelays(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[optSwitchRelaysRecommended]
	common.OptionMapRWMutex.RUnlock()

	relays := []recommendedRelay{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &relays); err != nil {
			// Malformed admin JSON must not break clients — serve "no
			// recommendations" and leave the bad value visible in options.
			relays = []recommendedRelay{}
		}
	}
	c.JSON(http.StatusOK, relays)
}
