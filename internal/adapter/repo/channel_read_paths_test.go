package repo

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func seedReadChannel(t *testing.T, tenant, name, key string, status int) *Channel {
	t.Helper()
	ch := &Channel{Name: name, Key: key, TenantId: tenant, Status: status, Type: 1}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return ch
}

func TestChannelsForMonitoring_DBPathIncludesDisabled(t *testing.T) {
	defer setupSQLiteDB(t)()
	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	defer func() { common.MemoryCacheEnabled = prev }()

	seedReadChannel(t, "default", "on", "k1", common.ChannelStatusEnabled)
	seedReadChannel(t, "default", "off", "k2", common.ChannelStatusManuallyDisabled)

	got, err := ChannelsForMonitoring()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (disabled channels must be included)", len(got))
	}
}

func TestChannelsForMonitoring_MemoryCachePath(t *testing.T) {
	prev := common.MemoryCacheEnabled
	channelSyncLock.Lock()
	prevIDM := channelsIDM
	channelsIDM = map[int]*Channel{7: {Id: 7, Name: "cached"}, 9: {Id: 9, Name: "cached-off", Status: common.ChannelStatusManuallyDisabled}}
	channelSyncLock.Unlock()
	common.MemoryCacheEnabled = true
	defer func() {
		common.MemoryCacheEnabled = prev
		channelSyncLock.Lock()
		channelsIDM = prevIDM
		channelSyncLock.Unlock()
	}()

	got, err := ChannelsForMonitoring()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int]bool{}
	for _, c := range got {
		ids[c.Id] = true
	}
	if len(got) != 2 || !ids[7] || !ids[9] {
		t.Fatalf("got %v, want ids 7 and 9", ids)
	}
}

