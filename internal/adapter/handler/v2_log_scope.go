package handler

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-gonic/gin"
)

// logReadScope is the data scope of the tenant log read routes (list, stat,
// export, rankings, statement) for callers below tenant admin. A department
// lead sees every member's rows of the projects they belong to, not just
// their own; anyone else keeps the plain per-user view (zero value).
type logReadScope struct {
	deptLead bool
	// projectIDs is never nil for a dept_lead: an empty slice means "no
	// project" and matches nothing (fail closed), it must never degrade into
	// "no project filter".
	projectIDs []int
}

// resolveLogReadScope derives the scope from allowedProjectIDs. Platform staff
// and tenant admins get the zero value: their routes keep their own semantics.
func resolveLogReadScope(c *gin.Context, tc *middleware.TenantContext) logReadScope {
	all, ids := allowedProjectIDs(c, tc)
	if all || tenantRoleOf(tc) != entity.TenantRoleDeptLead {
		return logReadScope{}
	}
	if ids == nil {
		ids = []int{}
	}
	return logReadScope{deptLead: true, projectIDs: ids}
}

// forbids reports whether an explicit project_id filter reaches outside the
// scope. Only a dept_lead can be out of scope.
func (s logReadScope) forbids(projectID int) bool {
	return s.deptLead && projectID > 0 && !slices.Contains(s.projectIDs, projectID)
}

// rejectProject writes the 404 for an out-of-scope project_id and reports
// whether it did. 404, not 403: a lead must not be able to probe which other
// project ids exist.
func (s logReadScope) rejectProject(c *gin.Context, projectID int) bool {
	if !s.forbids(projectID) {
		return false
	}
	c.JSON(http.StatusNotFound, gin.H{
		"success": false,
		"message": "Project not found",
	})
	return true
}

// userID is the user_id filter for the scope: a dept_lead is scoped by
// project, everyone else by their own user id.
func (s logReadScope) userID(own int) int {
	if s.deptLead {
		return 0
	}
	return own
}

// projects is the ProjectIDs filter for repo.LogQueryParams (nil = none).
func (s logReadScope) projects() []int {
	if s.deptLead {
		return s.projectIDs
	}
	return nil
}

// logOtherString reads one top-level string key out of a log row's Other JSON
// ("" when absent, not a string, or the payload is not valid JSON).
func logOtherString(other, key string) string {
	if other == "" {
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(other), &m) != nil {
		return ""
	}
	var out string
	if raw, ok := m[key]; ok && json.Unmarshal(raw, &out) == nil {
		return out
	}
	return ""
}
