package handler

// v2_billing_checkout_idempotency_test.go — two identical checkout POSTs must
// create ONE order.
//
// The platform enforces checkout idempotency on the Idempotency-Key header
// (identity_client.go sends it; a checkout without it is 400ed). newhub then
// fed that correctly-plumbed mechanism a value that can never repeat:
// "api-<account>-<8 random hex>", minted fresh per HTTP request. A
// double-clicked Pay button, a retried request after a client timeout, or a
// browser refresh therefore each opened a NEW order.
//
// The stub below stands in for the platform's dedupe: same key in, same order
// back, no new order. That behaviour is the platform's contract, not something
// these tests prove — what they prove is that newhub feeds it a key that CAN
// repeat. A checkout is not money moving by itself (the customer still has to
// pay), so the damage is duplicate pending orders and a customer who can pay
// the same top-up twice, not a silent double charge.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// fakeCheckoutPlatform models lurus-platform's checkout endpoint: orders are
// keyed by the Idempotency-Key HEADER, and a repeat of a key returns the order
// that key already created.
type fakeCheckoutPlatform struct {
	mu      sync.Mutex
	byKey   map[string]string // idempotency key -> order_no
	keySeen []string          // every key received, in order
	seq     int
}

func newFakeCheckoutPlatform(t *testing.T) *fakeCheckoutPlatform {
	t.Helper()
	p := &fakeCheckoutPlatform{byKey: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		p.mu.Lock()
		p.keySeen = append(p.keySeen, key)
		orderNo, replay := p.byKey[key]
		if !replay {
			p.seq++
			orderNo = "LO2026092100000000" + strconv.Itoa(p.seq)
			p.byKey[key] = orderNo
		}
		p.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"order_no": orderNo,
			"pay_url":  "https://pay.example/" + orderNo,
			"status":   "pending",
		})
	}))
	t.Cleanup(srv.Close)

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	t.Cleanup(func() { common.IdentityServiceURL = prevURL })
	return p
}

func (p *fakeCheckoutPlatform) ordersCreated() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byKey)
}

func (p *fakeCheckoutPlatform) keys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.keySeen))
	copy(out, p.keySeen)
	return out
}

// postCheckout drives CreateBillingCheckout once, with optional request
// headers, and returns the decoded data object.
func postCheckout(t *testing.T, accountID int64, body map[string]any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	c, w := r2chanNewCtx(http.MethodPost, "/api/v2/user/billing/checkout", body)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	c.Set("identity_account_id", accountID)

	CreateBillingCheckout(c)

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v — raw: %s", err, w.Body.String())
	}
	data, _ := resp["data"].(map[string]any)
	return w.Code, data
}

// TestCreateBillingCheckout_ClientKeyProducesOneOrder is the oracle: when the
// console retries a checkout with the same Idempotency-Key, exactly one order
// must exist on the platform side and both responses must name it.
func TestCreateBillingCheckout_ClientKeyProducesOneOrder(t *testing.T) {
	setupCheckoutOwnershipDB(t)
	platform := newFakeCheckoutPlatform(t)

	const accountID = int64(77001)
	body := map[string]any{"amount_cny": 50.0, "payment_method": "alipay"}
	headers := map[string]string{"Idempotency-Key": "console-checkout-intent-1"}

	code1, data1 := postCheckout(t, accountID, body, headers)
	if code1 != http.StatusCreated {
		t.Fatalf("first checkout: status = %d, want 201", code1)
	}
	code2, data2 := postCheckout(t, accountID, body, headers)
	if code2 != http.StatusCreated {
		t.Fatalf("retry: status = %d, want 201", code2)
	}

	if n := platform.ordersCreated(); n != 1 {
		t.Errorf("orders created = %d, want 1 — the client key never reached the platform, keys seen: %v",
			n, platform.keys())
	}
	if data1["order_no"] != data2["order_no"] {
		t.Errorf("order_no = %v then %v, want the same order back on a retry", data1["order_no"], data2["order_no"])
	}
	for i, k := range platform.keys() {
		if k != "console-checkout-intent-1" {
			t.Errorf("key %d sent to the platform = %q, want the caller-supplied key", i, k)
		}
	}
}

// pinCheckoutClock freezes the window the fallback key buckets on, so these
// tests measure the key derivation rather than whether two back-to-back
// requests happened to straddle a real two-minute boundary.
func pinCheckoutClock(t *testing.T, at time.Time) {
	t.Helper()
	prev := checkoutIntentNow
	checkoutIntentNow = func() time.Time { return at }
	t.Cleanup(func() { checkoutIntentNow = prev })
}

