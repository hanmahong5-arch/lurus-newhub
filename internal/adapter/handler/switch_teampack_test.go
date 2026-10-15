package handler

// switch_teampack_test.go - TestSwitchTeamPack*: the reseller team-asset
// bundle store. Root admin PUT/DELETE /api/v2/admin/tenants/:id/switch-teampack
// and the three public GETs /api/v2/:tenant_slug/switch/teampack{,.sig,/meta}.
// Routes are mounted with the same path shapes as api-v2-router.go on a fresh
// engine (root actor stubbed the way tenant_model_allowlist_test.go does).

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-gonic/gin"
)

const teamPackTestSig = "ab01cd23ef45ab01cd23ef45ab01cd23ef45ab01cd23ef45ab01cd23ef45ab01" +
	"cd23ef45ab01cd23ef45ab01cd23ef45ab01cd23ef45ab01cd23ef45ab01cd23"

type teamPackEnv struct {
	ctx    *V2TestContext
	engine *gin.Engine
	slug   string
}

func setupTeamPackEnv(t *testing.T) *teamPackEnv {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	t.Cleanup(ctx.Cleanup)
	if err := repo.DB.AutoMigrate(&entity.SwitchTeamPack{}); err != nil {
		t.Fatalf("migrate switch_teampacks: %v", err)
	}

	var tenant repo.Tenant
	if err := repo.DB.Where("id = ?", ctx.TenantID).First(&tenant).Error; err != nil {
		t.Fatalf("load seeded tenant: %v", err)
	}

	r := gin.New()
	apiV2 := r.Group("/api/v2")
	apiV2.GET("/:tenant_slug/switch/teampack/meta", GetSwitchTeamPackMeta)
	apiV2.GET("/:tenant_slug/switch/teampack", GetSwitchTeamPackBlob)
	apiV2.GET("/:tenant_slug/switch/teampack.sig", GetSwitchTeamPackSig)
	admin := apiV2.Group("/admin/tenants")
	admin.Use(func(c *gin.Context) {
		c.Set("id", ctx.RootUser.Id)
		c.Next()
	})
	admin.PUT("/:id/switch-teampack", PutSwitchTeamPack)
	admin.DELETE("/:id/switch-teampack", DeleteSwitchTeamPack)

	return &teamPackEnv{ctx: ctx, engine: r, slug: tenant.Slug}
}

func (e *teamPackEnv) do(method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

func (e *teamPackEnv) adminPath(ref string) string {
	return "/api/v2/admin/tenants/" + ref + "/switch-teampack"
}

func (e *teamPackEnv) pubPath(slug, suffix string) string {
	return "/api/v2/" + slug + "/switch/teampack" + suffix
}

// gzBlob returns n bytes that start with the gzip magic (content is not a
// valid stream; the Hub only checks the magic).
func teamPackGzBlob(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	b[0], b[1] = 0x1f, 0x8b
	return b
}

func teamPackBody(version string, blob []byte, sig string) []byte {
	out, _ := json.Marshal(map[string]string{
		"version":     version,
		"blob_base64": base64.StdEncoding.EncodeToString(blob),
		"sig_hex":     sig,
	})
	return out
}

func teamPackJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %v, body: %.200s", err, w.Body.String())
	}
	return m
}

func (e *teamPackEnv) publish(t *testing.T, blob []byte) map[string]interface{} {
	t.Helper()
	w := e.do(http.MethodPut, e.adminPath(e.ctx.TenantID), teamPackBody("1.2.3", blob, teamPackTestSig), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("publish: status %d body %s", w.Code, w.Body.String())
	}
	return teamPackJSON(t, w)
}

