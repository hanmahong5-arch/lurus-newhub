package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

const acctKeysBase = "/internal/v1/provisioning/accounts/"


// setupAccountKeysRouter extends the integration router with the account-key
// routes, the live binding table, a provisioning-only key (narrow scope, but
// whitelisted for the default tenant) and a /v1/probe route that runs the real
// TokenAuth and reports the relay's resolved source product.
func setupAccountKeysRouter(t *testing.T) *gin.Engine {
	t.Helper()
	router, cleanup := SetupIntegrationRouter(t)
	t.Cleanup(cleanup)
	if err := repo.DB.AutoMigrate(&repo.AccountKeyBinding{}); err != nil {
		t.Fatalf("migrate binding: %v", err)
	}
	// Narrow keys need a tenant whitelist row; use an all-scope key for the
	// behavioural tests and a scope-less key for the 403 case.
	g := router.Group("/internal/v1/provisioning")
	g.Use(middleware.InternalApiAuth(), middleware.RequireScope(repo.ScopeProvisioning))
	g.POST("/accounts/:account_id/keys", CreateAccountKey)
	g.POST("/accounts/:account_id/keys/rotate", RotateAccountKey)
	g.DELETE("/accounts/:account_id/keys", RevokeAccountKey)
	g.GET("/accounts/:account_id/keys", ListAccountKeys)

	router.GET("/v1/probe", middleware.TokenAuth(), func(c *gin.Context) {
		info := relaycommon.GenRelayInfoOpenAI(c, &dto.GeneralOpenAIRequest{})
		c.JSON(http.StatusOK, gin.H{"product": info.SourceProduct})
	})
	return router
}

func probe(router *gin.Engine, key, productHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/probe", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	if productHeader != "" {
		req.Header.Set("X-Lurus-Product", productHeader)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func dataOf(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	d, _ := parseResponse(t, w)["data"].(map[string]interface{})
	if d == nil {
		t.Fatalf("no data in response: %s", w.Body.String())
	}
	return d
}

func createKey(router *gin.Engine, acct, product, idem string) *httptest.ResponseRecorder {
	h := authHeaders()
	if idem != "" {
		h["Idempotency-Key"] = idem
	}
	return internalRequest(router, "POST", acctKeysBase+acct+"/keys", map[string]interface{}{"product": product}, h)
}

func TestAccountKey_CreateThenIdempotent(t *testing.T) {
	router := setupAccountKeysRouter(t)

	w1 := createKey(router, "7001", "lutu", "idem-1")
	if w1.Code != http.StatusCreated {
		t.Fatalf("first create: %d %s", w1.Code, w1.Body.String())
	}
	d1 := dataOf(t, w1)
	key1, _ := d1["key"].(string)
	if key1 == "" {
		t.Fatalf("plaintext key missing on first create")
	}

	// Same Idempotency-Key and a different one: both return the same binding,
	// never a second key, and never the plaintext again.
	for _, idem := range []string{"idem-1", "idem-other", ""} {
		w := createKey(router, "7001", "lutu", idem)
		if w.Code != http.StatusOK {
			t.Fatalf("replay(%q): %d %s", idem, w.Code, w.Body.String())
		}
		d := dataOf(t, w)
		if d["token_id"] != d1["token_id"] {
			t.Fatalf("replay(%q) token_id changed: %v vs %v", idem, d["token_id"], d1["token_id"])
		}
		if _, has := d["key"]; has {
			t.Fatalf("plaintext leaked on replay")
		}
		if m, _ := d["key_masked"].(string); m == "" || m == key1 {
			t.Fatalf("expected masked key, got %q", m)
		}
	}

	var n int64
	repo.DB.Model(&repo.Token{}).Where("identity_account_id = ?", 7001).Count(&n)
	if n != 1 {
		t.Fatalf("expected exactly 1 token, got %d", n)
	}
	// Wallet link: token carries the platform account id.
	var tok repo.Token
	repo.DB.First(&tok, "identity_account_id = ?", 7001)
	if tok.SourceProduct != "lutu" {
		t.Fatalf("token source_product = %q", tok.SourceProduct)
	}

	// A second product gets its own key.
	w := createKey(router, "7001", "kova", "")
	if w.Code != http.StatusCreated {
		t.Fatalf("second product: %d %s", w.Code, w.Body.String())
	}
}

func TestAccountKey_ConcurrentCreateYieldsOneKey(t *testing.T) {
	router := setupAccountKeysRouter(t)
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = createKey(router, "7002", "switch", "").Code
		}(i)
	}
	wg.Wait()
	created := 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusOK:
		default:
			t.Fatalf("unexpected status %v", codes)
		}
	}
	if created != 1 {
		t.Fatalf("expected exactly one 201, got %v", codes)
	}
	var n int64
	repo.DB.Model(&repo.AccountKeyBinding{}).Where("identity_account_id = ?", 7002).Count(&n)
	if n != 1 {
		t.Fatalf("bindings = %d", n)
	}
}

