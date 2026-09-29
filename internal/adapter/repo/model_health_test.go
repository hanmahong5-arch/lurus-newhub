package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func setupModelHealthDB(t *testing.T) {
	t.Helper()
	t.Cleanup(setupPoolTestDB(t))
	if err := DB.AutoMigrate(&entity.ModelHealth{}); err != nil {
		t.Fatalf("automigrate model_health: %v", err)
	}
}

func TestModelHealth_SaveUpsertsByChannelAndModel(t *testing.T) {
	setupModelHealthDB(t)

	if got, err := GetModelHealth(1, "m:free"); err != nil || got != nil {
		t.Fatalf("unprobed pair: got %+v err %v, want nil nil", got, err)
	}
	row := &entity.ModelHealth{ChannelId: 1, Model: "m:free", LastProbeAt: 100, Ok: false, ConsecutiveFailures: 1, LastError: "boom"}
	if err := SaveModelHealth(row); err != nil {
		t.Fatalf("insert: %v", err)
	}
	row2 := &entity.ModelHealth{ChannelId: 1, Model: "m:free", LastProbeAt: 200, Ok: false, ConsecutiveFailures: 3, AutoDisabled: true, AutoDisabledAt: 200}
	if err := SaveModelHealth(row2); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := GetModelHealth(1, "m:free")
	if err != nil || got == nil {
		t.Fatalf("get after upsert: %+v %v", got, err)
	}
	if got.ConsecutiveFailures != 3 || !got.AutoDisabled || got.LastProbeAt != 200 {
		t.Fatalf("upsert did not overwrite: %+v", got)
	}
	var n int64
	DB.Model(&entity.ModelHealth{}).Count(&n)
	if n != 1 {
		t.Fatalf("rows = %d, want 1 (upsert, not insert)", n)
	}
}

func TestModelHealth_LoadAutoDisabledAndList(t *testing.T) {
	setupModelHealthDB(t)
	for _, r := range []entity.ModelHealth{
		{ChannelId: 1, Model: "a", AutoDisabled: true},
		{ChannelId: 1, Model: "b", AutoDisabled: false},
		{ChannelId: 2, Model: "a", AutoDisabled: true},
	} {
		r := r
		if err := SaveModelHealth(&r); err != nil {
			t.Fatal(err)
		}
	}
	pairs, err := LoadAutoDisabledModelPairs()
	if err != nil {
		t.Fatal(err)
	}
	if !pairs[1]["a"] || pairs[1]["b"] || !pairs[2]["a"] || len(pairs) != 2 {
		t.Fatalf("pairs = %v", pairs)
	}
	rows, err := ListModelHealth([]int{1})
	if err != nil || len(rows) != 2 || rows[0].Model != "a" {
		t.Fatalf("list channel 1: %+v %v", rows, err)
	}
	if err := DeleteModelHealthForChannel(1); err != nil {
		t.Fatal(err)
	}
	rows, _ = ListModelHealth(nil)
	if len(rows) != 1 || rows[0].ChannelId != 2 {
		t.Fatalf("after delete: %+v", rows)
	}
}

func TestModelHealth_SaveRejectsMissingKey(t *testing.T) {
	setupModelHealthDB(t)
	if err := SaveModelHealth(&entity.ModelHealth{Model: "x"}); err == nil {
		t.Fatal("want error without channel_id")
	}
	if err := SaveModelHealth(nil); err == nil {
		t.Fatal("want error on nil")
	}
}