func TestSwitchTeamPack_PutThenPublicGetsAreByteConsistent(t *testing.T) {
	e := setupTeamPackEnv(t)
	capture := captureChannelAudit(t)
	blob := teamPackGzBlob(4096)
	sum := sha256.Sum256(blob)
	wantSHA := hex.EncodeToString(sum[:])

	// version is trimmed before validation and storage.
	w := e.do(http.MethodPut, e.adminPath(e.ctx.TenantID),
		teamPackBody("  1.2.3+rc-1_x  ", blob, strings.ToUpper(teamPackTestSig)), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body %s", w.Code, w.Body.String())
	}
	put := teamPackJSON(t, w)
	if put["success"] != true {
		t.Fatalf("PUT success = %v", put["success"])
	}
	data := put["data"].(map[string]interface{})
	if data["version"] != "1.2.3+rc-1_x" || data["sha256"] != wantSHA ||
		data["size"] != float64(len(blob)) || data["updated_at"].(float64) <= 0 {
		t.Fatalf("PUT data wrong: %v", data)
	}

	// meta mirrors the PUT data.
	w = e.do(http.MethodGet, e.pubPath(e.slug, "/meta"), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("meta status = %d body %s", w.Code, w.Body.String())
	}
	meta := teamPackJSON(t, w)
	if meta["success"] != true {
		t.Fatalf("meta success = %v", meta["success"])
	}
	md := meta["data"].(map[string]interface{})
	for _, k := range []string{"version", "sha256", "size", "updated_at"} {
		if md[k] != data[k] {
			t.Errorf("meta[%s] = %v, PUT data = %v", k, md[k], data[k])
		}
	}
	if len(md) != 4 {
		t.Errorf("meta data has %d keys, want exactly 4: %v", len(md), md)
	}

	// blob: exact bytes, gzip content type, strong ETag, Content-Length.
	w = e.do(http.MethodGet, e.pubPath(e.slug, ""), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("blob status = %d", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), blob) {
		t.Fatal("blob bytes differ from what was uploaded")
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", ct)
	}
	if etag := w.Header().Get("ETag"); etag != `"`+wantSHA+`"` {
		t.Errorf("ETag = %q, want quoted sha256 %q", etag, wantSHA)
	}
	if cl := w.Header().Get("Content-Length"); cl != "4096" {
		t.Errorf("Content-Length = %q, want 4096", cl)
	}

	// sig: lower-case 128 hex, no newline, text/plain.
	w = e.do(http.MethodGet, e.pubPath(e.slug, ".sig"), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("sig status = %d body %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != teamPackTestSig || len(got) != 128 {
		t.Errorf("sig body = %q (len %d), want the lower-cased 128-hex upload", got, len(got))
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("sig Content-Type = %q, want text/plain", ct)
	}

	// the PUT was audited.
	var found bool
	for _, ev := range capture.snapshot() {
		if strings.Contains(ev.Details, "switch-teampack set: "+e.ctx.TenantID) && strings.Contains(ev.Details, wantSHA) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no audit event for the PUT; got %d events", len(capture.snapshot()))
	}
}

func TestSwitchTeamPack_IfNoneMatch304(t *testing.T) {
	e := setupTeamPackEnv(t)
	blob := teamPackGzBlob(512)
	data := e.publish(t, blob)["data"].(map[string]interface{})
	etag := `"` + data["sha256"].(string) + `"`

	for _, inm := range []string{etag, `W/` + etag, `"other", ` + etag, `*`} {
		w := e.do(http.MethodGet, e.pubPath(e.slug, ""), nil, map[string]string{"If-None-Match": inm})
		if w.Code != http.StatusNotModified {
			t.Errorf("If-None-Match %q: status = %d, want 304", inm, w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("If-None-Match %q: 304 carried a body", inm)
		}
		if w.Header().Get("ETag") != etag {
			t.Errorf("If-None-Match %q: 304 ETag = %q", inm, w.Header().Get("ETag"))
		}
	}

	w := e.do(http.MethodGet, e.pubPath(e.slug, ""), nil, map[string]string{"If-None-Match": `"stale"`})
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), blob) {
		t.Errorf("non-matching If-None-Match: status = %d, want full 200", w.Code)
	}
}

func TestSwitchTeamPack_RepublishReplacesRow(t *testing.T) {
	e := setupTeamPackEnv(t)
	e.publish(t, teamPackGzBlob(100))
	second := teamPackGzBlob(200)
	w := e.do(http.MethodPut, e.adminPath(e.ctx.TenantID), teamPackBody("2.0.0", second, teamPackTestSig), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("second PUT status = %d body %s", w.Code, w.Body.String())
	}
	var n int64
	repo.DB.Model(&entity.SwitchTeamPack{}).Where("tenant_id = ?", e.ctx.TenantID).Count(&n)
	if n != 1 {
		t.Fatalf("rows for tenant = %d, want 1 (one pack per tenant)", n)
	}
	w = e.do(http.MethodGet, e.pubPath(e.slug, ""), nil, nil)
	if !bytes.Equal(w.Body.Bytes(), second) {
		t.Fatal("GET did not serve the replacement blob")
	}
}

func TestSwitchTeamPack_SizeLimits(t *testing.T) {
	e := setupTeamPackEnv(t)

	// exactly 8 MiB is accepted.
	w := e.do(http.MethodPut, e.adminPath(e.ctx.TenantID), teamPackBody("1", teamPackGzBlob(8<<20), teamPackTestSig), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("8 MiB exactly: status = %d body %.200s", w.Code, w.Body.String())
	}

	// 8 MiB + 1 byte decoded is refused (the base64 still fits under 12 MiB).
	w = e.do(http.MethodPut, e.adminPath(e.ctx.TenantID), teamPackBody("1", teamPackGzBlob(8<<20+1), teamPackTestSig), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("8 MiB + 1: status = %d, want 400", w.Code)
	}

	// a request body above the 12 MiB read cap is refused too.
	w = e.do(http.MethodPut, e.adminPath(e.ctx.TenantID), teamPackBody("1", teamPackGzBlob(10<<20), teamPackTestSig), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("body > 12 MiB: status = %d, want 400", w.Code)
	}
}

