package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Wallet call seams — package-level vars so hermetic tests can inject
// "debit succeeded / revert failed" without a live platform gRPC endpoint
// (same seam convention as app.AsyncGo). Production always uses the real
// common.* clients.
var (
	debitWallet  = common.DebitWalletGRPC
	creditWallet = common.CreditWalletGRPC
)

// Reseller-facing admin handlers for tenant credit pools.
// Routes registered under /api/v2/admin/tenants/:id/credit-pool* in
// api-v2-router.go. Auth: middleware.RootJWTAuth (Platform admin / Reseller).
//
// Canonical: ADR 2026-05-18 (tenant-credit-pool) §4.1.

// CreateCreditPool initialises a credit pool row for a tenant.
// Route: POST /api/v2/admin/tenants/:id/credit-pool
// Body:  { max_balance, reset_period, alert_threshold_pct }
//
// 201 with pool body, or 409 if a pool already exists for the tenant.
// `max_balance == -1` means unlimited (relay gate becomes a no-op).
func CreateCreditPool(c *gin.Context) {
	tenantID := c.Param("id")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "tenant id required"})
		return
	}
	if _, err := repo.GetTenantByID(tenantID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Tenant not found"})
		return
	}

	if existing, err := repo.GetTenantCreditPool(tenantID); err == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{
			"success":    false,
			"message":    "Credit pool already exists for tenant",
			"error_code": "POOL_ALREADY_EXISTS",
		})
		return
	}

	var req struct {
		MaxBalance        int64  `json:"max_balance"`
		ResetPeriod       string `json:"reset_period"`
		AlertThresholdPct int    `json:"alert_threshold_pct"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request: " + err.Error()})
		return
	}
	if req.MaxBalance < 0 && req.MaxBalance != repo.PoolMaxBalanceUnlimited {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "max_balance must be >= 0 or -1 (unlimited)",
		})
		return
	}

	actorID := c.GetInt("id")
	pool, err := repo.CreateTenantCreditPool(tenantID, actorID, req.MaxBalance, req.ResetPeriod, req.AlertThresholdPct)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create pool: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"success": true, "data": pool})
}

// GetCreditPool returns the pool row for a tenant.
// Route: GET /api/v2/admin/tenants/:id/credit-pool
// Returns 404 with ErrPoolNotFound — distinct from the relay-gate semantics
// where absence means "unlimited bypass".
func GetCreditPool(c *gin.Context) {
	tenantID := c.Param("id")
	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		if errors.Is(err, repo.ErrPoolNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"success":    false,
				"message":    "Credit pool not configured for tenant",
				"error_code": "POOL_NOT_FOUND",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": pool})
}

// endUserPoolView is the read-only projection returned to EndUsers from
// GET /api/v2/:tenant_slug/credit-pool/me. Strictly whitelisted — never
// uses the full TenantCreditPool struct so admin-only fields (ceiling
// policy, alert thresholds, owner, reset schedule) cannot leak even if
// new fields land on the entity.
//
// Pointers let null serialise for "unlimited" pools (no ceiling = no
// meaningful current/max — the relay gate is a no-op for these).
type endUserPoolView struct {
	CurrentBalance *int64 `json:"current_balance"`
	MaxBalance     *int64 `json:"max_balance"`
	Health         string `json:"health"`
}

// poolHealthForEndUser maps a pool balance to one of four enum values
// safe to render in the EndUser dashboard. Thresholds match the
// front-end CreditPoolDrawer.poolHealth helper so admin + EndUser
// surfaces agree on the same band names.
//
//   - "green"     balance >= 20% of max
//   - "yellow"    balance > 0 but < 20% of max
//   - "red"       balance == 0 (gate is now blocking)
//   - "unlimited" no ceiling configured (relay gate skipped)
func poolHealthForEndUser(pool *repo.TenantCreditPool) string {
	if pool == nil || pool.MaxBalance == repo.PoolMaxBalanceUnlimited {
		return "unlimited"
	}
	if pool.CurrentBalance <= 0 {
		return "red"
	}
	// 20% threshold via integer arithmetic — avoids float drift on
	// tiny ceilings (e.g. max=10 should still report "yellow" at 1).
	if pool.CurrentBalance*5 < pool.MaxBalance {
		return "yellow"
	}
	return "green"
}

// GetCreditPoolForEndUser returns the pool summary for the authenticated
// EndUser. Route: GET /api/v2/:tenant_slug/credit-pool/me.
// Auth:  OIDCAuth — tenantCtx.TenantID is derived from the JWT.
//
// Tier 1.2 (2026-05-19): EndUsers were learning their pool was exhausted
// only by hitting HTTP 402 from the relay gate. This endpoint lets them
// poll proactively so the dashboard can warn before traffic breaks.
//
// Security:
//   - URL tenant_slug MUST resolve to the same tenant as the JWT's
//     org claim — 403 TENANT_MISMATCH otherwise. Without this guard
//     an EndUser with a valid JWT for tenant A could read tenant B's
//     pool by typing the slug.
//   - Response struct (endUserPoolView) is a strict whitelist — admin
//     fields (ceiling policy, owner_user_id, alert_threshold_pct,
//     reset_period, last_topup_at, draws) never serialise.
//   - ErrPoolNotFound returns 200 with the "unlimited" projection, not
//     404 — matches the relay gate semantic where absence = unlimited.
func GetCreditPoolForEndUser(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil || tenantCtx == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Tenant context not found",
			"error_code": "UNAUTHENTICATED",
		})
		return
	}

	tenant, terr := repo.GetTenantBySlug(c.Param("tenant_slug"))
	if terr != nil || tenant == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Tenant not found",
			"error_code": "TENANT_NOT_FOUND",
		})
		return
	}

	if tenant.Id != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "Authenticated tenant does not match URL slug",
			"error_code": "TENANT_MISMATCH",
		})
		return
	}

	pool, perr := repo.GetTenantCreditPool(tenant.Id)
	if perr != nil {
		if errors.Is(perr, repo.ErrPoolNotFound) {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": endUserPoolView{
					CurrentBalance: nil,
					MaxBalance:     nil,
					Health:         "unlimited",
				},
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to read credit pool: " + perr.Error(),
			"error_code": "POOL_READ_FAILED",
		})
		return
	}

	view := endUserPoolView{Health: poolHealthForEndUser(pool)}
	// Always expose current_balance — informative even for unlimited.
	cb := pool.CurrentBalance
	view.CurrentBalance = &cb
	if pool.MaxBalance != repo.PoolMaxBalanceUnlimited {
		mb := pool.MaxBalance
		view.MaxBalance = &mb
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// TopupCreditPool funds the pool from the actor's platform wallet.
// Route: POST /api/v2/admin/tenants/:id/credit-pool/topup
// Body:  { amount, reason? }
//
// Flow per ADR §9 Q4 (wallet-debit only — no admin-grant path):
//  1. Lookup pool (must exist).
//  2. DebitWalletGRPC(amount) — if it fails, return 402.
//  3. TopupPool(amount) — if it fails (ErrPoolWouldExceedCeiling), call
//     CreditWalletGRPC for revert; if revert ALSO fails the debit is stranded:
//     it is persisted as an open credit_pool_fund_events row (source =
//     "topup_stranded") that the background reconcile sweep compensates
//     (app.ReconcileStrandedTopups), and the STRANDED log line remains as
//     operator context.
//
// The two-step is necessarily non-atomic across services, so we accept a
// narrow stranded-debit window — but every stranded debit is now durable,
// metered (newhub_credit_pool_stranded_*) and auto-compensated, not log-only.
// A retried request with the same Idempotency-Key settles its own stranded
// event via the claim protocol instead of double-crediting (see
// app.TryFinalizeStrandedTopup).
func TopupCreditPool(c *gin.Context) {
	tenantID := c.Param("id")

	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false, "message": "Credit pool not found", "error_code": "POOL_NOT_FOUND",
		})
		return
	}

	var req struct {
		Amount int64  `json:"amount" binding:"required"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "amount must be a positive integer"})
		return
	}

	actorID := c.GetInt("id")
	actor, err := repo.GetUserById(actorID, false)
	if err != nil || actor == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Actor not found"})
		return
	}
	if actor.LurusAccountID == nil || *actor.LurusAccountID <= 0 {
		c.JSON(http.StatusPreconditionFailed, gin.H{
			"success": false, "message": "Actor has no platform wallet (lurus_account_id unset)",
		})
		return
	}
	accountID := *actor.LurusAccountID

	walletAmount := float64(req.Amount) / 1000.0 // 1 LB ≈ 1000 quota units, matches existing relay accounting
	// Idempotency key per topup intent (contracts.md S1 / ADR D4 "deterministic
	// business key, never random"): honour a caller-supplied Idempotency-Key so a
	// double-clicked/retried topup dedupes against double-charge; fall back to a
	// per-request UUID only when absent (no client key → treated as a fresh intent,
	// NOT content-hashed: a content hash would dedupe two legitimately-identical
	// topups into one wallet debit but credit the pool twice). The same key flows
	// to the gRPC debit and its HTTP twin; the revert uses a distinct key so it is
	// never deduped against the debit.
	idemKey := c.GetHeader("Idempotency-Key")
	if idemKey == "" {
		idemKey = c.GetHeader("X-Idempotency-Key")
	}
	if idemKey == "" {
		idemKey = "pool-topup:" + uuid.NewString()
	}
	debit, derr := debitWallet(
		c.Request.Context(), accountID, walletAmount,
		"pool_topup", "Credit pool topup for tenant "+tenantID, "newhub", idemKey,
	)
	if derr != nil || debit == nil || !debit.Success {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"success":    false,
			"message":    "Wallet debit failed; topup aborted",
			"error_code": "WALLET_DEBIT_FAILED",
		})
		return
	}

	// Retry of a previously-stranded intent (same Idempotency-Key): the debit
	// above was deduped upstream, so the money was only taken once. Settle the
	// stranded event via its claim protocol instead of running a fresh
	// TopupPool — a blind second credit here plus the background reconcile
	// sweep would double-credit a single debit.
	if evt, handled, ferr := app.TryFinalizeStrandedTopup(c.Request.Context(), idemKey, tenantID); handled {
		if ferr != nil {
			status := http.StatusInternalServerError
			code := "POOL_TOPUP_FAILED"
			if errors.Is(ferr, repo.ErrPoolWouldExceedCeiling) {
				status = http.StatusConflict
				code = "POOL_CEILING_EXCEEDED"
			}
			c.JSON(status, gin.H{"success": false, "message": ferr.Error(), "error_code": code})
			return
		}
		metrics.CreditPoolBalance.WithLabelValues(tenantID).Set(float64(evt.NewBalance))
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"tenant_id":   tenantID,
				"new_balance": evt.NewBalance,
				"max_balance": pool.MaxBalance,
				"reconciled":  true,
			},
		})
		return
	}

	newBalance, terr := repo.TopupPool(pool.ID, tenantID, req.Amount, actorID, req.Reason)
	if terr != nil {
		// Revert the wallet — best effort.
		if rerr := creditWallet(
			c.Request.Context(), accountID, walletAmount,
			"pool_topup_revert",
			"Revert: pool topup failed for tenant "+tenantID,
			"newhub", idemKey+":revert",
		); rerr != nil {
			// Stranded debit: money left the wallet, pool was not credited,
			// revert failed too. Persist an open fund event so the background
			// reconcile sweep (app.ReconcileStrandedTopups) compensates it —
			// the log line is context for operators, no longer the only trace.
			if serr := app.RecordStrandedTopup(c.Request.Context(), idemKey, tenantID, pool.ID, req.Amount); serr != nil {
				common.SysError("failed to persist stranded topup event (log is the only trace!) " +
					"event_id=" + idemKey + " err=" + serr.Error())
			}
			common.SysError("STRANDED wallet debit — pool topup AND revert both failed. " +
				"event_id=" + idemKey +
				" account=" + strconv.FormatInt(accountID, 10) +
				" tenant=" + tenantID +
				" amount=" + strconv.FormatInt(req.Amount, 10) +
				" pool_err=" + terr.Error() + " revert_err=" + rerr.Error())
		}
		status := http.StatusInternalServerError
		code := "POOL_TOPUP_FAILED"
		if errors.Is(terr, repo.ErrPoolWouldExceedCeiling) {
			status = http.StatusConflict
			code = "POOL_CEILING_EXCEEDED"
		}
		c.JSON(status, gin.H{"success": false, "message": terr.Error(), "error_code": code})
		return
	}

	metrics.CreditPoolBalance.WithLabelValues(tenantID).Set(float64(newBalance))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tenant_id":   tenantID,
			"new_balance": newBalance,
			"max_balance": pool.MaxBalance,
		},
	})
}

