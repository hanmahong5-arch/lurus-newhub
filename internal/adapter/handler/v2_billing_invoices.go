package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"
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
//
// Quota/AmountCNY/RequestCount cover only rows repo.BillableConsumePredicate
// accepts — a settlement that SettleConsume flagged failed, or a manual
// channel-test probe, no longer count as a customer charge (cycle13 L2).
// UnbilledQuota/UnbilledRequestCount are the rest of that month's
// type=consume rows: quota that was logged but that nobody's wallet ever
// paid, surfaced instead of silently dropped so an operator reconciling a
// month's total against a raw log count is not left wondering where rows
// went.
type invoiceMonthBucket struct {
	Month                string  `json:"month"`                  // "YYYY-MM"
	Quota                int64   `json:"quota"`                  // billable quota units consumed
	AmountCNY            float64 `json:"amount_cny"`             // recorded wallet charges + today's price of unrecorded quota (invoiceAmountCNY)
	RequestCount         int64   `json:"request_count"`          // number of billable log rows
	UnbilledQuota        int64   `json:"unbilled_quota"`         // quota logged but excluded from billing (settlement-failed / channel-test)
	UnbilledRequestCount int64   `json:"unbilled_request_count"` // number of log rows excluded from billing
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
	// ChargedUnits4 sums logs.charged_cny4 (what the wallet was actually
	// debited, 0.0001 CNY); ChargedQuota is the quota of the rows that carry
	// such a record, so the rest can still be priced the old way.
	ChargedUnits4 int64 `gorm:"column:charged_units4"`
	ChargedQuota  int64 `gorm:"column:charged_quota"`
}

// monthGroupExpr is the per-dialect SQL expression that buckets a row's
// unix-epoch created_at into its "YYYY-MM" calendar month. The SQLite arm
// exists only for the hermetic glebarez unit-test tier; runtime is always
// PostgreSQL.
func monthGroupExpr() string {
	if common.UsingPostgreSQL {
		// UTC, like the range bounds: TO_TIMESTAMP yields a timestamptz and
		// TO_CHAR renders it in the SESSION time zone, so on a database set
		// to Asia/Shanghai a row from 23:30 UTC on the last day of a month
		// was filed under the next month while the bounds counted it here.
		return "TO_CHAR(TO_TIMESTAMP(created_at) AT TIME ZONE 'UTC', 'YYYY-MM')"
	}
	return "STRFTIME('%Y-%m', DATETIME(created_at, 'unixepoch'))"
}

// queryInvoiceMonthRows runs the per-month GROUP BY for one user/tenant/range,
// either over every type=consume row (billableOnly=false) or, scoped through
// repo.BillableConsumePredicate, only the rows that actually settled
// (billableOnly=true). aggregateInvoiceMonths runs both and takes the
// difference to get the unbilled bucket, instead of duplicating
// BillableConsumePredicate's marker strings here (cycle13 L2 — the predicate
// lives in repo/log.go, the one place that owns those literals).
func queryInvoiceMonthRows(userID int, tenantID string, fromTS, toTS int64, billableOnly bool) ([]rawMonthRow, error) {
	var rows []rawMonthRow
	monthExpr := monthGroupExpr()

	tx := repo.LOG_DB.
		Model(&repo.Log{}).
		Select(monthExpr+" AS month, "+
			"COALESCE(SUM(quota), 0) AS quota_sum, "+
			"COALESCE(SUM(charged_cny4), 0) AS charged_units4, "+
			"COALESCE(SUM(CASE WHEN charged_cny4 > 0 THEN quota ELSE 0 END), 0) AS charged_quota, "+
			"COUNT(*) AS request_count").
		Where("user_id = ? AND tenant_id = ? AND created_at >= ? AND created_at < ?",
			userID, tenantID, fromTS, toTS)

	if billableOnly {
		tx = tx.Scopes(repo.BillableConsumePredicate)
	} else {
		tx = tx.Where("type = ?", repo.LogTypeConsume)
	}

	err := tx.Group(monthExpr).Order("month DESC").Scan(&rows).Error
	return rows, err
}

// invoiceAmountCNY is what the month's billable rows cost: the recorded
// wallet charge where settlement wrote one, and the quota priced at today's
// rate only for rows without a record (credit-pool / local-quota spend and
// rows older than logs.charged_cny4). An exchange-rate change therefore no
// longer rewrites a month that was already charged.
func invoiceAmountCNY(r rawMonthRow) float64 {
	return currency.Units4ToCNY(r.ChargedUnits4) + currency.QuotaToCNY(int(r.QuotaSum-r.ChargedQuota))
}

// aggregateInvoiceMonths returns one bucket per calendar month in range,
// split into the billable quota (what repo.BillableConsumePredicate accepts)
// and the unbilled remainder (settlement-failed / channel-test rows —
// cycle13 L2). It runs the GROUP BY twice — once unfiltered, once scoped to
// billable — and subtracts, rather than computing both in one query with a
// hand-rolled CASE WHEN that would have to re-embed the same marker
// substrings BillableConsumePredicate already owns.
func aggregateInvoiceMonths(userID int, tenantID string, fromTS, toTS int64) ([]invoiceMonthBucket, error) {
	allRows, err := queryInvoiceMonthRows(userID, tenantID, fromTS, toTS, false)
	if err != nil {
		return nil, err
	}
	billableRows, err := queryInvoiceMonthRows(userID, tenantID, fromTS, toTS, true)
	if err != nil {
		return nil, err
	}

	billableByMonth := make(map[string]rawMonthRow, len(billableRows))
	for _, r := range billableRows {
		billableByMonth[r.Month] = r
	}

	// Iterate allRows (not billableRows) so the result keeps every month that
	// has ANY consume row, including a month whose rows are entirely
	// unbilled — that used to be silently included in the pre-L2 total, and
	// must still be visible, just moved into unbilled_quota rather than
	// dropped from the response altogether.
	buckets := make([]invoiceMonthBucket, 0, len(allRows))
	for _, all := range allRows {
		billable := billableByMonth[all.Month] // zero value if the month has no billable rows
		buckets = append(buckets, invoiceMonthBucket{
			Month:        all.Month,
			Quota:        billable.QuotaSum,
			AmountCNY:    invoiceAmountCNY(billable),
			RequestCount: billable.RequestCount,
			// The two aggregates are separate round trips; a row that lands
			// between them can make the difference negative, which must not
			// reach a customer-facing response.
			UnbilledQuota:        max(all.QuotaSum-billable.QuotaSum, 0),
			UnbilledRequestCount: max(all.RequestCount-billable.RequestCount, 0),
		})
	}
	return buckets, nil
}