func TestSwitchTeamPack_BadShapes400(t *testing.T) {
	e := setupTeamPackEnv(t)
	good := teamPackGzBlob(64)
	notGzip := []byte("plain text, not a gzip stream")

	cases := []struct {
		name string
		body []byte
	}{
		{"sig too short", teamPackBody("1", good, teamPackTestSig[:127])},
		{"sig too long", teamPackBody("1", good, teamPackTestSig+"0")},
		{"sig non-hex", teamPackBody("1", good, strings.Repeat("zz", 64))},
		{"sig empty", teamPackBody("1", good, "")},
		{"version empty", teamPackBody("", good, teamPackTestSig)},
		{"version blank", teamPackBody("   ", good, teamPackTestSig)},
		{"version too long", teamPackBody(strings.Repeat("a", 65), good, teamPackTestSig)},
		{"version too long runes", teamPackBody(strings.Repeat("版", 65), good, teamPackTestSig)},
		{"version control newline", teamPackBody("1\n2", good, teamPackTestSig)},
		{"version control tab", teamPackBody("1\t2", good, teamPackTestSig)},
		{"version control nul", teamPackBody("1\u00002", good, teamPackTestSig)},
		{"blob not gzip", teamPackBody("1", notGzip, teamPackTestSig)},
		{"blob empty", teamPackBody("1", nil, teamPackTestSig)},
		{"blob one byte", teamPackBody("1", []byte{0x1f}, teamPackTestSig)},
		{"blob not base64", []byte(`{"version":"1","blob_base64":"!!!not-base64!!!","sig_hex":"` + teamPackTestSig + `"}`)},
		{"body not json", []byte(`{nope`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := e.do(http.MethodPut, e.adminPath(e.ctx.TenantID), tc.body, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body %.200s", w.Code, w.Body.String())
			}
			if teamPackJSON(t, w)["success"] != false {
				t.Error("success must be false")
			}
		})
	}

	// none of the rejected PUTs wrote a row.
	w := e.do(http.MethodGet, e.pubPath(e.slug, "/meta"), nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("after only-bad PUTs meta status = %d, want 404 (nothing stored)", w.Code)
	}
}

// The Switch side accepts any control-free label of up to 64 runes as a pack
// version (spaces, CJK, brackets included); the Hub must accept the same set.
// Before the fix the Hub allowed only [A-Za-z0-9._+-] and these were 400.
func TestSwitchTeamPack_VersionMatchesSwitchRule(t *testing.T) {
	e := setupTeamPackEnv(t)
	good := teamPackGzBlob(64)
	for _, v := range []string{"1.0 beta", "v2(内测)", "1/2", "vé", strings.Repeat("版", 64), strings.Repeat("a", 64)} {
		w := e.do(http.MethodPut, e.adminPath(e.ctx.TenantID), teamPackBody(v, good, teamPackTestSig), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("version %q: status = %d, want 200, body %.200s", v, w.Code, w.Body.String())
		}
		w = e.do(http.MethodGet, e.pubPath(e.slug, "/meta"), nil, nil)
		got := teamPackJSON(t, w)["data"].(map[string]interface{})["version"]
		if got != v {
			t.Fatalf("version round-trip = %v, want %q", got, v)
		}
	}
}

