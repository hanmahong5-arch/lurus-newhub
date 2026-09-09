package repo

// tenant_config_test.go — L1 operator amendment: GetTenantConfig re-wrapped
// gorm.ErrRecordNotFound into a fresh errors.New, so no caller could tell a
// missing row from a DB fault; tenantpolicy (added in the same change)
// relies on this exported sentinel. This locks the sentinel so a future
// edit that reverts to a bare errors.New goes red here instead of silently
// reopening the fail-open gap.

import (
	"errors"
	"testing"
)

func TestGetTenantConfig_MissingRow_ReturnsSentinel(t *testing.T) {
	SetupTestDB(t)

	_, err := GetTenantConfig("no-such-tenant-config-test", "no.such.key")
	if err == nil {
		t.Fatal("expected an error for a missing config row, got nil")
	}
	if !errors.Is(err, ErrTenantConfigNotFound) {
		t.Fatalf("err = %v, want errors.Is(err, ErrTenantConfigNotFound) — callers use errors.Is to distinguish missing-row from a real DB fault", err)
	}
}
