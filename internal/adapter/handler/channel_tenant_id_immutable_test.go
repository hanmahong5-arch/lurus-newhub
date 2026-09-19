package handler

// channel_tenant_id_immutable_test.go pins the ownership half of PUT
// /api/channel/ (cycle-12 plan §L5).
//
// PatchChannel embeds repo.Channel (channel.go:1252-1256) and
// entity/channel.go:16 gives tenant_id a json tag, so the field binds from the
// request body. enforceTenantScope only consults the STORED row's tenant, so a
// tenant admin could pass the scope check on a channel it owns and, in the
// same request, hand that channel to another tenant — or to the shared
// "default" pool, where every tenant routes through it.

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// tenantReassignAuditCapture collects audit events in memory so the test can
// read them without a migrated audit_events table.
type tenantReassignAuditCapture struct {
	mu     sync.Mutex
	events []*entity.AuditEvent
}

func (w *tenantReassignAuditCapture) CreateAuditEvent(event *entity.AuditEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event)
	return nil
}

func (w *tenantReassignAuditCapture) snapshot() []*entity.AuditEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*entity.AuditEvent, len(w.events))
	copy(out, w.events)
	return out
}

// captureChannelAudit pins a synchronous in-memory audit writer for the
// duration of the test. governance.AsyncGo is forced synchronous here too so
// the event is readable when the handler returns, and both are restored
// afterwards.
func captureChannelAudit(t *testing.T) *tenantReassignAuditCapture {
	t.Helper()
	prevAsync := governance.AsyncGo
	governance.AsyncGo = func(f func()) { f() }
	capture := &tenantReassignAuditCapture{}
	governance.SetAuditWriter(capture)
	t.Cleanup(func() {
		governance.AsyncGo = prevAsync
		// No getter for the previous writer exists; park a discard writer
		// bound to nothing, the same convention secure_verification_test.go
		// and internal_credit_pool_fund_test.go use.
		governance.SetAuditWriter(&tenantReassignAuditCapture{})
	})
	return capture
}

// updateChannelBody is a PUT /api/channel/ payload that touches no field
// channelWriteTouchesSensitiveField (channel_sensitive_write.go:89) treats as
// sensitive: no key, same type as the stored row, no base_url / overrides.
func updateChannelBody(ch *repo.Channel, tenantID string) map[string]interface{} {
	body := map[string]interface{}{
		"id":     ch.Id,
		"name":   ch.Name + "-renamed",
		"type":   ch.Type,
		"models": ch.Models,
		"group":  ch.Group,
	}
	if tenantID != "" {
		body["tenant_id"] = tenantID
	}
	return body
}

// TestV1UpdateChannel_TenantIdImmutableForNonRoot — role 10 cannot move a
// channel it owns into another tenant, and the attempt is audited.
func TestV1UpdateChannel_TenantIdImmutableForNonRoot(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	capture := captureChannelAudit(t)

	own := SeedV2Channel(t, ctx, "reassign-own")

	c, w := v1Ctx(http.MethodPut, "/api/channel/", updateChannelBody(own, idorVictimTenant),
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("update must still succeed for the fields the caller may change, code=%d body=%s", w.Code, w.Body.String())
	}

	stored := v1ReloadChannel(t, ctx, own.Id)
	if stored.TenantId != ctx.TenantID {
		t.Errorf("channel reassigned cross-tenant by request body: tenant_id=%q want %q", stored.TenantId, ctx.TenantID)
	}
	if stored.Name != own.Name+"-renamed" {
		t.Errorf("the non-ownership part of the update was dropped: name=%q", stored.Name)
	}

	found := false
	for _, e := range capture.snapshot() {
		if e.Action == governance.ActionChannelUpdated && strings.Contains(e.Details, "tenant_id_change_ignored") {
			found = true
			var details map[string]interface{}
			if err := json.Unmarshal([]byte(e.Details), &details); err != nil {
				t.Fatalf("audit details not JSON: %v — raw %s", err, e.Details)
			}
			if got, _ := details["requested_tenant_id"].(string); got != idorVictimTenant {
				t.Errorf("audit details requested_tenant_id=%v want %q", details["requested_tenant_id"], idorVictimTenant)
			}
		}
	}
	if !found {
		t.Errorf("no audit event recorded the ignored tenant reassignment, events=%+v", capture.snapshot())
	}
}

// TestV1UpdateChannel_TenantIdImmutable_DefaultPoolIsAlsoBlocked — moving a
// channel into the shared "default" pool is the same escalation (every tenant
// routes through shared channels), so it is refused the same way.
func TestV1UpdateChannel_TenantIdImmutable_DefaultPoolIsAlsoBlocked(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	captureChannelAudit(t)

	own := SeedV2Channel(t, ctx, "reassign-to-default")

	c, w := v1Ctx(http.MethodPut, "/api/channel/", updateChannelBody(own, "default"),
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("update must still succeed, code=%d body=%s", w.Code, w.Body.String())
	}
	if stored := v1ReloadChannel(t, ctx, own.Id); stored.TenantId != ctx.TenantID {
		t.Errorf("channel donated to the shared default pool: tenant_id=%q want %q", stored.TenantId, ctx.TenantID)
	}
}

// TestV1UpdateChannel_RootMayReassignTenant — the platform operator keeps the
// ability to move a channel between tenants, and no ignored-reassignment
// detail is stamped on that event.
func TestV1UpdateChannel_RootMayReassignTenant(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	capture := captureChannelAudit(t)

	own := SeedV2Channel(t, ctx, "reassign-by-root")

	c, w := v1Ctx(http.MethodPut, "/api/channel/", updateChannelBody(own, idorVictimTenant),
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	UpdateChannel(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("root update must succeed, code=%d body=%s", w.Code, w.Body.String())
	}
	if stored := v1ReloadChannel(t, ctx, own.Id); stored.TenantId != idorVictimTenant {
		t.Errorf("root reassignment did not take: tenant_id=%q want %q", stored.TenantId, idorVictimTenant)
	}
	for _, e := range capture.snapshot() {
		if strings.Contains(e.Details, "tenant_id_change_ignored") {
			t.Errorf("root's accepted reassignment was recorded as ignored: %s", e.Details)
		}
	}
}

// TestV1UpdateChannel_UnchangedTenantIdIsNotAudited — the console round-trips
// the row it just read, so a body that repeats the channel's own tenant_id is
// the normal case and must not produce a refusal detail.
func TestV1UpdateChannel_UnchangedTenantIdIsNotAudited(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	capture := captureChannelAudit(t)

	own := SeedV2Channel(t, ctx, "reassign-noop")

	c, w := v1Ctx(http.MethodPut, "/api/channel/", updateChannelBody(own, ctx.TenantID),
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	UpdateChannel(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("update must succeed, code=%d body=%s", w.Code, w.Body.String())
	}
	if stored := v1ReloadChannel(t, ctx, own.Id); stored.TenantId != ctx.TenantID {
		t.Errorf("tenant_id changed on a no-op round-trip: %q", stored.TenantId)
	}
	for _, e := range capture.snapshot() {
		if strings.Contains(e.Details, "tenant_id_change_ignored") {
			t.Errorf("a round-trip of the channel's own tenant_id was audited as a refusal: %s", e.Details)
		}
	}
}
