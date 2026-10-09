package middleware

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// identity_attribution.go — enterprise per-employee / per-department
// attribution (docs/plans/enterprise-hub-2026-10-07.md section 2.1).
//
// A customer's own gateway names who is calling through two headers. They are
// client-controlled text and the host nginx forwards every client header, so
// the pod cannot tell a gateway from a browser by address: trust is decided
// per KEY (tokens.trusted_identity_headers), never per header or per IP.
//
// Both results are labels that end up stamped permanently on log rows. They
// are not authorization inputs: nothing gates on them.

const (
	// HeaderEmployee carries the employee reference, [A-Za-z0-9._@:-]{1,64}.
	HeaderEmployee = "X-Lurus-Employee"
	// HeaderDept carries a department code (projects.external_code) or name.
	HeaderDept = "X-Lurus-Dept"

	// identityAttributionDoneKey marks a request whose attribution is resolved.
	identityAttributionDoneKey = "identity_attribution_done"

	maxEmployeeRefLen = 64
	// maxDeptHeaderLen matches projects.name (varchar 128); a longer value
	// cannot name any project.
	maxDeptHeaderLen = 128
)

// validEmployeeRef reports whether s is an acceptable X-Lurus-Employee value.
func validEmployeeRef(s string) bool {
	if s == "" || len(s) > maxEmployeeRefLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == '.', ch == '_', ch == '@', ch == ':', ch == '-':
		default:
			return false
		}
	}
	return true
}

// validDeptHeader accepts any printable UTF-8 text (department names are
// often not ASCII) up to maxDeptHeaderLen bytes.
func validDeptHeader(s string) bool {
	if s == "" || len(s) > maxDeptHeaderLen || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return false
		}
	}
	return true
}

// applyIdentityAttribution resolves the request's employee and project and
// stores them on the context (ContextKeyEmployeeRef / ContextKeyProjectId).
// Priority: trusted header > the token's own value. The token's project is
// already on the context when this runs; it is overwritten only when a valid
// department header names a live project of THE TOKEN'S TENANT.
func applyIdentityAttribution(c *gin.Context, token *repo.Token) {
	// Resolve once per request: the playground handler re-runs
	// SetupContextForToken with a stripped temp token, which must neither wipe
	// the first result nor count the same untrusted request twice.
	if _, done := c.Get(identityAttributionDoneKey); done {
		return
	}
	c.Set(identityAttributionDoneKey, true)
	common.SetContextKey(c, constant.ContextKeyEmployeeRef, token.EmployeeRef)

	if c.Request == nil {
		return
	}
	empRaw := strings.TrimSpace(c.GetHeader(HeaderEmployee))
	deptRaw := strings.TrimSpace(c.GetHeader(HeaderDept))
	if empRaw == "" && deptRaw == "" {
		return
	}
	if !token.TrustedIdentityHeaders {
		// Ignore silently (no error: a browser extension or proxy may add
		// headers); count once per request so an operator can see it.
		metrics.RecordIdentityHeaderUntrusted()
		return
	}

	if empRaw != "" {
		if validEmployeeRef(empRaw) {
			common.SetContextKey(c, constant.ContextKeyEmployeeRef, empRaw)
		} else {
			metrics.RecordIdentityHeaderInvalid("employee")
		}
	}
	if deptRaw != "" {
		if !validDeptHeader(deptRaw) {
			metrics.RecordIdentityHeaderInvalid("dept")
			return
		}
		tenantID := token.TenantId
		if tenantID == "" {
			tenantID = "default"
		}
		if pid, ok := repo.ResolveProjectByDeptCode(tenantID, deptRaw); ok {
			common.SetContextKey(c, constant.ContextKeyProjectId, pid)
		} else {
			// Keep the token's own project; never create one from a header.
			metrics.RecordIdentityHeaderUnknownDept()
		}
	}
}