// TestCreateBillingCheckout_FallbackKeyIsDeterministic: with no client header
// at all — today's console — two identical POSTs inside one intent window must
// still reach the platform under ONE key. This is the half of the fix that
// protects clients that have not been updated yet.
func TestCreateBillingCheckout_FallbackKeyIsDeterministic(t *testing.T) {
	setupCheckoutOwnershipDB(t)
	platform := newFakeCheckoutPlatform(t)
	pinCheckoutClock(t, time.Date(2026, 9, 21, 10, 0, 30, 0, time.UTC))

	const accountID = int64(77003)
	body := map[string]any{"amount_cny": 120.0, "payment_method": "alipay", "return_url": "https://hub.example/done"}

	if code, _ := postCheckout(t, accountID, body, nil); code != http.StatusCreated {
		t.Fatalf("first checkout: status = %d, want 201", code)
	}
	if code, _ := postCheckout(t, accountID, body, nil); code != http.StatusCreated {
		t.Fatalf("double submit: status = %d, want 201", code)
	}

	keys := platform.keys()
	if len(keys) != 2 || keys[0] != keys[1] {
		t.Errorf("keys sent = %v, want the same key twice — a per-request random key can never dedupe", keys)
	}
	if n := platform.ordersCreated(); n != 1 {
		t.Errorf("orders created = %d, want 1", n)
	}
}

// TestCreateBillingCheckout_FallbackKeyDistinguishesIntents is the
// over-dedupe guard for the fallback: a different amount (or a different
// account, or a later window) is a different intent and must get its own
// order. A fallback that collapsed everything from one account into a single
// key would be worse than the random key it replaced — the customer's second,
// deliberate top-up would silently hand back the first order.
func TestCreateBillingCheckout_FallbackKeyDistinguishesIntents(t *testing.T) {
	setupCheckoutOwnershipDB(t)
	platform := newFakeCheckoutPlatform(t)
	base := time.Date(2026, 9, 21, 10, 0, 30, 0, time.UTC)
	pinCheckoutClock(t, base)

	const accountID = int64(77004)
	if code, _ := postCheckout(t, accountID, map[string]any{"amount_cny": 50.0, "payment_method": "alipay"}, nil); code != http.StatusCreated {
		t.Fatalf("50 CNY checkout: status = %d, want 201", code)
	}
	// Different amount -> different intent.
	if code, _ := postCheckout(t, accountID, map[string]any{"amount_cny": 80.0, "payment_method": "alipay"}, nil); code != http.StatusCreated {
		t.Fatalf("80 CNY checkout: status = %d, want 201", code)
	}
	// Different payment method -> different intent.
	if code, _ := postCheckout(t, accountID, map[string]any{"amount_cny": 50.0, "payment_method": "wechat"}, nil); code != http.StatusCreated {
		t.Fatalf("wechat checkout: status = %d, want 201", code)
	}
	// Same intent, a later window -> a deliberate repeat top-up, not a retry.
	pinCheckoutClock(t, base.Add(3*time.Minute))
	if code, _ := postCheckout(t, accountID, map[string]any{"amount_cny": 50.0, "payment_method": "alipay"}, nil); code != http.StatusCreated {
		t.Fatalf("repeat top-up: status = %d, want 201", code)
	}
	// And another account's identical request is never the same intent.
	if code, _ := postCheckout(t, int64(77005), map[string]any{"amount_cny": 50.0, "payment_method": "alipay"}, nil); code != http.StatusCreated {
		t.Fatalf("other account checkout: status = %d, want 201", code)
	}

	if n := platform.ordersCreated(); n != 5 {
		t.Errorf("orders created = %d, want 5 — five distinct intents, keys seen: %v", n, platform.keys())
	}
}

// TestCreateBillingCheckout_XIdempotencyKeyHeaderHonoured: the credit-pool
// topup accepts either spelling of the header (tenant_credit_pool.go), so a
// client that already speaks that dialect must not silently fall back to a
// generated key here.
func TestCreateBillingCheckout_XIdempotencyKeyHeaderHonoured(t *testing.T) {
	setupCheckoutOwnershipDB(t)
	platform := newFakeCheckoutPlatform(t)

	const accountID = int64(77002)
	body := map[string]any{"amount_cny": 80.0, "payment_method": "wechat"}
	headers := map[string]string{"X-Idempotency-Key": "x-header-intent-1"}

	if code, _ := postCheckout(t, accountID, body, headers); code != http.StatusCreated {
		t.Fatalf("first checkout: status = %d, want 201", code)
	}
	if code, _ := postCheckout(t, accountID, body, headers); code != http.StatusCreated {
		t.Fatalf("retry: status = %d, want 201", code)
	}

	if n := platform.ordersCreated(); n != 1 {
		t.Errorf("orders created = %d, want 1, keys seen: %v", n, platform.keys())
	}
	for i, k := range platform.keys() {
		if k != "x-header-intent-1" {
			t.Errorf("key %d sent to the platform = %q, want the X-Idempotency-Key value", i, k)
		}
	}
}
