package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

const (
	maxInvoiceMonths = 12
)

// invoiceMonthBucket holds aggregated spend for one calendar month.
//
// There is intentionally no `amount_usd` field. A prior version emitted one
// by copying amount_cny 1:1, which is off by the real CNY/USD rate (~7x) —
// there is no FX source wired into this package. Add it back only once a
// real conversion is available (see operation_setting.USDExchangeRate for
// the site-wide rate already used elsewhere, if that becomes the source).
type invoiceMonthBucket struct {
	Month        string  `json:"month"`         // "YYYY-MM"
	Quota        int64   `json:"quota"`         // raw quota units consumed
	AmountCNY    float64 `json:"amount_cny"`    // quota / QuotaPerUnit (1 CNY per QuotaPerUnit)
	RequestCount int64   `json:"request_count"` // number of log rows
}

// ListInvoicesV2 returns monthly spend buckets for the authenticated user in
// the given tenant scope.
//
// GET /api/v2/:tenant_slug/billing/invoices?from=YYYY-MM&to=YYYY-MM
//
// Both `from` and `to` are optional. When omitted the endpoint returns the
// most recent 12 calendar months. The range is always capped at 12 months
// regardless of what the caller provides — data older than that would require
// a separate archive endpoint.
func ListInvoicesV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "tenant context not found",
		})
		return
	}

	userID := tenantCtx.UserID
	tenantID := tenantCtx.TenantID

	from, to, parseErr := parseBillingMonthRange(c.Query("from"), c.Query("to"))
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("invalid date range: %v", parseErr),
		})
		return
	}

	// Translate month boundaries to unix timestamps that match the Log.CreatedAt
	// bigint column (seconds since epoch).
	fromTS := from.Unix()
	toTS := to.Unix()

	buckets, err := aggregateInvoiceMonths(userID, tenantID, fromTS, toTS)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "failed to query invoice data",
		})
		return
	}

	// Summary aggregates across the returned range.
	var totalQuota int64
	var totalCNY float64
	// MTD: find the bucket for the current calendar month.
	now := time.Now().UTC()
	currentMonth := fmt.Sprintf("%04d-%02d", now.Year(), now.Month())
	var mtdQuota int64
	for _, b := range buckets {
		totalQuota += b.Quota
		totalCNY += b.AmountCNY
		if b.Month == currentMonth {
			mtdQuota = b.Quota
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":          buckets,
			"total_invoices": len(buckets),
			"summary": gin.H{
				"total_quota": totalQuota,
				"total_cny":   totalCNY,
				"mtd_quota":   mtdQuota,
			},
		},
	})
}

// parseBillingMonthRange validates and clamps the from/to month query params.
// Both are optional; defaults produce a trailing 12-month window ending at the
// current month. The returned times are the start of `from` and the exclusive
// end of `to` (i.e. first second of the month after `to`).
func parseBillingMonthRange(fromStr, toStr string) (from time.Time, toExcl time.Time, err error) {
	now := time.Now().UTC()
	// Default: last 12 months (inclusive of current month)
	defaultTo := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC) // start of next month = exclusive end of this month
	defaultFrom := defaultTo.AddDate(0, -maxInvoiceMonths, 0)

	if fromStr == "" && toStr == "" {
		return defaultFrom, defaultTo, nil
	}

	if fromStr != "" {
		t, e := time.Parse("2006-01", fromStr)
		if e != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from must be YYYY-MM, got %q", fromStr)
		}
		from = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	} else {
		from = defaultFrom
	}

	if toStr != "" {
		t, e := time.Parse("2006-01", toStr)
		if e != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to must be YYYY-MM, got %q", toStr)
		}
		// Exclusive end: first second of the month after `to`
		toExcl = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	} else {
		toExcl = defaultTo
	}

	if !from.Before(toExcl) {
		return time.Time{}, time.Time{}, fmt.Errorf("from must be before to")
	}

	// Clamp to at most maxInvoiceMonths regardless of caller input.
	if months := monthsBetween(from, toExcl); months > maxInvoiceMonths {
		from = toExcl.AddDate(0, -maxInvoiceMonths, 0)
	}

	return from, toExcl, nil
}

// monthsBetween returns the number of whole months from a to b (b > a assumed).
func monthsBetween(a, b time.Time) int {
	years := b.Year() - a.Year()
	months := int(b.Month()) - int(a.Month())
	return years*12 + months
}

// rawMonthRow is the scan target for the per-database-dialect GROUP BY query.
type rawMonthRow struct {
	Month        string `gorm:"column:month"`
	QuotaSum     int64  `gorm:"column:quota_sum"`
	RequestCount int64  `gorm:"column:request_count"`
}

// aggregateInvoiceMonths runs the per-month GROUP BY depending on whether
// we are connected to PostgreSQL or SQLite. The SQLite arm exists only for
// the hermetic glebarez unit-test tier; runtime is always PostgreSQL.
func aggregateInvoiceMonths(userID int, tenantID string, fromTS, toTS int64) ([]invoiceMonthBucket, error) {
	var rows []rawMonthRow

	if common.UsingPostgreSQL {
		err := repo.LOG_DB.
			Model(&repo.Log{}).
			Select(
				"TO_CHAR(TO_TIMESTAMP(created_at), 'YYYY-MM') AS month, "+
					"COALESCE(SUM(quota), 0) AS quota_sum, "+
					"COUNT(*) AS request_count",
			).
			Where("user_id = ? AND tenant_id = ? AND type = ? AND created_at >= ? AND created_at < ?",
				userID, tenantID, repo.LogTypeConsume, fromTS, toTS).
			Group("TO_CHAR(TO_TIMESTAMP(created_at), 'YYYY-MM')").
			Order("month DESC").
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
	} else {
		// SQLite fallback — strftime on unix epoch.
		err := repo.LOG_DB.
			Model(&repo.Log{}).
			Select(
				"STRFTIME('%Y-%m', DATETIME(created_at, 'unixepoch')) AS month, "+
					"COALESCE(SUM(quota), 0) AS quota_sum, "+
					"COUNT(*) AS request_count",
			).
			Where("user_id = ? AND tenant_id = ? AND type = ? AND created_at >= ? AND created_at < ?",
				userID, tenantID, repo.LogTypeConsume, fromTS, toTS).
			Group("STRFTIME('%Y-%m', DATETIME(created_at, 'unixepoch'))").
			Order("month DESC").
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
	}

	buckets := make([]invoiceMonthBucket, 0, len(rows))
	for _, r := range rows {
		amountCNY := float64(r.QuotaSum) / common.QuotaPerUnit
		buckets = append(buckets, invoiceMonthBucket{
			Month:        r.Month,
			Quota:        r.QuotaSum,
			AmountCNY:    amountCNY,
			RequestCount: r.RequestCount,
		})
	}
	return buckets, nil
}
