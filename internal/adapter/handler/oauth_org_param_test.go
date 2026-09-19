package handler

// oauth_org_param_test.go — L9 (cycle 12): the organization hint the gateway
// sends to the IdP, and the org id an admin may store on a tenant, must both
// refuse the seeded PLACEHOLDER values.
//
// Two rows shipped by the SQL baseline carry a literal stand-in in
// tenants.zitadel_org_id instead of a real IdP organization id:
// migrations/021_pg_baseline_gaps.sql:172 seeds the "default" tenant with
// 'ZITADEL_DEFAULT_ORG_ID_PLACEHOLDER' and
// migrations/030_seed_switch_tenant_and_credit_pool.sql:71 seeds the "switch"
// tenant with 'SWITCH_ORG_ID_PLACEHOLDER'. buildOIDCAuthURL used to forward
// whichever value it was handed as ?organization=, so a login against either
// tenant asked the IdP to scope the authorization to an organization that
// cannot exist — a provider that validates the hint answers an error page, one
// that ignores it silently drops the scoping the parameter was added for.

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// captureOrgParamSysLog redirects common.SysLog's text-mode output to a buffer
// for the duration of the test.
//
// SysLog writes the legacy "[SYS] … | <message>" line to gin.DefaultWriter in
// text mode and only the structured record in JSON mode (common/sys_log.go),
// so the format is forced to text here rather than assumed: nothing else in
// this package touches the logger, but "whatever an earlier test left behind"
// is not an oracle. The structured sinks go to io.Discard so the assertion
// reads exactly one source.
func captureOrgParamSysLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	common.InitSlog(&common.SlogConfig{JSONFormat: false, Writer: io.Discard, ErrWriter: io.Discard})
	prevOut, prevErr := gin.DefaultWriter, gin.DefaultErrorWriter
	buf := &bytes.Buffer{}
	gin.DefaultWriter = buf
	t.Cleanup(func() { gin.DefaultWriter, gin.DefaultErrorWriter = prevOut, prevErr })
	return buf
}

// orgParamOf parses the URL buildOIDCAuthURL produced and returns the
// organization query parameter plus whether the key is present at all
// (an empty-but-present key is a different wire shape from an absent one).
func orgParamOf(t *testing.T, raw string) (string, bool) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse auth URL %q: %v", raw, err)
	}
	q := u.Query()
	_, present := q["organization"]
	return q.Get("organization"), present
}

// TestBuildOIDCAuthURL_PlaceholderOrgID_OmitsOrganizationParam covers both
// placeholder spellings the baseline actually seeds. A tenant whose org id is
// a placeholder is not bound to any IdP organization, so the request must
// carry no organization hint rather than a made-up one.
func TestBuildOIDCAuthURL_PlaceholderOrgID_OmitsOrganizationParam(t *testing.T) {
	for _, orgID := range []string{
		"ZITADEL_DEFAULT_ORG_ID_PLACEHOLDER", // migrations/021_pg_baseline_gaps.sql:172
		"SWITCH_ORG_ID_PLACEHOLDER",          // migrations/030_seed_switch_tenant_and_credit_pool.sql:71
	} {
		raw := buildOIDCAuthURL(orgID, "state-1", "nonce-1", nil, "login")
		got, present := orgParamOf(t, raw)
		if present {
			t.Errorf("buildOIDCAuthURL(%q) sent organization=%q; a placeholder org id must not be forwarded to the IdP (url=%s)",
				orgID, got, raw)
		}
	}
}

// TestBuildOIDCAuthURL_RealOrgID_SendsOrganizationParam is the other half of
// the guard: a tenant genuinely bound to an IdP organization keeps getting the
// hint. Without this case the placeholder check above would still pass if the
// parameter were dropped unconditionally.
func TestBuildOIDCAuthURL_RealOrgID_SendsOrganizationParam(t *testing.T) {
	raw := buildOIDCAuthURL("org-123", "state-1", "nonce-1", nil, "login")
	got, present := orgParamOf(t, raw)
	if !present || got != "org-123" {
		t.Errorf("buildOIDCAuthURL(\"org-123\") organization=%q present=%v, want present with org-123 (url=%s)",
			got, present, raw)
	}
}

