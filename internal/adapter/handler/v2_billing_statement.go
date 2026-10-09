package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
	// The statement resolves a caller-chosen IANA zone; a minimal runtime
	// image has no system tzdata, so embed it rather than 400 every request.
	_ "time/tzdata"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/currency"

	"github.com/gin-gonic/gin"
)

const statementDefaultTZ = "Asia/Shanghai"

// statementRow is one line of the tenant monthly statement. Amounts are in
// 0.0001 CNY (the wallet's precision). charged_cny4 is what the platform
// wallet was actually debited; priced_cny4 is what the rows WITHOUT a wallet
// record were worth (credit-pool / local-quota spend, and pre-042 rows priced
// from their quota at today's rate) — the same split the monthly invoice uses.
type statementRow struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Requests    int64  `json:"requests"`
	Quota       int64  `json:"quota"`
	ChargedCNY4 int64  `json:"charged_cny4"`
	PricedCNY4  int64  `json:"priced_cny4"`
}

// statementGroupExpr maps group_by to the SQL grouping expression. The values
// are literals chosen here, never caller text.
var statementGroupExpr = map[string]string{
	"project":  "CAST(project_id AS TEXT)",
	"employee": "employee_ref",
	"token":    "CAST(token_id AS TEXT)",
	"model":    "model_name",
}

// parseStatementMonth resolves month (YYYY-MM, default: the current month in
// tz) to the [start, end) unix-second window of that calendar month in tz.
func parseStatementMonth(month string, loc *time.Location) (start, end time.Time, label string, err error) {
	if month == "" {
		now := time.Now().In(loc)
		month = fmt.Sprintf("%04d-%02d", now.Year(), int(now.Month()))
	}
	start, err = time.ParseInLocation("2006-01", month, loc)
	if err != nil {
		return time.Time{}, time.Time{}, "", fmt.Errorf("month must be YYYY-MM, got %q", month)
	}
	return start, start.AddDate(0, 1, 0), month, nil
}

// GetBillingStatementV2 is the tenant monthly statement: every billable
// consume row of the calendar month (in the caller's time zone) grouped by
// project, employee, token or model, with totals that are exactly the sum of the rows.
//
// GET /api/v2/:tenant_slug/billing/statement?month=YYYY-MM&tz=Asia/Shanghai&group_by=project|employee|token|model[&format=csv]
//
// Tenant admins see the whole tenant. A department lead sees only the rows of
// their own projects — the unassigned (project 0) bucket is therefore hidden
// from them, and totals only add up what they can see. Everyone else gets 403.
func GetBillingStatementV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "tenant context not found"})
		return
	}
	scope := resolveLogReadScope(c, tenantCtx)
	if !requireTenantAdmin(c, tenantCtx) && !scope.deptLead {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Admin role required"})
		return
	}

	badRequest := func(msg string) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": msg})
	}
	tz := c.DefaultQuery("tz", statementDefaultTZ)
	loc, locErr := time.LoadLocation(tz)
	if locErr != nil || tz == "" {
		badRequest(fmt.Sprintf("invalid tz %q", tz))
		return
	}
	groupBy := c.DefaultQuery("group_by", "project")
	groupExpr, ok := statementGroupExpr[groupBy]
	if !ok {
		badRequest("group_by must be project, employee, token or model")
		return
	}
	start, end, month, monthErr := parseStatementMonth(c.Query("month"), loc)
	if monthErr != nil {
		badRequest(monthErr.Error())
		return
	}

	rows, err := queryStatementRows(tenantCtx.TenantID, groupBy, groupExpr, start.Unix(), end.Unix(), scope.projects())
	if err != nil {
		common.SysError("GetBillingStatementV2: aggregate failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to query statement"})
		return
	}
	var totals statementRow
	for _, r := range rows {
		totals.Requests += r.Requests
		totals.Quota += r.Quota
		totals.ChargedCNY4 += r.ChargedCNY4
		totals.PricedCNY4 += r.PricedCNY4
	}

	if c.Query("format") == "csv" {
		writeStatementCSV(c, month, rows, totals)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"month":    month,
			"tz":       tz,
			"group_by": groupBy,
			"start":    start.Unix(),
			"end":      end.Unix(),
			"rows":     rows,
			"totals":   totals,
		},
	})
}