func TestAccountKey_UniqueIndexBackstop(t *testing.T) {
	setupAccountKeysRouter(t)
	a := &repo.AccountKeyBinding{IdentityAccountID: 7003, Product: "lutu", TokenId: 1}
	b := &repo.AccountKeyBinding{IdentityAccountID: 7003, Product: "lutu", TokenId: 2}
	if err := repo.DB.Create(a).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.DB.Create(b).Error; err == nil {
		t.Fatalf("second live binding for same (account, product) must violate the unique index")
	}
	// After soft-delete a new live binding is allowed again.
	repo.DB.Delete(a)
	b2 := &repo.AccountKeyBinding{IdentityAccountID: 7003, Product: "lutu", TokenId: 3}
	if err := repo.DB.Create(b2).Error; err != nil {
		t.Fatalf("re-create after revoke: %v", err)
	}
}

func TestAccountKey_RotateInvalidatesOldKey(t *testing.T) {
	router := setupAccountKeysRouter(t)
	d := dataOf(t, createKey(router, "7004", "lutu", ""))
	oldKey := d["key"].(string)
	if w := probe(router, oldKey, ""); w.Code != http.StatusOK {
		t.Fatalf("fresh key should authenticate: %d %s", w.Code, w.Body.String())
	}

	w := internalRequest(router, "POST", acctKeysBase+"7004/keys/rotate", map[string]interface{}{"product": "lutu"}, authHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", w.Code, w.Body.String())
	}
	newKey := dataOf(t, w)["key"].(string)
	if newKey == "" || newKey == oldKey {
		t.Fatalf("rotate must return a new plaintext key")
	}
	if w := probe(router, oldKey, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("old key after rotate: want 401, got %d", w.Code)
	}
	if w := probe(router, newKey, ""); w.Code != http.StatusOK {
		t.Fatalf("new key: want 200, got %d %s", w.Code, w.Body.String())
	}
}

func TestAccountKey_RevokeThenUnauthorized(t *testing.T) {
	router := setupAccountKeysRouter(t)
	key := dataOf(t, createKey(router, "7005", "lutu", ""))["key"].(string)

	w := internalRequest(router, "DELETE", acctKeysBase+"7005/keys?product=lutu", nil, authHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	if w := probe(router, key, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key: want 401, got %d", w.Code)
	}
	// Listing no longer shows it; a fresh create issues a new key.
	lw := internalRequest(router, "GET", acctKeysBase+"7005/keys", nil, authHeaders())
	items, _ := dataOf(t, lw)["items"].([]interface{})
	if len(items) != 0 {
		t.Fatalf("expected empty list after revoke, got %v", items)
	}
	if w := createKey(router, "7005", "lutu", ""); w.Code != http.StatusCreated {
		t.Fatalf("re-create after revoke: %d %s", w.Code, w.Body.String())
	}
	// Revoking a missing binding is a 404.
	if w := internalRequest(router, "DELETE", acctKeysBase+"7999/keys?product=lutu", nil, authHeaders()); w.Code != http.StatusNotFound {
		t.Fatalf("revoke missing: want 404, got %d", w.Code)
	}
}

func TestAccountKey_ScopeAndValidation(t *testing.T) {
	router := setupAccountKeysRouter(t)

	// read-only key has no provisioning scope -> 403
	w := internalRequest(router, "POST", acctKeysBase+"7006/keys", map[string]interface{}{"product": "lutu"}, readOnlyHeaders())
	if w.Code != http.StatusForbidden {
		t.Fatalf("no scope: want 403, got %d %s", w.Code, w.Body.String())
	}
	// unknown product -> 400
	if w := createKey(router, "7006", "not-a-product", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown product: want 400, got %d", w.Code)
	}
	if w := createKey(router, "7006", "", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("missing product: want 400, got %d", w.Code)
	}
	// bad account id -> 400
	if w := createKey(router, "abc", "lutu", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad account: want 400, got %d", w.Code)
	}
	if w := createKey(router, "0", "lutu", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("zero account: want 400, got %d", w.Code)
	}
}

func TestAccountKey_ListShowsPerProductMetadataMasked(t *testing.T) {
	router := setupAccountKeysRouter(t)
	k1 := dataOf(t, createKey(router, "7007", "lutu", ""))["key"].(string)
	createKey(router, "7007", "kova", "")
	w := internalRequest(router, "GET", acctKeysBase+"7007/keys", nil, authHeaders())
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	items, _ := dataOf(t, w)["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if raw, _ := json.Marshal(items); containsStr(string(raw), k1) {
		t.Fatalf("list leaked plaintext key")
	}
}

func TestAccountKey_RelayProductFromBinding(t *testing.T) {
	router := setupAccountKeysRouter(t)
	key := dataOf(t, createKey(router, "7008", "lutu", ""))["key"].(string)

	// No header: attributed to the bound product.
	w := probe(router, key, "")
	if w.Code != http.StatusOK {
		t.Fatalf("probe: %d %s", w.Code, w.Body.String())
	}
	if got := dataProduct(t, w); got != "lutu" {
		t.Fatalf("no header: product = %q, want lutu", got)
	}
	// Allow-listed header overrides.
	if got := dataProduct(t, probe(router, key, "kova")); got != "kova" {
		t.Fatalf("allow-listed override: product = %q, want kova", got)
	}
	// Non-allow-listed header is ignored and falls back to the bound product,
	// not to the global default.
	if got := dataProduct(t, probe(router, key, "evil-product")); got != "lutu" {
		t.Fatalf("forged header: product = %q, want lutu", got)
	}
}

func dataProduct(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Product string `json:"product"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Product
}

func containsStr(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