// ListCreditPoolUsage returns paginated draw history.
// Route: GET /api/v2/admin/tenants/:id/credit-pool/usage?offset=&limit=
func ListCreditPoolUsage(c *gin.Context) {
	tenantID := c.Param("id")
	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false, "message": "Credit pool not found", "error_code": "POOL_NOT_FOUND",
		})
		return
	}

	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	draws, total, err := repo.ListPoolDraws(pool.ID, offset, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"draws":  draws,
			"total":  total,
			"offset": offset,
			"limit":  limit,
		},
	})
}

// DeleteCreditPool soft-drains the pool: sets max_balance to zero so the
// relay gate blocks new debits, and records a final adjustment draw.
// Route: DELETE /api/v2/admin/tenants/:id/credit-pool
//
// We do NOT hard-delete: the audit ledger references pool_id; keeping the
// row preserves draw history. A future "recreate" path can reset the row.
func DeleteCreditPool(c *gin.Context) {
	tenantID := c.Param("id")
	pool, err := repo.GetTenantCreditPool(tenantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false, "message": "Credit pool not found", "error_code": "POOL_NOT_FOUND",
		})
		return
	}

	if err := repo.DB.Model(&repo.TenantCreditPool{}).
		Where("id = ?", pool.ID).
		Update("max_balance", 0).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	drained := &repo.TenantCreditPoolDraw{
		PoolID:      pool.ID,
		TenantID:    tenantID,
		Direction:   repo.PoolDrawDirectionDebit,
		Amount:      pool.CurrentBalance,
		Reason:      repo.PoolDrawReasonAdjustment,
		ActorUserID: c.GetInt("id"),
	}
	if err := repo.DB.Create(drained).Error; err != nil {
		common.SysError("DeleteCreditPool: drained-draw insert failed: " + err.Error())
	}

	metrics.CreditPoolBalance.WithLabelValues(tenantID).Set(0)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Credit pool drained"})
}
