package repo

// l2_log_stat_period_pg_test.go — regression coverage for the D3 defect:
// GetUserLogStatByPeriod bound a time.Time directly to the unix-epoch bigint
// column `created_at` (entity.Log.CreatedAt), which PostgreSQL rejects with
// 22P02 (invalid_text_representation) — /v1/billing/usage returned an
// unconditional 500 for every customer. This MUST run on the PG tier
// (SetupTestDB), never SQLite: glebarez SQLite's type coercion is lenient
// enough that binding a time.Time to an integer column silently "works",
// which is exactly why this defect shipped and lived undetected.
//
// It also locks the `AND type = ?` (LogTypeConsume) filter in
// GetUserLogStatByPeriod (E1, log.go:788-805): the comment that filter used
// to carry ("topup rows carry a huge, non-usage quota") was false —
// RecordLog/RecordLogWithTenant (log.go:199-220/223-242) never set Quota on
// a Log{}, so production topup/manage rows have Quota==0, not a huge value.
// The real reasons are (a) those rows also have an empty model_name, which
// would add a spurious "" group to a model-grouped result, and (b)
// LogTypeError rows DO carry a real ModelName but were never billed, which
// would silently inflate that model's count. The seed data below matches
// what RecordLog actually writes (Quota=0, ModelName="" on the topup row)
// instead of an unrealistic huge quota, so this test exercises the true
// failure mode rather than the one the old, disproven comment described.

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

func TestL2GetUserLogStatByPeriod_PG(t *testing.T) {
	SetupTestDB(t)

	u := seedUser(t, "l2-logstat-user", "l2-logstat@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")

	now := common.GetTimestamp()

	logs := []*Log{
		{UserId: u.Id, Username: u.Username, Type: LogTypeConsume, ModelName: "m", Quota: 100, Content: "c1", CreatedAt: now},
		// Matches what RecordLog/RecordLogWithTenant actually write for a
		// topup/manage row: ModelName="" and Quota left at its zero value —
		// not the "huge quota" the old (false) comment described.
		{UserId: u.Id, Username: u.Username, Type: LogTypeTopup, ModelName: "", Quota: 0, Content: "topup", CreatedAt: now},
		// A real, non-billed error row sharing the consume row's model name.
		// If the `type = ?` filter were dropped, this would double count("m").
		{UserId: u.Id, Username: u.Username, Type: LogTypeError, ModelName: "m", Quota: 0, Content: "err", CreatedAt: now},
	}
	for _, l := range logs {
		if err := DB.Create(l).Error; err != nil {
			t.Fatalf("seed log %q: %v", l.Content, err)
		}
	}

	since := time.Unix(now-7*24*60*60, 0)

	stats, err := GetUserLogStatByPeriod(u.Id, since)
	if err != nil {
		t.Fatalf("GetUserLogStatByPeriod: %v (this is the 22P02 defect if non-nil)", err)
	}

	// Neither the empty-model-name topup row nor the LogTypeError row must
	// leak into the result: exactly one group, key="m", count=1.
	if len(stats) != 1 {
		t.Fatalf("expected exactly 1 grouped row (m), got %d: %+v", len(stats), stats)
	}
	if stats[0].Key != "m" {
		t.Errorf("stats[0].Key = %q, want %q", stats[0].Key, "m")
	}
	if stats[0].Count != 1 {
		t.Errorf("stats[0].Count = %d, want 1 (the topup and error rows must not be counted)", stats[0].Count)
	}
	if stats[0].TotalQuota != 100 {
		t.Errorf("stats[0].TotalQuota = %d, want 100", stats[0].TotalQuota)
	}
}
