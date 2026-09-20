package handler

// health_draining_test.go — oracle for cycle 13 L11's readiness half of the
// graceful-drain contract. callHealth/healthSnapshotAll come from
// health_test.go (same package).

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/lifecycle"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestGetHealthDetailed_Draining_Returns503 locks the readiness contract: once
// the process-wide Drainer is marked draining, /api/health must answer 503
// {"status":"draining"} immediately, before it even looks at the DB/Redis/
// billing checks below — repo.DB is left nil here (which on its own would be
// the intentional-off-state "not_configured", HTTP 200) specifically so a
// 200 in this test could only mean the draining branch was skipped.
func TestGetHealthDetailed_Draining_Returns503(t *testing.T) {
	healthSnapshotAll(t)
	repo.DB = nil
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(false)

	if lifecycle.IsDraining() {
		t.Fatal("test isolation broken: process-wide Drainer was already draining at test start")
	}
	lifecycle.MarkDraining()
	t.Cleanup(func() { lifecycle.Default().ResetForTest() })

	w, _ := callHealth(t)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 while draining, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse health body: %v, raw=%s", err, w.Body.String())
	}
	if body.Status != "draining" {
		t.Errorf("status = %q, want draining", body.Status)
	}
	// The draining short-circuit must not carry over the normal "checks" map
	// at all — it is a fast bail-out before any dependency is even probed.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("parse health body as map: %v", err)
	}
	if _, present := raw["checks"]; present {
		t.Errorf("draining response carries a \"checks\" key, want the DB/Redis/billing probes skipped entirely: %s", w.Body.String())
	}
}

// TestGetHealthDetailed_NotDraining_StaysOnNormalPath is the negative
// counterpart: with the Drainer at its default (not draining) state, the
// existing all-ok path must still report 200 "healthy" — proves the gate
// added above is additive, not accidentally unconditional.
func TestGetHealthDetailed_NotDraining_StaysOnNormalPath(t *testing.T) {
	healthSnapshotAll(t)
	if lifecycle.IsDraining() {
		t.Fatal("test isolation broken: process-wide Drainer was already draining at test start")
	}
	repo.DB = nil // not_configured: an intentional off-state, still healthy
	common.RedisEnabled = false
	common.SetBillingUnifiedEnabled(false)

	w, _ := callHealth(t)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	status, _ := healthFullBody(t, w)
	if status != "healthy" {
		t.Errorf("status = %q, want healthy", status)
	}
}
