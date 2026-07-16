package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// setRelaysOption swaps the recommended-relays option value and restores the
// previous state on cleanup — same save/restore pattern as the switch app
// release tests.
func setRelaysOption(t *testing.T, value string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	prev, existed := common.OptionMap[optSwitchRelaysRecommended]
	if value == "" {
		delete(common.OptionMap, optSwitchRelaysRecommended)
	} else {
		common.OptionMap[optSwitchRelaysRecommended] = value
	}
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if existed {
			common.OptionMap[optSwitchRelaysRecommended] = prev
		} else {
			delete(common.OptionMap, optSwitchRelaysRecommended)
		}
		common.OptionMapRWMutex.Unlock()
	})
}

func getRecommendedRelaysResponse(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v2/relays/recommended", GetRecommendedRelays)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/relays/recommended", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestGetRecommendedRelays_UnsetServesEmptyArray(t *testing.T) {
	setRelaysOption(t, "")

	w := getRecommendedRelaysResponse(t)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// Bare-array contract: the Switch client decodes []RelayEndpoint directly.
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestGetRecommendedRelays_PublishedList(t *testing.T) {
	setRelaysOption(t, `[{"id":"lurus-main","name":"Lurus 主线路","kind":"lurus-hub","url":"https://hub.lurus.cn","description":"默认推荐"}]`)

	w := getRecommendedRelaysResponse(t)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var relays []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &relays); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(relays) != 1 {
		t.Fatalf("len = %d, want 1", len(relays))
	}
	if relays[0]["id"] != "lurus-main" || relays[0]["url"] != "https://hub.lurus.cn" {
		t.Errorf("unexpected entry: %v", relays[0])
	}
}

func TestGetRecommendedRelays_NeverLeaksKeysAndSurvivesBadJSON(t *testing.T) {
	// An apiKey pasted into the option value must not reach clients: the wire
	// struct has no such field.
	setRelaysOption(t, `[{"id":"x","name":"n","kind":"custom","url":"https://r.example","apiKey":"sk-SECRET"}]`)
	w := getRecommendedRelaysResponse(t)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), "SECRET") {
		t.Error("response leaked apiKey from option value")
	}

	// Malformed JSON degrades to an empty list, never an error.
	setRelaysOption(t, `{not json!!`)
	w = getRecommendedRelaysResponse(t)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("body = %q, want [] for malformed option", got)
	}
}
