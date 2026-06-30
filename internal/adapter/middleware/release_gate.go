package middleware

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

// ReleaseGateConfig holds the release-download gate's dependencies. Splitting the
// IO (release lookup / identity resolution / entitlement call) out as function
// fields keeps the decision logic in ReleaseDownloadGateWith pure and
// hermetically testable — no DB, no JWKS, no platform HTTP in unit tests.
type ReleaseGateConfig struct {
	// GatedProducts is the set of product_ids that REQUIRE a paid entitlement to
	// download. Empty = feature off (every download stays public, current
	// behaviour preserved). This is the owner-controlled "which products are
	// paid" policy, sourced from RELEASE_GATED_PRODUCTS.
	GatedProducts map[string]bool
	// LookupProductID resolves a release id to its product_id. found=false when
	// the release does not exist, so the gate defers to the handler's 404.
	LookupProductID func(ctx context.Context, releaseID int64) (productID string, found bool)
	// ResolveAccount best-effort resolves the caller's platform account id from a
	// OIDC JWT. ok=false when the caller is anonymous / token invalid.
	ResolveAccount func(c *gin.Context) (accountID int64, ok bool)
	// CheckEntitled reports whether accountID may download productID. A non-nil
	// error means the entitlement system could not give a definite answer; the
	// gate treats that as DENY (fail-closed), unlike the LLM relay's fail-open
	// EntitlementCheck — relay quota is also enforced downstream, but a binary
	// download has no second line of defence.
	CheckEntitled func(ctx context.Context, accountID int64, productID string) (allowed bool, err error)
}

// ReleaseDownloadGateWith enforces a paid-entitlement gate on the release
// download route for products listed in GatedProducts. It is fail-CLOSED for
// gated products (anonymous → 401, no/failed entitlement → 403) and a no-op for
// every other product, so free/public downloads are unaffected.
func ReleaseDownloadGateWith(cfg ReleaseGateConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Feature off: no product is gated → preserve public download behaviour.
		if len(cfg.GatedProducts) == 0 {
			c.Next()
			return
		}

		// Malformed id → let the handler own input validation (single source).
		releaseID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.Next()
			return
		}

		productID, found := cfg.LookupProductID(c.Request.Context(), releaseID)
		if !found {
			// Release missing → defer to the handler's 404, don't leak via the gate.
			c.Next()
			return
		}

		if !cfg.GatedProducts[productID] {
			// Free / public product → unchanged anonymous download.
			c.Next()
			return
		}

		// --- Gated product: fail-closed enforcement ---
		accountID, ok := cfg.ResolveAccount(c)
		if !ok || accountID <= 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success":    false,
				"message":    "This download requires a signed-in account with an active entitlement.",
				"error_code": "DOWNLOAD_AUTH_REQUIRED",
			})
			return
		}

		allowed, err := cfg.CheckEntitled(c.Request.Context(), accountID, productID)
		if err != nil {
			// Entitlement system indeterminate → DENY (fail-closed).
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success":    false,
				"message":    "Could not verify your entitlement for this product. Please try again later.",
				"error_code": "ENTITLEMENT_CHECK_FAILED",
			})
			return
		}
		if !allowed {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success":     false,
				"message":     "Your account does not have an active entitlement to download this product.",
				"error_code":  "NO_ENTITLEMENT",
				"upgrade_url": common.IdentityPublicURL + "/pricing",
			})
			return
		}

		c.Next()
	}
}

// ReleaseDownloadGate wires ReleaseDownloadGateWith with the production
// implementations (releases table, OIDC JWT, platform entitlement API).
// Reads the gated-product set once at router-setup time.
func ReleaseDownloadGate() gin.HandlerFunc {
	raw := os.Getenv("RELEASE_GATED_PRODUCTS")
	gated := parseGatedProducts(raw)
	if len(gated) > 0 {
		common.SysLog("release download gate ENABLED for products: " + raw)
	}
	return ReleaseDownloadGateWith(ReleaseGateConfig{
		GatedProducts:   gated,
		LookupProductID: lookupReleaseProductID,
		ResolveAccount:  resolveOIDCAccountID,
		CheckEntitled:   checkProductEntitlement,
	})
}

// parseGatedProducts turns "lurus-switch, lurus-creator" into a set.
func parseGatedProducts(raw string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out[p] = true
		}
	}
	return out
}

func lookupReleaseProductID(ctx context.Context, releaseID int64) (string, bool) {
	rel, err := repo.NewReleaseRepository(repo.DB).GetReleaseByID(ctx, releaseID)
	if err != nil || rel == nil {
		return "", false
	}
	return rel.ProductId, true
}

// resolveOIDCAccountID best-effort validates a OIDC JWT and returns the
// caller's platform account id. Non-aborting (unlike OIDCAuth) so the gate
// owns the fail-closed response shape. Mirrors OIDCAuth's
// verify-signature → issuer-check → UpsertAccountGRPC path without the
// c.Next()/c.Abort() side effects.
func resolveOIDCAccountID(c *gin.Context) (int64, bool) {
	if raw, ok := c.Get("identity_account_id"); ok {
		if id, ok2 := raw.(int64); ok2 && id > 0 {
			return id, true
		}
	}
	if !oidcEnabled {
		return 0, false
	}
	authHeader := c.GetHeader("Authorization")
	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	tokenString = strings.TrimPrefix(tokenString, "bearer ")
	if tokenString == "" || tokenString == authHeader {
		return 0, false
	}
	claims := &OIDCClaims{}
	if _, err := VerifyIDTokenWithJWKS(tokenString, claims); err != nil {
		return 0, false
	}
	// Same multi-issuer set as OIDCAuth to cover the rebrand alias window.
	releaseIssuerOK := false
	for _, accepted := range oidcIssuers {
		if claims.Issuer == accepted {
			releaseIssuerOK = true
			break
		}
	}
	if !releaseIssuerOK {
		return 0, false
	}
	im, _ := common.UpsertAccountGRPC(c.Request.Context(), claims.Subject, claims.Email, claims.Name, "")
	if im == nil || im.ID <= 0 {
		return 0, false
	}
	c.Set("identity_account_id", im.ID)
	return im.ID, true
}

// checkProductEntitlement asks platform whether accountID may download
// productID. Allowed iff quota_remaining is non-zero (-1 unlimited / >0 metered);
// absent or 0 → DENY. NOTE: a gated product MUST surface a quota_remaining
// entitlement for this predicate to admit it — perpetual licences should report
// -1. See doc/runbook for the owner/platform confirmation of this contract.
func checkProductEntitlement(ctx context.Context, accountID int64, productID string) (bool, error) {
	ent, err := common.GetEntitlements(ctx, accountID, productID)
	if err != nil {
		return false, err
	}
	return ent.GetInt("quota_remaining", 0) != 0, nil
}
