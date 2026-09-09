package tenantpolicy

// allowlist_test.go — hermetic (glebarez sqlite, no TEST_POSTGRES_DSN
// needed) coverage of matching, the TTL cache, Invalidate and Mode parsing,
// plus the L1 operator-amendment lock: a missing tenant_configs row must
// come back with a nil error (fail-open must be silent for the common
// "never configured" case, not the "config not found" fault path).

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var tpDBCounter atomic.Int64

func setupTenantPolicyDB(t *testing.T) func() {
	t.Helper()
	dbName := fmt.Sprintf("file:tenantpolicy_test_%d?mode=memory&cache=shared", tpDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.TenantConfig{}); err != nil {
		t.Fatalf("migrate TenantConfig: %v", err)
	}
	prevDB := repo.DB
	repo.DB = db
	return func() {
		repo.DB = prevDB
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

func TestModelAllowed_ExactWildcardAndNormalisation(t *testing.T) {
	cases := []struct {
		name  string
		list  []string
		model string
		want  bool
	}{
		{"exact match", []string{"gpt-4o"}, "gpt-4o", true},
		{"exact mismatch", []string{"gpt-4o"}, "gpt-4o-mini", false},
		{"wildcard prefix match", []string{"gemini-2.5-flash-*"}, "gemini-2.5-flash-thinking-4096", true},
		{"wildcard prefix no match", []string{"claude-*"}, "gpt-4o", false},
		{"empty list denies everything", []string{}, "gpt-4o", false},
		{"blank entries skipped, not treated as wildcard-all", []string{"  ", ""}, "gpt-4o", false},
		{"multiple entries, second matches", []string{"claude-*", "gpt-4o"}, "gpt-4o", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ModelAllowed(tc.list, tc.model); got != tc.want {
				t.Errorf("ModelAllowed(%v, %q) = %v, want %v", tc.list, tc.model, got, tc.want)
			}
		})
	}
}

func TestLoadModelAllowlist_MissingRow_NoError(t *testing.T) {
	cleanup := setupTenantPolicyDB(t)
	defer cleanup()

	list, configured, err := LoadModelAllowlist("tenant-never-configured")
	if err != nil {
		t.Fatalf("err = %v, want nil — a never-configured tenant is not a fault", err)
	}
	if configured {
		t.Errorf("configured = true, want false for a missing row")
	}
	if list != nil {
		t.Errorf("list = %v, want nil", list)
	}
}

func TestLoadModelAllowlist_ConfiguredRow(t *testing.T) {
	cleanup := setupTenantPolicyDB(t)
	defer cleanup()

	if err := repo.SetTenantConfigJSON("tenant-a", ModelAllowlistConfigKey, []string{"gpt-4o", "claude-*"}, ""); err != nil {
		t.Fatalf("seed allow-list: %v", err)
	}

	list, configured, err := LoadModelAllowlist("tenant-a")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !configured {
		t.Fatal("configured = false, want true")
	}
	if len(list) != 2 || list[0] != "gpt-4o" || list[1] != "claude-*" {
		t.Errorf("list = %v, want [gpt-4o claude-*]", list)
	}
}

func TestLoadModelAllowlist_DenyAll(t *testing.T) {
	cleanup := setupTenantPolicyDB(t)
	defer cleanup()

	if err := repo.SetTenantConfigJSON("tenant-deny", ModelAllowlistConfigKey, []string{}, ""); err != nil {
		t.Fatalf("seed empty allow-list: %v", err)
	}

	list, configured, err := LoadModelAllowlist("tenant-deny")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !configured {
		t.Fatal("configured = false, want true for an explicit []")
	}
	if len(list) != 0 {
		t.Errorf("list = %v, want empty", list)
	}
	if ModelAllowed(list, "gpt-4o") {
		t.Error("ModelAllowed with an explicit [] allow-list = true, want false (deny all)")
	}
}

func TestLoadModelAllowlist_CachesWithinTTLAndInvalidates(t *testing.T) {
	cleanup := setupTenantPolicyDB(t)
	defer cleanup()

	if err := repo.SetTenantConfigJSON("tenant-cache", ModelAllowlistConfigKey, []string{"m1"}, ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	list, configured, err := LoadModelAllowlist("tenant-cache")
	if err != nil || !configured || len(list) != 1 || list[0] != "m1" {
		t.Fatalf("initial load = %v/%v/%v, want [m1]/true/nil", list, configured, err)
	}

	// Write a new value directly (bypassing Invalidate) — the cached read
	// must still see the OLD value within the TTL window.
	if err := repo.SetTenantConfigJSON("tenant-cache", ModelAllowlistConfigKey, []string{"m2"}, ""); err != nil {
		t.Fatalf("update: %v", err)
	}
	list, _, _ = LoadModelAllowlist("tenant-cache")
	if len(list) != 1 || list[0] != "m1" {
		t.Fatalf("cached load after uninvalidated write = %v, want [m1] (still cached)", list)
	}

	// Invalidate makes the next read see the new value immediately, without
	// waiting out the TTL.
	Invalidate("tenant-cache")
	list, _, _ = LoadModelAllowlist("tenant-cache")
	if len(list) != 1 || list[0] != "m2" {
		t.Fatalf("load after Invalidate = %v, want [m2]", list)
	}
}

func TestLoadModelAllowlist_EmptyTenantID(t *testing.T) {
	cleanup := setupTenantPolicyDB(t)
	defer cleanup()

	list, configured, err := LoadModelAllowlist("")
	if err != nil || configured || list != nil {
		t.Errorf("LoadModelAllowlist(\"\") = %v/%v/%v, want nil/false/nil", list, configured, err)
	}
}

func TestMode_OnlyLiteralEnforceEnforces(t *testing.T) {
	cases := []struct {
		env  string
		want string
	}{
		{"", ModeObserve},
		{"enforce", ModeEnforce},
		{"Enforce", ModeObserve},
		{"ENFORCE", ModeObserve},
		{"garbage", ModeObserve},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("env=%q", tc.env), func(t *testing.T) {
			t.Setenv("TENANT_MODEL_ALLOWLIST_MODE", tc.env)
			if got := Mode(); got != tc.want {
				t.Errorf("Mode() with env=%q = %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}

func TestLoadModelAllowlist_RealDBErrorFailsOpenWithError(t *testing.T) {
	cleanup := setupTenantPolicyDB(t)
	defer cleanup()

	// Close the DB out from under repo.DB so GetTenantConfig hits a real
	// error other than record-not-found — the caller must get a non-nil err
	// (fail-open + log branch), not silently treated as "unconfigured".
	sqlDB, err := repo.DB.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	_ = sqlDB.Close()

	_, configured, err := LoadModelAllowlist("tenant-broken-db")
	if configured {
		t.Error("configured = true on a DB error, want false")
	}
	if err == nil {
		t.Fatal("err = nil on a closed DB, want a non-nil error")
	}
	if errors.Is(err, repo.ErrTenantConfigNotFound) {
		t.Error("err wraps ErrTenantConfigNotFound on a DB fault — that sentinel must be reserved for the missing-row case")
	}
}
