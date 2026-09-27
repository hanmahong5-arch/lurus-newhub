package middleware

// distributor_provider_filter_test.go — cycle 18 L8: the request body's
// `provider` object ({"region": "eu"} / {"data_collection": "deny"}) reaches
// channel selection through the distributor. Drives the real Distribute()
// against two equal-weight channels whose Setting JSON differs only in region
// / data_collection, and asserts WHICH channel was picked.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func seedProviderFilterChannel(t *testing.T, db *gorm.DB, id int, setting string) {
	t.Helper()
	seedTenantRelayChannel(t, db, id, "default", 100)
	if err := db.Model(&repo.Channel{}).Where("id = ?", id).Update("setting", setting).Error; err != nil {
		t.Fatalf("set setting on channel %d: %v", id, err)
	}
}

func selectedChannelID(t *testing.T, body string) int {
	t.Helper()
	var out struct {
		ChannelID int `json:"channel_id"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode capture body %q: %v", body, err)
	}
	return out.ChannelID
}

func TestDistribute_ProviderFilterFromRequestBody(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })

	seedProviderFilterChannel(t, db, 9801, `{"region":"eu"}`)
	seedProviderFilterChannel(t, db, 9802, `{"region":"us","data_collection":"deny"}`)
	repo.InitChannelCache()

	setup := func(c *gin.Context) {
		c.Set("id", 1)
		c.Set("token_id", 7)
		c.Set("group", "default")
		c.Set("tenant_context", &TenantContext{TenantID: "default"})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	}
	r := mountDistributeCapture(setup)

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"region", `{"model":"gpt-4o","provider":{"region":"eu"}}`, 9801},
		{"region_case_insensitive", `{"model":"gpt-4o","provider":{"region":"EU"}}`, 9801},
		{"zdr", `{"model":"gpt-4o","provider":{"data_collection":"deny"}}`, 9802},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 30; i++ {
				w := doDistributeCapture(r, tc.body)
				if w.Code != http.StatusOK {
					t.Fatalf("iteration %d: status = %d, body=%s", i, w.Code, w.Body.String())
				}
				if got := selectedChannelID(t, w.Body.String()); got != tc.want {
					t.Fatalf("iteration %d: selected channel %d, want %d", i, got, tc.want)
				}
			}
		})
	}

	t.Run("no_provider_object_draws_both", func(t *testing.T) {
		seen := map[int]bool{}
		for i := 0; i < 50; i++ {
			w := doDistributeCapture(r, `{"model":"gpt-4o"}`)
			if w.Code != http.StatusOK {
				t.Fatalf("iteration %d: status = %d, body=%s", i, w.Code, w.Body.String())
			}
			seen[selectedChannelID(t, w.Body.String())] = true
		}
		if !seen[9801] || !seen[9802] {
			t.Fatalf("a request without provider must reach both channels, saw %v", seen)
		}
	})

	t.Run("unsatisfiable_filter_names_the_filter", func(t *testing.T) {
		w := doDistributeCapture(r, `{"model":"gpt-4o","provider":{"region":"cn","data_collection":"deny"}}`)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
		}
		want := "no channel satisfies provider filter (region=cn, data_collection=deny)"
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("body = %s, want it to contain %q", w.Body.String(), want)
		}
	})
}
