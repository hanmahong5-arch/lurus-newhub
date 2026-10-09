package repo

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var walletCacheDBCounter atomic.Int64

func setupWalletCacheDB(t *testing.T) *gorm.DB {
	t.Helper()
	n := walletCacheDBCounter.Add(1)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:walletcache%d?mode=memory&cache=shared", n)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Tenant{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := DB
	DB = db
	ResetTenantWalletCache()
	now := time.Now()
	tenantWalletNow = func() time.Time { return now }
	t.Cleanup(func() {
		DB = prev
		tenantWalletNow = time.Now
		ResetTenantWalletCache()
	})
	return db
}

func seedWalletRow(t *testing.T, db *gorm.DB, id string, flag bool) {
	t.Helper()
	if err := db.Create(&Tenant{Id: id, IDPOrgID: "org-" + id, Slug: id, Name: id, Status: 1}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Model(&Tenant{}).Where("id = ?", id).Update("wallet_authoritative", flag).Error; err != nil {
		t.Fatalf("flag: %v", err)
	}
}

func TestTenantWalletAuthoritative_CachedWithinTTLThenRefreshed(t *testing.T) {
	db := setupWalletCacheDB(t)
	seedWalletRow(t, db, "wc1", true)
	if !TenantWalletAuthoritative("wc1") {
		t.Fatal("want true on first read")
	}
	// Out-of-band change (another replica): invisible inside the TTL.
	db.Model(&Tenant{}).Where("id = ?", "wc1").Update("wallet_authoritative", false)
	if !TenantWalletAuthoritative("wc1") {
		t.Fatal("second read inside the TTL must come from the cache")
	}
	base := tenantWalletNow()
	tenantWalletNow = func() time.Time { return base.Add(tenantWalletCacheTTL + time.Second) }
	if TenantWalletAuthoritative("wc1") {
		t.Fatal("after the TTL the flag must be re-read")
	}
}

func TestTenantWalletAuthoritative_UpdateTenantInvalidates(t *testing.T) {
	db := setupWalletCacheDB(t)
	seedWalletRow(t, db, "wc2", false)
	if TenantWalletAuthoritative("wc2") {
		t.Fatal("want false")
	}
	if err := UpdateTenant("wc2", map[string]interface{}{"wallet_authoritative": true}); err != nil {
		t.Fatal(err)
	}
	if !TenantWalletAuthoritative("wc2") {
		t.Fatal("UpdateTenant must drop the cached flag at once")
	}
}

func TestTenantWalletAuthoritative_DBErrorKeepsLastValue(t *testing.T) {
	db := setupWalletCacheDB(t)
	seedWalletRow(t, db, "wc3", true)
	if !TenantWalletAuthoritative("wc3") {
		t.Fatal("want true")
	}
	if err := db.Migrator().DropTable(&Tenant{}); err != nil {
		t.Fatal(err)
	}
	base := tenantWalletNow()
	tenantWalletNow = func() time.Time { return base.Add(tenantWalletCacheTTL + time.Minute) }
	if !TenantWalletAuthoritative("wc3") {
		t.Fatal("a failing DB must fall back to the last known value")
	}
}

func TestTenantWalletAuthoritative_DBErrorNeverReadFailsClosed(t *testing.T) {
	db := setupWalletCacheDB(t)
	if err := db.Migrator().DropTable(&Tenant{}); err != nil {
		t.Fatal(err)
	}
	if TenantWalletAuthoritative("never-read") {
		t.Fatal("no prior value and a DB error must answer false")
	}
	if TenantWalletAuthoritative("") {
		t.Fatal("empty id must answer false")
	}
}