// queryStatementRows aggregates the tenant's billable consume rows in
// [fromTS, toTS) by the group expression. projectIDs != nil restricts it to
// those projects (empty non-nil matches nothing).
func queryStatementRows(tenantID, groupBy, groupExpr string, fromTS, toTS int64, projectIDs []int) ([]statementRow, error) {
	var raw []struct {
		Grp           string `gorm:"column:grp"`
		Label         string `gorm:"column:label"`
		Requests      int64  `gorm:"column:requests"`
		Quota         int64  `gorm:"column:quota"`
		Charged       int64  `gorm:"column:charged"`
		Priced        int64  `gorm:"column:priced"`
		UnpricedQuota int64  `gorm:"column:unpriced_quota"`
	}
	tx := repo.LOG_DB.Model(&repo.Log{}).
		Scopes(repo.BillableConsumePredicate).
		Where("tenant_id = ? AND created_at >= ? AND created_at < ?", tenantID, fromTS, toTS)
	tx = repo.ApplyLogAttributionFilters(tx, projectIDs, "")
	if err := tx.
		Select(groupExpr + " AS grp, " +
			"COALESCE(MAX(token_name), '') AS label, " +
			"COUNT(*) AS requests, " +
			"COALESCE(SUM(quota), 0) AS quota, " +
			"COALESCE(SUM(charged_cny4), 0) AS charged, " +
			"COALESCE(SUM(CASE WHEN charged_cny4 = 0 THEN priced_cny4 ELSE 0 END), 0) AS priced, " +
			"COALESCE(SUM(CASE WHEN charged_cny4 = 0 AND priced_cny4 = 0 THEN quota ELSE 0 END), 0) AS unpriced_quota").
		Group(groupExpr).
		Scan(&raw).Error; err != nil {
		return nil, err
	}

	var names map[int]string
	if groupBy == "project" {
		ids := make([]int, 0, len(raw))
		for _, r := range raw {
			if id, convErr := strconv.Atoi(r.Grp); convErr == nil {
				ids = append(ids, id)
			}
		}
		var nameErr error
		if names, nameErr = repo.ResolveProjectNames(tenantID, ids); nameErr != nil {
			// Non-fatal: key stays the stable join column.
			common.SysError("GetBillingStatementV2: resolve project names: " + nameErr.Error())
			names = map[int]string{}
		}
	}

	rows := make([]statementRow, 0, len(raw))
	for _, r := range raw {
		row := statementRow{
			Key:         r.Grp,
			Requests:    r.Requests,
			Quota:       r.Quota,
			ChargedCNY4: r.Charged,
			// Rows with neither record are priced from their quota, rounded
			// per row so the totals (the sum of rows) stay exact integers.
			PricedCNY4: r.Priced + currency.CNYToUnits4(currency.QuotaToCNY(int(r.UnpricedQuota))),
		}
		switch groupBy {
		case "project":
			if id, convErr := strconv.Atoi(r.Grp); convErr == nil {
				row.Label = names[id]
			}
		case "employee":
			row.Label = r.Grp
		case "token":
			row.Label = r.Label
		case "model":
			row.Label = r.Grp
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Quota != rows[j].Quota {
			return rows[i].Quota > rows[j].Quota
		}
		return rows[i].Key < rows[j].Key
	})
	return rows, nil
}

// statementCSVHeader is the fixed column order of the CSV form.
var statementCSVHeader = []string{"key", "label", "requests", "quota", "charged_cny4", "priced_cny4"}

// writeStatementCSV streams the statement as CSV: one line per row, then a
// final totals line keyed "*" (which no project id, token id or employee_ref
// can collide with: refs are limited to [A-Za-z0-9._@:-]).
func writeStatementCSV(c *gin.Context, month string, rows []statementRow, totals statementRow) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="statement-%s-%s.csv"`, c.Param("tenant_slug"), month))
	c.Header("Cache-Control", "no-cache")
	c.Status(http.StatusOK)
	w := csv.NewWriter(c.Writer)
	defer w.Flush()
	line := func(r statementRow) []string {
		return csvRow(r.Key, r.Label, strconv.FormatInt(r.Requests, 10), strconv.FormatInt(r.Quota, 10),
			strconv.FormatInt(r.ChargedCNY4, 10), strconv.FormatInt(r.PricedCNY4, 10))
	}
	if err := w.Write(statementCSVHeader); err != nil {
		common.SysError("GetBillingStatementV2: write csv header: " + err.Error())
		return
	}
	for _, r := range rows {
		if err := w.Write(line(r)); err != nil {
			common.SysError("GetBillingStatementV2: write csv row: " + err.Error())
			return
		}
	}
	totals.Key, totals.Label = "*", "total"
	if err := w.Write(line(totals)); err != nil {
		common.SysError("GetBillingStatementV2: write csv totals: " + err.Error())
	}
}
