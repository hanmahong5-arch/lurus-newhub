package app

import (
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"

	"github.com/gin-gonic/gin"
)

// A policy hit leaves routing_decision in the gin context; the consume row
// mirrors it under other.routed. A direct request has neither.
func TestGenerateTextOtherInfo_RoutedMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mk := func() (*gin.Context, *relaycommon.RelayInfo) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		start := time.Now()
		return c, &relaycommon.RelayInfo{
			StartTime:         start,
			FirstResponseTime: start.Add(-time.Second),
			ChannelMeta:       &relaycommon.ChannelMeta{},
		}
	}
	c, info := mk()
	if _, ok := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 0, 0, 1)["routed"]; ok {
		t.Fatal("direct request must not carry other.routed")
	}
	c, info = mk()
	c.Set("routing_decision", map[string]any{"requested": "model-a", "model": "model-b", "reason": "applied"})
	got, ok := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 0, 0, 1)["routed"].(map[string]any)
	if !ok || got["requested"] != "model-a" || got["model"] != "model-b" || got["reason"] != "applied" {
		t.Fatalf("other.routed = %#v, want the decision triple", got)
	}
}
