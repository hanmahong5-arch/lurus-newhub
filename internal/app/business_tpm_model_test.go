package app

// business_tpm_model_test.go — hermetic coverage for the (tenant, model) TPM
// window dimension (RecordBusinessTPMModelUsage / QueryBusinessTPMModelWindow):
// both backends, window sliding via the BizTPMNow seam, attribution skips, and
// isolation between models and tenants. Helpers (freezeBizTPMClock,
// withoutRedisTPM, withMiniRedisTPM) live in business_tpm_test.go — same
// package; unique tenant/model names per test keep -count=1 safe.

import (
	"context"
	"testing"
	"time"
)

func TestBusinessTPMModel_MemoryRecordQuerySlide(t *testing.T) {
	withoutRedisTPM(t)
	advance := freezeBizTPMClock(t)
	tenant, model := "tpm-model-mem-tenant", "m-alpha"

	RecordBusinessTPMModelUsage(tenant, model, 120)
	advance(10 * time.Second)
	RecordBusinessTPMModelUsage(tenant, model, 80)

	total, oldestMs, err := QueryBusinessTPMModelWindow(context.Background(), tenant, model)
	if err != nil {
		t.Fatalf("model query: %v", err)
	}
	if total != 200 {
		t.Errorf("model window total = %d, want 200", total)
	}
	if oldestMs == 0 {
		t.Error("oldestMs = 0, want the first record's timestamp")
	}

	// Other models and other tenants have their own windows.
	if got, _, _ := QueryBusinessTPMModelWindow(context.Background(), tenant, "m-other"); got != 0 {
		t.Errorf("other model window = %d, want 0 (windows must isolate per model)", got)
	}
	if got, _, _ := QueryBusinessTPMModelWindow(context.Background(), "tpm-model-mem-other", model); got != 0 {
		t.Errorf("other tenant window = %d, want 0 (windows must isolate per tenant)", got)
	}

	// Slide: after 55 more seconds the first record (65s old) is pruned.
	advance(55 * time.Second)
	if got, _, _ := QueryBusinessTPMModelWindow(context.Background(), tenant, model); got != 80 {
		t.Errorf("window after partial slide = %d, want 80", got)
	}
	advance(61 * time.Second)
	if got, oldest, _ := QueryBusinessTPMModelWindow(context.Background(), tenant, model); got != 0 || oldest != 0 {
		t.Errorf("window after full slide = (%d, %d), want (0, 0)", got, oldest)
	}
}

func TestBusinessTPMModel_RedisBackend(t *testing.T) {
	withMiniRedisTPM(t)
	freezeBizTPMClock(t)
	tenant, model := "tpm-model-redis-tenant", "m-beta"

	RecordBusinessTPMModelUsage(tenant, model, 30)
	RecordBusinessTPMModelUsage(tenant, model, 30) // same frozen instant — seq must keep both

	total, oldestMs, err := QueryBusinessTPMModelWindow(context.Background(), tenant, model)
	if err != nil {
		t.Fatalf("model query: %v", err)
	}
	if total != 60 {
		t.Errorf("model window total = %d, want 60 (same-instant records must not collapse)", total)
	}
	if oldestMs == 0 {
		t.Error("oldestMs = 0, want the record timestamp")
	}
	if got, _, _ := QueryBusinessTPMModelWindow(context.Background(), tenant, "m-gamma"); got != 0 {
		t.Errorf("other model window = %d, want 0", got)
	}
}

func TestRecordBusinessTPMModelUsage_AttributionSkips(t *testing.T) {
	withoutRedisTPM(t)
	freezeBizTPMClock(t)
	tenant, model := "tpm-model-skip-tenant", "m-skip"

	RecordBusinessTPMModelUsage(tenant, model, 0)   // zero => skip
	RecordBusinessTPMModelUsage(tenant, model, -50) // refund => skip
	RecordBusinessTPMModelUsage("", model, 100)     // no tenant => unattributable
	RecordBusinessTPMModelUsage(tenant, "", 100)    // no model => unattributable

	if got, _, _ := QueryBusinessTPMModelWindow(context.Background(), tenant, model); got != 0 {
		t.Errorf("window after skipped records = %d, want 0", got)
	}
}
