package repo

// response_registry_test.go — oracle tests for the response_registry
// persistence functions (migration 036, cycle-8 L7). Drives the real
// Upsert/Get/Delete/Sweep functions against a hermetic SQLite DB, not
// hand-built entity.ResponseRegistry structs standing in for the code under
// test.

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

func TestResponseRegistryRepo_UpsertGetDelete(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	row := &entity.ResponseRegistry{
		ResponseId:    "resp_repo_1",
		TenantId:      "lurus",
		UserId:        7,
		TokenId:       42,
		ChannelId:     3,
		UpstreamModel: "gpt-4o-mini",
		CreatedAt:     1000,
		ExpiresAt:     2000,
	}
	if err := UpsertResponseRegistry(row); err != nil {
		t.Fatalf("UpsertResponseRegistry: %v", err)
	}

	got, err := GetResponseRegistry("resp_repo_1")
	if err != nil {
		t.Fatalf("GetResponseRegistry: %v", err)
	}
	if got.TenantId != "lurus" || got.UserId != 7 || got.ChannelId != 3 || got.UpstreamModel != "gpt-4o-mini" {
		t.Errorf("GetResponseRegistry row = %+v, want the seeded fields", got)
	}

	// Re-upsert with a different channel/model — the UPDATE branch of the
	// upsert, not a second INSERT (which would violate the PRIMARY KEY).
	row.ChannelId = 9
	row.UpstreamModel = "gpt-4o"
	if err := UpsertResponseRegistry(row); err != nil {
		t.Fatalf("re-upsert UpsertResponseRegistry: %v", err)
	}
	got2, err := GetResponseRegistry("resp_repo_1")
	if err != nil {
		t.Fatalf("GetResponseRegistry after re-upsert: %v", err)
	}
	if got2.ChannelId != 9 || got2.UpstreamModel != "gpt-4o" {
		t.Errorf("re-upsert did not update the row: %+v", got2)
	}

	if err := DeleteResponseRegistry("resp_repo_1"); err != nil {
		t.Fatalf("DeleteResponseRegistry: %v", err)
	}
	if _, err := GetResponseRegistry("resp_repo_1"); !errors.Is(err, ErrResponseRegistryNotFound) {
		t.Errorf("GetResponseRegistry after delete = %v, want ErrResponseRegistryNotFound", err)
	}
}

func TestResponseRegistryRepo_GetAbsentReturnsSentinel(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	if _, err := GetResponseRegistry("resp_never_existed"); !errors.Is(err, ErrResponseRegistryNotFound) {
		t.Errorf("GetResponseRegistry(absent) = %v, want ErrResponseRegistryNotFound", err)
	}
	if _, err := GetResponseRegistry(""); !errors.Is(err, ErrResponseRegistryNotFound) {
		t.Errorf("GetResponseRegistry(\"\") = %v, want ErrResponseRegistryNotFound", err)
	}
}

// TestResponseRegistryTTLSeconds drives the real env resolution: default,
// explicit positive override, and the invalid-value fallback.
func TestResponseRegistryTTLSeconds(t *testing.T) {
	prev, had := os.LookupEnv("RESPONSE_REGISTRY_TTL_DAYS")
	defer func() {
		if had {
			_ = os.Setenv("RESPONSE_REGISTRY_TTL_DAYS", prev)
		} else {
			_ = os.Unsetenv("RESPONSE_REGISTRY_TTL_DAYS")
		}
	}()

	_ = os.Unsetenv("RESPONSE_REGISTRY_TTL_DAYS")
	if got, want := ResponseRegistryTTLSeconds(), int64(30*24*60*60); got != want {
		t.Errorf("default TTL = %d, want %d (30 days)", got, want)
	}

	_ = os.Setenv("RESPONSE_REGISTRY_TTL_DAYS", "7")
	if got, want := ResponseRegistryTTLSeconds(), int64(7*24*60*60); got != want {
		t.Errorf("override TTL = %d, want %d (7 days)", got, want)
	}

	_ = os.Setenv("RESPONSE_REGISTRY_TTL_DAYS", "not-a-number")
	if got, want := ResponseRegistryTTLSeconds(), int64(30*24*60*60); got != want {
		t.Errorf("invalid-value TTL = %d, want default %d", got, want)
	}

	_ = os.Setenv("RESPONSE_REGISTRY_TTL_DAYS", "0")
	if got, want := ResponseRegistryTTLSeconds(), int64(30*24*60*60); got != want {
		t.Errorf("zero-value TTL = %d, want default %d (zero is not positive)", got, want)
	}
}

// TestSweepExpiredResponseRegistry_ExpiredOnly seeds one expired and one
// live row and asserts only the expired one is removed — the mutation
// "drop the expires_at <= ? filter" would sweep both.
func TestSweepExpiredResponseRegistry_ExpiredOnly(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	now := time.Now()
	expired := &entity.ResponseRegistry{
		ResponseId: "resp_expired", TenantId: "lurus", UserId: 1, TokenId: 1,
		ChannelId: 1, UpstreamModel: "gpt-4o-mini",
		CreatedAt: now.Add(-48 * time.Hour).Unix(), ExpiresAt: now.Add(-1 * time.Hour).Unix(),
	}
	live := &entity.ResponseRegistry{
		ResponseId: "resp_live", TenantId: "lurus", UserId: 1, TokenId: 1,
		ChannelId: 1, UpstreamModel: "gpt-4o-mini",
		CreatedAt: now.Unix(), ExpiresAt: now.Add(30 * 24 * time.Hour).Unix(),
	}
	if err := UpsertResponseRegistry(expired); err != nil {
		t.Fatalf("seed expired: %v", err)
	}
	if err := UpsertResponseRegistry(live); err != nil {
		t.Fatalf("seed live: %v", err)
	}

	n, err := SweepExpiredResponseRegistry(now)
	if err != nil {
		t.Fatalf("SweepExpiredResponseRegistry: %v", err)
	}
	if n != 1 {
		t.Errorf("swept %d row(s), want 1", n)
	}
	if _, err := GetResponseRegistry("resp_expired"); !errors.Is(err, ErrResponseRegistryNotFound) {
		t.Errorf("expired row survived the sweep: err=%v", err)
	}
	if _, err := GetResponseRegistry("resp_live"); err != nil {
		t.Errorf("live row was swept: %v", err)
	}
}
