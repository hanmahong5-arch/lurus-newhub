package middleware

// entitlement_product_test.go — L4 (PRODUCT-ID-SSOT / AC-2b): EntitlementCheck
// must ask the platform about the SAME per-request product the relay's wallet
// debit is filed under (ratio_setting.ResolveSourceProduct(X-Lurus-Product)),
// not the package-level ratio_setting.DefaultSourceProduct constant it used
// before this fix — otherwise a "kova" caller's entitlement gate silently
// checks "llm-api" quota, which is not the quota being spent.
//
// abortQuotaExceeded's 429 must also be a typed, wire-native envelope
// (rendered via renderRejection) rather than the old ad-hoc
// {"error":"quota_exceeded",...} map, so a Claude/Gemini SDK sees its own
// error shape on this gate exactly like every other relay-stage rejection.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// TestEntitlementCheck_ResolvesProductPerRequest locks the X-Lurus-Product ->
// entitlements-call wiring: a "kova" header must reach the platform as
// "kova", and a header outside the allow-list must silently fold to the
// default — never pass an unvalidated caller string straight through to the
// entitlements URL.
func TestEntitlementCheck_ResolvesProductPerRequest(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"quota_remaining":"-1"}`)) // unlimited -> always allowed, never cached-deny
	}))
	defer srv.Close()

	prevURL := common.IdentityServiceURL
	common.IdentityServiceURL = srv.URL
	defer func() { common.IdentityServiceURL = prevURL }()

	const acctKova = int64(881001)
	const acctBogus = int64(881002)
	entitlementCache.Delete(entitlementCacheKey{accountID: acctKova, product: "kova"})
	entitlementCache.Delete(entitlementCacheKey{accountID: acctBogus, product: ratio_setting.DefaultSourceProduct})
	defer entitlementCache.Delete(entitlementCacheKey{accountID: acctKova, product: "kova"})
	defer entitlementCache.Delete(entitlementCacheKey{accountID: acctBogus, product: ratio_setting.DefaultSourceProduct})

	r := gin.New()
	r.Use(func(c *gin.Context) {
		if v := c.GetHeader("X-Test-Account"); v == "kova" {
			c.Set("identity_account_id", acctKova)
		} else {
			c.Set("identity_account_id", acctBogus)
		}
		c.Next()
	})
	r.GET("/e", EntitlementCheck(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	// Allow-listed header: must reach the platform as "kova".
	req := httptest.NewRequest(http.MethodGet, "/e", nil)
	req.Header.Set("X-Test-Account", "kova")
	req.Header.Set(ratio_setting.SourceProductHeader, "kova")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("kova request status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	// Unrecognized header: must silently fold to the default, never forward
	// the raw caller-supplied string.
	req2 := httptest.NewRequest(http.MethodGet, "/e", nil)
	req2.Header.Set(ratio_setting.SourceProductHeader, "evil-injected-product")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("bogus-header request status = %d, want 200; body=%s", w2.Code, w2.Body.String())
	}

	if len(gotPaths) != 2 {
		t.Fatalf("platform hit %d times, want 2; paths=%v", len(gotPaths), gotPaths)
	}
	if !strings.HasSuffix(gotPaths[0], "/entitlements/kova") {
		t.Errorf("kova request hit %q, want a path ending in /entitlements/kova", gotPaths[0])
	}
	if !strings.HasSuffix(gotPaths[1], "/entitlements/"+ratio_setting.DefaultSourceProduct) {
		t.Errorf("bogus-header request hit %q, want a path ending in /entitlements/%s (folded default)", gotPaths[1], ratio_setting.DefaultSourceProduct)
	}
}

// TestEntitlementCheck_DeniedEntry_TypedEnvelopeBothWires locks the 429 body
// shape on two wires: the OpenAI-default (no format stamped, e.g. a bare
// middleware test or a caller outside the relay chain) and Claude (stamped
// via StampRelayFormat on a /v1/messages route, mirroring how the real relay
// chain mounts both middlewares — see relay-router.go).
func TestEntitlementCheck_DeniedEntry_TypedEnvelopeBothWires(t *testing.T) {
	const acct = int64(881003)
	key := entitlementCacheKey{accountID: acct, product: ratio_setting.DefaultSourceProduct}
	entitlementCache.Store(key, entitlementEntry{allowed: false, checkedAt: time.Now()})
	defer entitlementCache.Delete(key)

	mount := func(c *gin.Context) { c.Set("identity_account_id", acct); c.Next() }

	t.Run("openai_default", func(t *testing.T) {
		r := gin.New()
		r.Use(mount)
		r.GET("/e", EntitlementCheck(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e", nil))

		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429; body=%s", w.Code, w.Body.String())
		}
		var body struct {
			Error struct {
				Type     string          `json:"type"`
				Code     string          `json:"code"`
				Metadata json.RawMessage `json:"metadata"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body did not unmarshal into the OpenAI error envelope: %v; body=%s", err, w.Body.String())
		}
		if body.Error.Code != "quota_exceeded" {
			t.Errorf("error.code = %q, want quota_exceeded", body.Error.Code)
		}
		// L3-CONTRACT-TAXONOMY: the OpenAI wire now reports the vendor type
		// for a 429 (rate_limit_error) instead of the retired new_api_error
		// literal — see types.WireErrorType.
		if body.Error.Type != "rate_limit_error" {
			t.Errorf("error.type = %q, want rate_limit_error", body.Error.Type)
		}
		// The OpenAI wire is the one shape with room for a metadata object
		// (types.OpenAIError.Metadata) — abortQuotaExceeded's upgrade_url
		// rides it via ErrOptionWithUpgradeURL so the client can navigate
		// straight to the pricing page.
		if !strings.Contains(string(body.Error.Metadata), "upgrade_url") {
			t.Errorf("error.metadata = %s, want it to contain upgrade_url", body.Error.Metadata)
		}
		// L3-CONTRACT-TAXONOMY item 6: this gate keys on identity_account_id
		// (one platform account), not the tenant concept, so the honest
		// scope label is "account" — see doc/product-integration-guide.md
		// §E for the callout that not every 429 carries these two headers.
		if got := w.Header().Get("X-RateLimit-Scope"); got != "account" {
			t.Errorf("X-RateLimit-Scope = %q, want account", got)
		}
		if got := w.Header().Get("X-RateLimit-Type"); got != "quota" {
			t.Errorf("X-RateLimit-Type = %q, want quota", got)
		}
	})

	t.Run("claude_wire", func(t *testing.T) {
		r := gin.New()
		// StampRelayFormat must precede EntitlementCheck in registration order
		// (gin snapshots the chain at Group()/route registration) — mirrors
		// relay-router.go's actual mount order for every relay group.
		r.Use(StampRelayFormat())
		r.Use(mount)
		r.POST("/v1/messages", EntitlementCheck(), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))

		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429; body=%s", w.Code, w.Body.String())
		}
		var body struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body did not unmarshal into the Claude error envelope: %v; body=%s", err, w.Body.String())
		}
		if body.Type != "error" {
			t.Errorf("top-level type = %q, want \"error\" (Claude wire)", body.Type)
		}
		// L3-CONTRACT-TAXONOMY item 4: before the root-mapping fix,
		// abortQuotaExceeded built its error via types.WithOpenAIError, so
		// ToClaudeError's ErrorTypeOpenAIError branch stamped the raw Code
		// string ("quota_exceeded") into error.type instead of the vendor
		// taxonomy — a Claude SDK would see an unrecognised type value. Pin
		// the correct vendor value here.
		if body.Error.Type != "rate_limit_error" {
			t.Errorf("error.type = %q, want the Anthropic-wire vendor taxonomy value rate_limit_error (not the leaked quota_exceeded code)", body.Error.Type)
		}
		if !strings.Contains(body.Error.Message, "quota_exceeded") {
			t.Errorf("error.message = %q, want it to contain quota_exceeded (Gemini/Claude envelopes drop Code)", body.Error.Message)
		}
		// types.ClaudeError has no Metadata field at all (see
		// middleware/wire_format.go's renderRejection comment: "the
		// Claude/Gemini envelopes have no room for them") — a Claude caller
		// on this gate gets NO upgrade_url anywhere in the body, nested or
		// top-level. Asserting the raw body has no such substring pins that
		// reality rather than a "both wires carry metadata" claim this wire
		// structurally cannot satisfy.
		if strings.Contains(w.Body.String(), "upgrade_url") {
			t.Errorf("Claude wire body unexpectedly carries upgrade_url (ClaudeError has no Metadata field to carry it): %s", w.Body.String())
		}
	})
}
