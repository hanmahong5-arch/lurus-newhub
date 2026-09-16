package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// registerV1ChannelRoutes mounts the v1 admin channel handlers under test on the
// shared V2 harness router (they are not part of the v2 group registration).
// Role is root: this file and channel_config_validation_legacy_test.go (which
// reuses it) exercise channel CRUD/config-validation mechanics, not the
// channel:sensitive_write grant (L2, cycle 9) — a non-root identity here
// would now need an explicit grant to reach code these tests cover for
// unrelated reasons.
func registerV1ChannelRoutes(ctx *V2TestContext) {
	v1 := ctx.Router.Group("/api")
	v1.Use(func(c *gin.Context) {
		c.Set("tenant_id", ctx.TenantID)
		c.Set("id", ctx.AdminUser.Id)
		c.Set("role", common.RoleRootUser)
		c.Next()
	})
	v1.POST("/channel", AddChannel)
	v1.POST("/channel/copy/:id", CopyChannel)
}

// TestAddChannel_NullChannelBody guards against a nil-pointer panic: JSON null
// for the channel field binds without error, leaving Channel nil, which was
// dereferenced before validation. Must return a clean 400 instead.
func TestAddChannel_NullChannelBody(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelRoutes(ctx)

	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{name: "explicit null channel", body: map[string]interface{}{"mode": "single", "channel": nil}},
		{name: "missing channel field", body: map[string]interface{}{"mode": "single"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := V2Request(ctx.Router, http.MethodPost, "/api/channel", tt.body, nil)
			AssertV2Status(t, w, http.StatusBadRequest)
			resp := ParseV2Response(t, w)
			if success, _ := resp["success"].(bool); success {
				t.Errorf("expected success=false, body: %s", w.Body.String())
			}
		})
	}
}

// TestCopyChannel_ReturnsNewId guards against the clone-by-value bug where the
// insert wrote the auto-generated id into a slice copy and the response always
// carried data.id=0.
func TestCopyChannel_ReturnsNewId(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	registerV1ChannelRoutes(ctx)

	origin := SeedV2Channel(t, ctx, "Copy Source")

	w := V2Request(ctx.Router, http.MethodPost, fmt.Sprintf("/api/channel/copy/%d", origin.Id), nil, nil)
	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body: %s", w.Body.String())
	}
	newID := int(data["id"].(float64))
	if newID == 0 {
		t.Fatal("expected non-zero new channel id in response")
	}
	if newID == origin.Id {
		t.Fatalf("expected a new id, got the origin id %d", origin.Id)
	}

	// The returned id must reference the actually inserted clone row.
	var cloned struct{ Name string }
	if err := ctx.DB.Table("channels").Where("id = ?", newID).Select("name").First(&cloned).Error; err != nil {
		t.Fatalf("clone row not found for returned id %d: %v", newID, err)
	}
	if cloned.Name != origin.Name+"_复制" {
		t.Errorf("expected cloned name %q, got %q", origin.Name+"_复制", cloned.Name)
	}
}
