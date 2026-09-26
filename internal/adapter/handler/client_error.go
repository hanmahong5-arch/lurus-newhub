package handler

import (
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
)

// clientErrorReport is what the console's reporter sends. Every field is
// untrusted and optional; the kind is folded to a fixed set by
// metrics.RecordClientError, and the free-text fields only ever reach the
// log, bounded and stripped of control characters.
type clientErrorReport struct {
	Kind    string `json:"kind"`
	Page    string `json:"page"`
	Message string `json:"message"`
	Version string `json:"version"`
}

const (
	clientErrorMaxBody    = 4 << 10
	clientErrorMaxMessage = 300
	clientErrorMaxPage    = 128
	clientErrorMaxVersion = 40
)

// ReportClientError records an error a browser reported: one counter
// increment (lurus_gateway_client_errors_total{kind}) and one bounded log
// line carrying the request id, so an operator can go from an alarm to the
// page and the release that broke. It never fails the caller: a reporter
// that gets an error back has nowhere to report it.
//
// Route: POST /api/client-error (no auth: a console can crash before or
// during login; per-IP rate limited in its own bucket).
func ReportClientError(c *gin.Context) {
	var r clientErrorReport
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, clientErrorMaxBody)
	if err := c.ShouldBindJSON(&r); err != nil {
		c.Status(http.StatusNoContent)
		return
	}
	metrics.RecordClientError(r.Kind)
	common.SysLog(fmt.Sprintf("client error: kind=%s page=%s version=%s request_id=%s message=%s",
		logSafe(r.Kind, 32), logSafe(r.Page, clientErrorMaxPage), logSafe(r.Version, clientErrorMaxVersion),
		c.GetString(common.RequestIdKey), logSafe(r.Message, clientErrorMaxMessage)))
	c.Status(http.StatusNoContent)
}

// logSafe bounds s and drops control characters, so a report cannot forge
// extra log lines or flood one.
func logSafe(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return fmt.Sprintf("%q", s)
}
