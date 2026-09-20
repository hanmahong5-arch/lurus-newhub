package handler

// channel_key_reveal_audit_test.go — cycle 13 L4 (V1DOORS/SECURITY-14) oracle:
// GetChannelKey (POST /api/channel/:id/key) reveals an upstream provider
// secret and previously left only a logs row (repo.RecordLog) — a row that
// DELETE /api/log/ can itself remove. This asserts the separate,
// non-purgeable audit_events row it now also writes.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// TestGetChannelKey_RecordsChannelKeyAccessedAudit is the L4 plan's named
// oracle: root POST /api/channel/:id/key produces exactly one
// security.channel_key_accessed row whose Details carry the channel id and
// tenant but never the key itself (the key only ever appears in the JSON
// response body, gated by RootAuth + SecureVerificationRequired upstream of
// this handler in production).
func TestGetChannelKey_RecordsChannelKeyAccessedAudit(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil {
		t.Fatalf("auto migrate audit tables: %v", err)
	}
	pinAuditWriter(t, ctx.DB)

	ch := SeedV2Channel(t, ctx, "reveal-target")

	c, w := v1Ctx(http.MethodPost, fmt.Sprintf("/api/channel/%d/key", ch.Id), nil,
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ch.Id)}}
	GetChannelKey(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := v1Body(t, w)
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body=%s", w.Body.String())
	}
	returnedKey, _ := data["key"].(string)
	if returnedKey != ch.Key {
		t.Fatalf("returned key = %q, want %q", returnedKey, ch.Key)
	}

	events, _, err := repo.GetAuditEvents("", governance.ActionChannelKeyAccessed, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 security.channel_key_accessed row, got %d", len(events))
	}
	ev := events[0]
	if ev.Resource != governance.ResourceChannel {
		t.Errorf("Resource = %q, want %q", ev.Resource, governance.ResourceChannel)
	}
	if ev.ResourceID != ch.Id {
		t.Errorf("ResourceID = %d, want %d", ev.ResourceID, ch.Id)
	}
	if ev.ActorID != ctx.RootUser.Id {
		t.Errorf("ActorID = %d, want %d", ev.ActorID, ctx.RootUser.Id)
	}
	if strings.Contains(ev.Details, ch.Key) {
		t.Fatalf("audit Details leaked the channel key: %s", ev.Details)
	}
	if !strings.Contains(ev.Details, ctx.TenantID) {
		t.Errorf("Details missing tenant id: %s", ev.Details)
	}
	if !strings.Contains(ev.Details, fmt.Sprintf("%d", ch.Id)) {
		t.Errorf("Details missing channel id: %s", ev.Details)
	}
}

// TestGetChannelKey_UnknownChannel_NoAuditEvent pins the negative case: a
// lookup failure must not produce a security.channel_key_accessed row — the
// action name promises the key was actually accessed.
func TestGetChannelKey_UnknownChannel_NoAuditEvent(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil {
		t.Fatalf("auto migrate audit tables: %v", err)
	}
	pinAuditWriter(t, ctx.DB)

	const missingID = 999999
	c, w := v1Ctx(http.MethodPost, fmt.Sprintf("/api/channel/%d/key", missingID), nil,
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(missingID)}}
	GetChannelKey(c)

	body := v1Body(t, w)
	if success, _ := body["success"].(bool); success {
		t.Fatalf("expected success=false for an unknown channel id, body=%s", w.Body.String())
	}

	events, _, err := repo.GetAuditEvents("", governance.ActionChannelKeyAccessed, 0, "", 0, 0, 0, 10)
	if err != nil {
		t.Fatalf("GetAuditEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 security.channel_key_accessed rows for a failed lookup, got %d", len(events))
	}
}