func TestSwitchTeamPack_TenantResolution(t *testing.T) {
	e := setupTeamPackEnv(t)
	good := teamPackBody("1", teamPackGzBlob(64), teamPackTestSig)

	// unknown tenant -> 404 on every verb/route.
	if w := e.do(http.MethodPut, e.adminPath("no-such-tenant"), good, nil); w.Code != http.StatusNotFound {
		t.Errorf("PUT unknown tenant: status = %d, want 404", w.Code)
	}
	if w := e.do(http.MethodDelete, e.adminPath("no-such-tenant"), nil, nil); w.Code != http.StatusNotFound {
		t.Errorf("DELETE unknown tenant: status = %d, want 404", w.Code)
	}
	for _, suffix := range []string{"/meta", "", ".sig"} {
		if w := e.do(http.MethodGet, e.pubPath("no-such-tenant", suffix), nil, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %q unknown tenant: status = %d, want 404", suffix, w.Code)
		}
	}

	// "default" is refused with 400.
	if w := e.do(http.MethodPut, e.adminPath("default"), good, nil); w.Code != http.StatusBadRequest {
		t.Errorf("PUT default: status = %d, want 400", w.Code)
	}
	if w := e.do(http.MethodDelete, e.adminPath("default"), nil, nil); w.Code != http.StatusBadRequest {
		t.Errorf("DELETE default: status = %d, want 400", w.Code)
	}

	// the tenant is addressable by id AND by slug; both land on the same row.
	if w := e.do(http.MethodPut, e.adminPath(e.ctx.TenantID), good, nil); w.Code != http.StatusOK {
		t.Fatalf("PUT by id: status = %d body %s", w.Code, w.Body.String())
	}
	bySlug := teamPackBody("by-slug", teamPackGzBlob(80), teamPackTestSig)
	if e.ctx.TenantID == e.slug {
		// The seed uses identical id and slug strings; prove the slug arm on a
		// second tenant whose id differs from its slug.
		other := &repo.Tenant{Id: "tp-tenant-id-x", Name: "TP", Slug: "tp-slug-x", Status: repo.TenantStatusEnabled}
		if err := repo.DB.Create(other).Error; err != nil {
			t.Fatalf("seed second tenant: %v", err)
		}
		if w := e.do(http.MethodPut, e.adminPath("tp-slug-x"), bySlug, nil); w.Code != http.StatusOK {
			t.Fatalf("PUT by slug: status = %d body %s", w.Code, w.Body.String())
		}
		if _, err := repo.GetSwitchTeamPack("tp-tenant-id-x"); err != nil {
			t.Fatalf("slug PUT must key the row on the tenant id: %v", err)
		}
		w := e.do(http.MethodGet, e.pubPath("tp-slug-x", "/meta"), nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("public meta by slug: status = %d", w.Code)
		}
	} else if w := e.do(http.MethodPut, e.adminPath(e.slug), bySlug, nil); w.Code != http.StatusOK {
		t.Fatalf("PUT by slug: status = %d body %s", w.Code, w.Body.String())
	}
}

func TestSwitchTeamPack_UnpublishedIs404(t *testing.T) {
	e := setupTeamPackEnv(t)
	for _, suffix := range []string{"/meta", "", ".sig"} {
		w := e.do(http.MethodGet, e.pubPath(e.slug, suffix), nil, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %q with no pack: status = %d, want 404", suffix, w.Code)
		}
		if suffix == "/meta" && teamPackJSON(t, w)["success"] != false {
			t.Error("404 meta must carry success=false")
		}
	}
}

func TestSwitchTeamPack_DisabledTenantIs404(t *testing.T) {
	e := setupTeamPackEnv(t)
	e.publish(t, teamPackGzBlob(64))
	if err := repo.DB.Model(&repo.Tenant{}).Where("id = ?", e.ctx.TenantID).
		Update("status", repo.TenantStatusSuspended).Error; err != nil {
		t.Fatalf("suspend tenant: %v", err)
	}
	for _, suffix := range []string{"/meta", "", ".sig"} {
		w := e.do(http.MethodGet, e.pubPath(e.slug, suffix), nil, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("GET %q for suspended tenant: status = %d, want 404", suffix, w.Code)
		}
	}
}

func TestSwitchTeamPack_DeleteIsIdempotentAndAudited(t *testing.T) {
	e := setupTeamPackEnv(t)
	capture := captureChannelAudit(t)

	// DELETE with nothing published is still 200.
	w := e.do(http.MethodDelete, e.adminPath(e.ctx.TenantID), nil, nil)
	if w.Code != http.StatusOK || teamPackJSON(t, w)["success"] != true {
		t.Fatalf("DELETE with no pack: status = %d body %s", w.Code, w.Body.String())
	}

	e.publish(t, teamPackGzBlob(64))
	if w := e.do(http.MethodGet, e.pubPath(e.slug, "/meta"), nil, nil); w.Code != http.StatusOK {
		t.Fatalf("meta before delete: status = %d", w.Code)
	}

	for i := 0; i < 2; i++ {
		w = e.do(http.MethodDelete, e.adminPath(e.ctx.TenantID), nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("DELETE #%d: status = %d body %s", i+1, w.Code, w.Body.String())
		}
	}
	for _, suffix := range []string{"/meta", "", ".sig"} {
		if w := e.do(http.MethodGet, e.pubPath(e.slug, suffix), nil, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %q after DELETE: status = %d, want 404", suffix, w.Code)
		}
	}

	var deletes int
	for _, ev := range capture.snapshot() {
		if strings.Contains(ev.Details, "switch-teampack deleted: "+e.ctx.TenantID) {
			deletes++
		}
	}
	if deletes != 3 {
		t.Fatalf("delete audit events = %d, want 3 (every DELETE is recorded)", deletes)
	}
}