// TestBuildOIDCAuthURL_PlaceholderOrgID_LogsOncePerProcess pins the other half
// of the placeholder branch: dropping the organization hint silently would
// leave an operator with no way to learn that a tenant is still unbound, but
// logging it on every login would put a line in the log for every single
// sign-on of the two seeded tenants. The LoadOrStore throttle is what makes
// the message both visible and affordable, and nothing else asserts it.
func TestBuildOIDCAuthURL_PlaceholderOrgID_LogsOncePerProcess(t *testing.T) {
	// A value unique to this test: placeholderOrgLogged is a package-level
	// sync.Map that outlives any one test in the binary.
	const orgID = "LOGONCE_PROBE_ORG_ID_PLACEHOLDER"
	out := captureOrgParamSysLog(t)

	for i := 0; i < 3; i++ {
		buildOIDCAuthURL(orgID, "state-1", "nonce-1", nil, "login")
	}

	if got := strings.Count(out.String(), orgID); got != 1 {
		t.Errorf("system log mentioned %s %d times over 3 authorize URLs, want exactly 1 — the placeholder must be reported once per process, not never and not per login; log=%q",
			orgID, got, out.String())
	}
}

// TestBuildOIDCAuthURL_RealOrgID_LogsNothing is the negative control for the
// throttle above: a bound tenant must not produce that line at all, otherwise
// "exactly one" would be satisfied by a message that fires for everyone.
func TestBuildOIDCAuthURL_RealOrgID_LogsNothing(t *testing.T) {
	const orgID = "org_logonce_real"
	out := captureOrgParamSysLog(t)

	buildOIDCAuthURL(orgID, "state-1", "nonce-1", nil, "login")

	if got := strings.Count(out.String(), orgID); got != 0 {
		t.Errorf("system log mentioned the real org id %s %d times, want 0; log=%q", orgID, got, out.String())
	}
}

// TestBuildOIDCAuthURL_PlaceholderSubstring_StillSendsOrganizationParam pins
// the match as a SUFFIX check, not a substring one: an organization whose real
// id merely contains the word must not lose its hint.
func TestBuildOIDCAuthURL_PlaceholderSubstring_StillSendsOrganizationParam(t *testing.T) {
	const orgID = "org_PLACEHOLDER_holdings_ltd"
	raw := buildOIDCAuthURL(orgID, "state-1", "nonce-1", nil, "login")
	got, present := orgParamOf(t, raw)
	if !present || got != orgID {
		t.Errorf("buildOIDCAuthURL(%q) organization=%q present=%v, want present and unchanged (url=%s)",
			orgID, got, present, raw)
	}
}

// TestCreateTenantV2_PlaceholderOrgID_Rejected stops the placeholder from
// entering the table through the admin API in the first place. A tenant row
// created with a placeholder org id is indistinguishable from the seeded ones
// and silently opts out of every org-bound check in this lane.
func TestCreateTenantV2_PlaceholderOrgID_Rejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ctx.Router.POST("/api/v2/admin/tenants", CreateTenant)

	body := map[string]interface{}{
		"zitadel_org_id": "ACME_ORG_ID_PLACEHOLDER",
		"slug":           "acme-placeholder",
		"name":           "Acme Placeholder",
	}
	w := V2Request(ctx.Router, http.MethodPost, "/api/v2/admin/tenants", body, nil)
	AssertV2Error(t, w, http.StatusBadRequest)

	var stored repo.Tenant
	err := ctx.DB.Where("zitadel_org_id = ?", "ACME_ORG_ID_PLACEHOLDER").First(&stored).Error
	if err == nil {
		t.Errorf("tenant %q was persisted despite the 400", stored.Id)
	}
}

// TestCreateTenantV2_RealOrgID_Accepted is the negative control for the check
// above: the rejection must be about the placeholder suffix, not about tenant
// creation in general.
func TestCreateTenantV2_RealOrgID_Accepted(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ctx.Router.POST("/api/v2/admin/tenants", CreateTenant)

	body := map[string]interface{}{
		"zitadel_org_id": "org_acme_real",
		"slug":           "acme-real",
		"name":           "Acme Real",
	}
	w := V2Request(ctx.Router, http.MethodPost, "/api/v2/admin/tenants", body, nil)
	AssertV2Status(t, w, http.StatusCreated)
}
