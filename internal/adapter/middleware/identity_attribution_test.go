package middleware

// identity_attribution_test.go — X-Lurus-Employee / X-Lurus-Dept handling in
// SetupContextForToken (migration 045, docs/plans/enterprise-hub-2026-10-07.md
// section 2.1). Real in-memory SQLite behind repo.DB so the department lookup
// runs the production query, including its tenant clause.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

var identityTestDBCounter atomic.Int64

// identityFixture returns a DB holding two tenants' projects:
//
//	ten-a: "Research" (code R-D), "Finance" (no code)
//	ten-b: "Sales" (code S1), "Finance" (no code)
func identityFixture(t *testing.T) (ids map[string]int) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:identity_attr_%d?mode=memory&cache=shared", identityTestDBCounter.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&entity.Project{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := repo.DB
	repo.DB = db
	repo.ResetProjectDeptCache()
	t.Cleanup(func() { repo.DB = prev; repo.ResetProjectDeptCache() })

	ids = map[string]int{}
	for _, p := range []entity.Project{
		{TenantId: "ten-a", Name: "Research", ExternalCode: "R-D"},
		{TenantId: "ten-a", Name: "Finance"},
		{TenantId: "ten-b", Name: "Sales", ExternalCode: "S1"},
		{TenantId: "ten-b", Name: "Finance"},
	} {
		p := p
		if err := db.Create(&p).Error; err != nil {
			t.Fatalf("seed project: %v", err)
		}
		ids[p.TenantId+"/"+p.Name] = p.Id
	}
	return ids
}

func setupWithHeaders(t *testing.T, tok *repo.Token, headers map[string]string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	if err := SetupContextForToken(c, tok); err != nil {
		t.Fatalf("SetupContextForToken: %v", err)
	}
	return c
}

func gotAttribution(c *gin.Context) (employee string, project int) {
	return common.GetContextKeyString(c, constant.ContextKeyEmployeeRef), common.GetContextKeyInt(c, constant.ContextKeyProjectId)
}

func trustedToken() *repo.Token {
	return &repo.Token{Id: 1, UserId: 1, TenantId: "ten-a", Key: "k", TrustedIdentityHeaders: true, ProjectId: 5, EmployeeRef: "tok-emp"}
}

func TestIdentityHeaders_TrustedKeyHonoured(t *testing.T) {
	ids := identityFixture(t)
	c := setupWithHeaders(t, trustedToken(), map[string]string{HeaderEmployee: "alice@corp.example", HeaderDept: "R-D"})
	emp, proj := gotAttribution(c)
	if emp != "alice@corp.example" || proj != ids["ten-a/Research"] {
		t.Errorf("attribution = %q / %d; want alice@corp.example / %d", emp, proj, ids["ten-a/Research"])
	}
}

func TestIdentityHeaders_UntrustedKeyIgnoredAndCounted(t *testing.T) {
	identityFixture(t)
	tok := trustedToken()
	tok.TrustedIdentityHeaders = false
	before := testutil.ToFloat64(metrics.IdentityHeaderUntrustedTotal)

	c := setupWithHeaders(t, tok, map[string]string{HeaderEmployee: "mallory", HeaderDept: "R-D"})
	emp, proj := gotAttribution(c)
	if emp != "tok-emp" || proj != 5 {
		t.Errorf("untrusted key was attributed %q / %d; want the token's own tok-emp / 5", emp, proj)
	}
	if got := testutil.ToFloat64(metrics.IdentityHeaderUntrustedTotal); got != before+1 {
		t.Errorf("untrusted counter = %v, want %v (one per request)", got, before+1)
	}

	// No header, no count.
	setupWithHeaders(t, tok, nil)
	if got := testutil.ToFloat64(metrics.IdentityHeaderUntrustedTotal); got != before+1 {
		t.Errorf("untrusted counter moved on a request with no identity headers: %v", got)
	}
}

func TestIdentityHeaders_InvalidEmployeeDropped(t *testing.T) {
	identityFixture(t)
	bad := []string{
		"has space",
		"semi;colon",
		"slash/x",
		"unié",
		strings.Repeat("a", 65),
	}
	for _, v := range bad {
		before := testutil.ToFloat64(metrics.IdentityHeaderInvalidTotal.WithLabelValues("employee"))
		c := setupWithHeaders(t, trustedToken(), map[string]string{HeaderEmployee: v})
		if emp, _ := gotAttribution(c); emp != "tok-emp" {
			t.Errorf("employee header %q accepted as %q; want drop and fall back to tok-emp", v, emp)
		}
		if got := testutil.ToFloat64(metrics.IdentityHeaderInvalidTotal.WithLabelValues("employee")); got != before+1 {
			t.Errorf("employee header %q: invalid counter = %v, want %v", v, got, before+1)
		}
	}
	// Boundary: exactly 64 valid characters is accepted.
	ok := strings.Repeat("a", 64)
	c := setupWithHeaders(t, trustedToken(), map[string]string{HeaderEmployee: ok})
	if emp, _ := gotAttribution(c); emp != ok {
		t.Errorf("64-char employee ref rejected: got %q", emp)
	}
}

func TestIdentityHeaders_InvalidDeptDropped(t *testing.T) {
	identityFixture(t)
	for _, v := range []string{strings.Repeat("d", 129), "bad\x01ctl"} {
		before := testutil.ToFloat64(metrics.IdentityHeaderInvalidTotal.WithLabelValues("dept"))
		c := setupWithHeaders(t, trustedToken(), map[string]string{HeaderDept: v})
		if _, proj := gotAttribution(c); proj != 5 {
			t.Errorf("dept header %q changed the project to %d; want the token's 5", v, proj)
		}
		if got := testutil.ToFloat64(metrics.IdentityHeaderInvalidTotal.WithLabelValues("dept")); got != before+1 {
			t.Errorf("dept header %q: invalid counter = %v, want %v", v, got, before+1)
		}
	}
}

