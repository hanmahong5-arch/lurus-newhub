package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func TestModelRateLimit_UpsertListDelete(t *testing.T) {
	defer setupSQLiteDB(t)()
	if err := DB.AutoMigrate(&entity.ModelRateLimit{}); err != nil {
		t.Fatal(err)
	}

	first, err := UpsertModelRateLimit("ta", "model-b", 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	if first.RateLimitRPM != 10 || first.RateLimitTPM != 100 || first.Id == 0 {
		t.Fatalf("created row: %+v", first)
	}
	// second upsert of the same key updates in place, never duplicates
	second, err := UpsertModelRateLimit("ta", "model-b", 20, 200)
	if err != nil {
		t.Fatal(err)
	}
	if second.Id != first.Id || second.RateLimitRPM != 20 || second.RateLimitTPM != 200 {
		t.Fatalf("upsert did not update in place: %+v vs %+v", first, second)
	}
	if _, err := UpsertModelRateLimit("ta", "model-a", 1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertModelRateLimit("tb", "model-a", 7, 7); err != nil {
		t.Fatal(err)
	}

	rows, err := ListModelRateLimits("ta")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Model != "model-a" || rows[1].Model != "model-b" {
		t.Fatalf("want [model-a model-b] for ta only, got %+v", rows)
	}

	ok, err := DeleteModelRateLimit("ta", "model-a")
	if err != nil || !ok {
		t.Fatalf("delete existing: %v %v", ok, err)
	}
	ok, err = DeleteModelRateLimit("ta", "model-a")
	if err != nil || ok {
		t.Fatalf("delete absent must be (false,nil): %v %v", ok, err)
	}
	if rows, _ = ListModelRateLimits("tb"); len(rows) != 1 {
		t.Fatalf("tenant tb row was affected by ta delete: %+v", rows)
	}
}

func TestCheckoutOrder_RecordAndOwner(t *testing.T) {
	defer setupSQLiteDB(t)()
	if err := DB.AutoMigrate(&BillingCheckoutOrder{}); err != nil {
		t.Fatal(err)
	}
	if err := RecordCheckoutOrder("", 1, 9.9, 1); err == nil {
		t.Fatal("empty order_no must be rejected")
	}
	if err := RecordCheckoutOrder("ord-1", 11, 9.9, 100); err != nil {
		t.Fatal(err)
	}
	// retry with a different account must not reassign ownership
	if err := RecordCheckoutOrder("ord-1", 22, 9.9, 101); err != nil {
		t.Fatal(err)
	}
	acc, found, err := CheckoutOrderAccount("ord-1")
	if err != nil || !found || acc != 11 {
		t.Fatalf("owner = %d found=%v err=%v, want 11", acc, found, err)
	}
	acc, found, err = CheckoutOrderAccount("ord-unknown")
	if err != nil || found || acc != 0 {
		t.Fatalf("unknown order must be (0,false,nil), got %d %v %v", acc, found, err)
	}
}