func TestListTenantChannelsWithKeys(t *testing.T) {
	defer setupSQLiteDB(t)()
	a1 := seedReadChannel(t, "ta", "a1", "k-a1\nk-a1b", 1)
	a2 := seedReadChannel(t, "ta", "a2", "k-a2", 1)
	b1 := seedReadChannel(t, "tb", "b1", "k-b1", 1)

	all, err := ListTenantChannelsWithKeys("ta", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Id != a2.Id || all[1].Id != a1.Id {
		t.Fatalf("want [a2,a1] id desc, got %+v", all)
	}
	if all[1].Key != "k-a1\nk-a1b" {
		t.Fatalf("key column must be loaded, got %q", all[1].Key)
	}

	// tenant isolation even when a foreign id is requested explicitly
	got, err := ListTenantChannelsWithKeys("ta", []int{a1.Id, b1.Id}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Id != a1.Id {
		t.Fatalf("foreign id leaked: %+v", got)
	}

	// empty (non-nil) ids = nothing, not "everything"
	got, err = ListTenantChannelsWithKeys("ta", []int{}, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty ids: got %d rows err %v, want 0", len(got), err)
	}

	got, err = ListTenantChannelsWithKeys("ta", nil, 1)
	if err != nil || len(got) != 1 || got[0].Id != a2.Id {
		t.Fatalf("limit 1: %+v err %v", got, err)
	}

	got, _ = ListTenantChannelsWithKeys("nobody", nil, 0)
	if len(got) != 0 {
		t.Fatalf("unknown tenant returned %d rows", len(got))
	}
}

func TestListChannelTemplateApplications(t *testing.T) {
	defer setupSQLiteDB(t)()
	if err := DB.AutoMigrate(&ChannelTemplateApplication{}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		DB.Create(&ChannelTemplateApplication{TemplateId: 5, ChannelId: int64(i), AppliedAt: int64(i)})
	}
	DB.Create(&ChannelTemplateApplication{TemplateId: 6, ChannelId: 99})

	rows, total, err := ListChannelTemplateApplications(5, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(rows) != 3 || rows[0].ChannelId != 3 || rows[2].ChannelId != 1 {
		t.Fatalf("total=%d rows=%+v, want 3 newest-first", total, rows)
	}

	rows, total, _ = ListChannelTemplateApplications(5, 1, 1)
	if total != 3 || len(rows) != 1 || rows[0].ChannelId != 2 {
		t.Fatalf("page: total=%d rows=%+v, want middle row", total, rows)
	}

	rows, total, err = ListChannelTemplateApplications(404, 0, 10)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("unknown template: %d %d %v", total, len(rows), err)
	}
}

func TestAuditTenant_RequiresTenant(t *testing.T) {
	defer setupSQLiteDB(t)()
	if _, _, err := ListTenantAuditEvents("", "", 0, 0, 0, 0, 10); !errors.Is(err, ErrAuditTenantRequired) {
		t.Fatalf("list err = %v", err)
	}
	if _, _, err := ExportTenantAuditEvents("", 0, "", 0, 0, 0, 10); !errors.Is(err, ErrAuditTenantRequired) {
		t.Fatalf("export err = %v", err)
	}
}

func seedAudit(t *testing.T, tenant, action string, actor int, ts int64) int64 {
	t.Helper()
	e := &entity.AuditEvent{TenantID: tenant, Action: action, ActorID: actor, Timestamp: ts, Resource: "token"}
	if err := DB.Create(e).Error; err != nil {
		t.Fatalf("seed audit: %v", err)
	}
	return e.ID
}

func TestListTenantAuditEvents_PinnedToTenant(t *testing.T) {
	defer setupSQLiteDB(t)()
	seedAudit(t, "ta", "x.a", 1, 100)
	seedAudit(t, "ta", "x.b", 2, 200)
	seedAudit(t, "tb", "x.a", 1, 100)

	ev, total, err := ListTenantAuditEvents("ta", "", 0, 0, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(ev) != 2 {
		t.Fatalf("total=%d len=%d, want 2 (tb row must not leak)", total, len(ev))
	}
	for _, e := range ev {
		if e.TenantID != "ta" {
			t.Fatalf("foreign row: %+v", e)
		}
	}
	_, total, _ = ListTenantAuditEvents("ta", "x.b", 0, 0, 0, 0, 10)
	if total != 1 {
		t.Fatalf("action filter total = %d", total)
	}
}

func TestExportTenantAuditEvents_CursorAndFilters(t *testing.T) {
	defer setupSQLiteDB(t)()
	var ids []int64
	for i := 0; i < 5; i++ {
		ids = append(ids, seedAudit(t, "ta", "act", 1, int64(100+i)))
	}
	seedAudit(t, "tb", "act", 1, 100)
	otherActor := seedAudit(t, "ta", "other", 2, 500)

	// page 1: limit 2 over the 6 ta rows -> next cursor is the 3rd row id
	ev, next, err := ExportTenantAuditEvents("ta", 0, "", 0, 0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 2 || ev[0].ID != ids[0] || ev[1].ID != ids[1] || next != ids[2] {
		t.Fatalf("page1 len=%d next=%d, want ids %v next %d", len(ev), next, ids[:2], ids[2])
	}
	seen := len(ev)
	for next != 0 {
		ev, next, err = ExportTenantAuditEvents("ta", next, "", 0, 0, 0, 2)
		if err != nil {
			t.Fatal(err)
		}
		seen += len(ev)
	}
	if seen != 6 {
		t.Fatalf("cursor walk saw %d rows, want 6", seen)
	}

	ev, next, _ = ExportTenantAuditEvents("ta", 0, "other", 0, 0, 0, 0)
	if len(ev) != 1 || ev[0].ID != otherActor || next != 0 {
		t.Fatalf("action filter: %+v next %d", ev, next)
	}
	ev, _, _ = ExportTenantAuditEvents("ta", 0, "", 2, 0, 0, 10)
	if len(ev) != 1 || ev[0].ActorID != 2 {
		t.Fatalf("actor filter: %+v", ev)
	}
	ev, _, _ = ExportTenantAuditEvents("ta", 0, "", 0, 102, 103, 10)
	if len(ev) != 2 {
		t.Fatalf("time window returned %d rows, want 2", len(ev))
	}
	if _, _, err := ExportTenantAuditEvents("ta", 0, "", 0, 0, 0, 999999); err != nil {
		t.Fatal(err)
	}
}