func TestIdentityHeaders_DeptCodeBeatsName(t *testing.T) {
	ids := identityFixture(t)
	// A project whose NAME equals another project's CODE: the code must win.
	db := repo.DB
	trap := entity.Project{TenantId: "ten-a", Name: "R-D"}
	if err := db.Create(&trap).Error; err != nil {
		t.Fatal(err)
	}
	repo.ResetProjectDeptCache()

	c := setupWithHeaders(t, trustedToken(), map[string]string{HeaderDept: "R-D"})
	if _, proj := gotAttribution(c); proj != ids["ten-a/Research"] {
		t.Errorf("project = %d; want the external_code match %d, not the name match %d", proj, ids["ten-a/Research"], trap.Id)
	}
	// Name still works when no code matches.
	c = setupWithHeaders(t, trustedToken(), map[string]string{HeaderDept: "Finance"})
	if _, proj := gotAttribution(c); proj != ids["ten-a/Finance"] {
		t.Errorf("name match = %d; want %d", proj, ids["ten-a/Finance"])
	}
}

func TestIdentityHeaders_UnknownDeptKeepsTokenProjectAndCreatesNothing(t *testing.T) {
	identityFixture(t)
	before := testutil.ToFloat64(metrics.IdentityHeaderUnknownDeptTotal)
	var projectsBefore int64
	repo.DB.Model(&entity.Project{}).Count(&projectsBefore)

	c := setupWithHeaders(t, trustedToken(), map[string]string{HeaderEmployee: "bob", HeaderDept: "Nope"})
	emp, proj := gotAttribution(c)
	if proj != 5 {
		t.Errorf("unknown dept changed project to %d; want the token's 5", proj)
	}
	if emp != "bob" {
		t.Errorf("a valid employee header must still apply next to an unknown dept; got %q", emp)
	}
	if got := testutil.ToFloat64(metrics.IdentityHeaderUnknownDeptTotal); got != before+1 {
		t.Errorf("unknown-dept counter = %v, want %v", got, before+1)
	}
	var projectsAfter int64
	repo.DB.Model(&entity.Project{}).Count(&projectsAfter)
	if projectsAfter != projectsBefore {
		t.Errorf("projects %d -> %d: a header created a project", projectsBefore, projectsAfter)
	}
}

func TestIdentityHeaders_OtherTenantsProjectNeverHit(t *testing.T) {
	ids := identityFixture(t)
	// ten-a's key names ten-b's code S1 and the name "Sales".
	for _, dept := range []string{"S1", "Sales"} {
		c := setupWithHeaders(t, trustedToken(), map[string]string{HeaderDept: dept})
		if _, proj := gotAttribution(c); proj != 5 {
			t.Errorf("dept %q resolved to %d across tenants; want the token's 5", dept, proj)
		}
	}
	// "Finance" exists in both tenants: each key lands in its OWN.
	c := setupWithHeaders(t, trustedToken(), map[string]string{HeaderDept: "Finance"})
	if _, proj := gotAttribution(c); proj != ids["ten-a/Finance"] {
		t.Errorf("ten-a Finance = %d, want %d", proj, ids["ten-a/Finance"])
	}
	tokB := trustedToken()
	tokB.TenantId = "ten-b"
	c = setupWithHeaders(t, tokB, map[string]string{HeaderDept: "Finance"})
	if _, proj := gotAttribution(c); proj != ids["ten-b/Finance"] {
		t.Errorf("ten-b Finance = %d, want %d", proj, ids["ten-b/Finance"])
	}
}

func TestIdentityHeaders_NoHeadersUsesTokenValues(t *testing.T) {
	identityFixture(t)
	c := setupWithHeaders(t, trustedToken(), nil)
	if emp, proj := gotAttribution(c); emp != "tok-emp" || proj != 5 {
		t.Errorf("attribution = %q / %d; want tok-emp / 5", emp, proj)
	}
}

// The playground handler calls SetupContextForToken a second time with a
// stripped temp token. That call must neither erase the employee/project the
// first call resolved nor count the same untrusted request twice.
func TestIdentityHeaders_SecondSetupCallKeepsResolvedAttribution(t *testing.T) {
	ids := identityFixture(t)
	c := setupWithHeaders(t, trustedToken(), map[string]string{HeaderEmployee: "alice", HeaderDept: "R-D"})

	temp := &repo.Token{Id: 1, Key: "k", UserId: 1, ProjectId: ids["ten-a/Research"]}
	if err := SetupContextForToken(c, temp); err != nil {
		t.Fatal(err)
	}
	if emp, proj := gotAttribution(c); emp != "alice" || proj != ids["ten-a/Research"] {
		t.Errorf("after second setup attribution = %q / %d; want alice / %d", emp, proj, ids["ten-a/Research"])
	}

	untrusted := trustedToken()
	untrusted.TrustedIdentityHeaders = false
	before := testutil.ToFloat64(metrics.IdentityHeaderUntrustedTotal)
	c2 := setupWithHeaders(t, untrusted, map[string]string{HeaderEmployee: "mallory"})
	if err := SetupContextForToken(c2, &repo.Token{Id: 1, Key: "k", UserId: 1}); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metrics.IdentityHeaderUntrustedTotal); got != before+1 {
		t.Errorf("untrusted counter = %v, want %v (counted once per request)", got, before+1)
	}
	if emp, _ := gotAttribution(c2); emp != "tok-emp" {
		t.Errorf("employee after second setup = %q, want tok-emp", emp)
	}
}
