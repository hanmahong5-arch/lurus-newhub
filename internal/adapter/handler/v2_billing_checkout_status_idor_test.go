package handler

// v2_billing_checkout_status_idor_test.go — locks the ownership gate on
// GET /api/v2/user/billing/checkout/:order_no/status (GetBillingCheckoutStatus).
//
// The platform's checkout-status endpoint is keyed only by order_no and takes
// no account parameter (identity_client.go GetCheckoutStatus builds the URL
// from order_no alone), so newhub must prove ownership locally: migration 028
// records (order_no -> account_id) at create time, and the poll rejects a
// caller whose linked platform account does not own the order. Before this
// gate, any authenticated caller could read another account's
// amount_cny/status/paid_at by knowing or enumerating its order_no.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupCheckoutOwnershipDB installs a hermetic sqlite DB with the ownership
// table and restores the previous global on cleanup.
func setupCheckoutOwnershipDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.BillingCheckoutOrder{}); err != nil {
		t.Fatalf("auto-migrate BillingCheckoutOrder: %v", err)
	}
	prev := repo.DB
	repo.DB = db
	t.Cleanup(func() { repo.DB = prev })
}

func TestGetBillingCheckoutStatus_CrossAccountIDOR(t *testing.T) {
	setupCheckoutOwnershipDB(t)

	const (
		ownerAccount    = int64(11111)
		attackerAccount = int64(99999)
		orderNo         = "LO20260716deadbeefdeadbeefdeadbeef"
	)
	// The order belongs to ownerAccount.
	if err := repo.RecordCheckoutOrder(orderNo, ownerAccount, 500.00, common.GetTimestamp()); err != nil {
		t.Fatalf("seed ownership record: %v", err)
	}

	// Stand in for lurus-platform's internal checkout-status endpoint: keyed
	// only by order_no, it cannot itself distinguish which account is asking —
	// which is exactly why the ownership gate has to live in newhub. If the
	// gate is bypassed the handler forwards here and returns 200; the gate
	// firing first means this server is never reached for a foreign caller.
	forwarded := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(common.CheckoutStatus{
			OrderNo:   orderNo,
			Status:    "paid",
			AmountCNY: 500.00,
		})
	}))
	defer srv.Close()

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	t.Cleanup(func() { common.IdentityServiceURL = prevURL })

	// (a) attacker polls an order it does not own -> 403, no forward.
	t.Run("foreign account denied", func(t *testing.T) {
		forwarded = false
		c, w := r2chanNewCtx(http.MethodGet, "/", nil)
		c.Params = gin.Params{{Key: "order_no", Value: orderNo}}
		c.Set("identity_account_id", attackerAccount)

		GetBillingCheckoutStatus(c)

		if w.Code != http.StatusForbidden {
			t.Errorf("foreign account must get 403, got code=%d body=%s", w.Code, w.Body.String())
		}
		if forwarded {
			t.Error("handler forwarded a foreign caller's request to the platform before the ownership gate")
		}
	})

	// (b) unknown order (no ownership record) -> 403, fail closed.
	t.Run("unknown order denied", func(t *testing.T) {
		forwarded = false
		c, w := r2chanNewCtx(http.MethodGet, "/", nil)
		c.Params = gin.Params{{Key: "order_no", Value: "LO20260716-does-not-exist"}}
		c.Set("identity_account_id", ownerAccount)

		GetBillingCheckoutStatus(c)

		if w.Code != http.StatusForbidden {
			t.Errorf("unknown order must fail closed with 403, got code=%d body=%s", w.Code, w.Body.String())
		}
		if forwarded {
			t.Error("handler forwarded an unowned order to the platform")
		}
	})

	// (c) unlinked account (no platform account) -> 503, no forward.
	t.Run("unlinked account", func(t *testing.T) {
		forwarded = false
		c, w := r2chanNewCtx(http.MethodGet, "/", nil)
		c.Params = gin.Params{{Key: "order_no", Value: orderNo}}
		// no identity_account_id set -> getIdentityAccountID returns 0

		GetBillingCheckoutStatus(c)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("unlinked account must get 503, got code=%d body=%s", w.Code, w.Body.String())
		}
		if forwarded {
			t.Error("handler forwarded despite an unlinked account")
		}
	})

	// (d) rightful owner polls its own order -> 200, forwarded.
	t.Run("owner allowed", func(t *testing.T) {
		forwarded = false
		c, w := r2chanNewCtx(http.MethodGet, "/", nil)
		c.Params = gin.Params{{Key: "order_no", Value: orderNo}}
		c.Set("identity_account_id", ownerAccount)

		GetBillingCheckoutStatus(c)

		if w.Code != http.StatusOK {
			t.Errorf("rightful owner must get 200, got code=%d body=%s", w.Code, w.Body.String())
		}
		if !forwarded {
			t.Error("owner's request was not forwarded to the platform status endpoint")
		}
	})
}
