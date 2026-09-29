package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Header precedence for InternalApiAuth: Authorization: Bearer first,
// X-API-Key (deprecated) second. See extractInternalApiKey for the reasons.

const wantLegacyWarning = `299 - "X-API-Key is deprecated; use Authorization: Bearer"`

type internalAuthFixture struct {
	router *gin.Engine
	rawKey string
	keyID  int
}

func newInternalAuthFixture(t *testing.T) internalAuthFixture {
	t.Helper()
	cleanup := setupMiddlewareTestDB(t)
	t.Cleanup(cleanup)
	// ValidateInternalApiKey bumps last_used_at on a goroutine. With the
	// default pool that write can hold the only connection that has the
	// schema, and the next request's lookup opens a fresh, empty :memory:
	// database ("no such table" → 401). One connection keeps every request on
	// the same database.
	sqlDB, err := repo.DB.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	rawKey, apiKey, err := repo.CreateInternalApiKey(
		"bearer-test-key", []string{"user:read"}, 1, 0, "bearer precedence test key",
	)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	r := gin.New()
	r.Use(InternalApiAuth())
	r.GET("/internal/probe/:id", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	return internalAuthFixture{router: r, rawKey: rawKey, keyID: apiKey.Id}
}

func (f internalAuthFixture) do(t *testing.T, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/internal/probe/42", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func (f internalAuthFixture) legacyCount() float64 {
	return testutil.ToFloat64(metrics.InternalAuthLegacyHeaderTotal.WithLabelValues(
		"/internal/probe/:id", strconv.Itoa(f.keyID)))
}

func assertStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, want, w.Body.String())
	}
}

func TestInternalApiAuth_Bearer_OK_NoWarning(t *testing.T) {
	f := newInternalAuthFixture(t)
	before := f.legacyCount()

	w := f.do(t, map[string]string{"Authorization": "Bearer " + f.rawKey})
	assertStatus(t, w, http.StatusOK)
	if got := w.Header().Get("Warning"); got != "" {
		t.Errorf("Bearer auth must not carry a deprecation Warning, got %q", got)
	}
	if d := f.legacyCount() - before; d != 0 {
		t.Errorf("legacy-header counter moved by %v on a Bearer request", d)
	}
}

func TestInternalApiAuth_BearerSchemeCaseInsensitive(t *testing.T) {
	f := newInternalAuthFixture(t)
	assertStatus(t, f.do(t, map[string]string{"Authorization": "bearer " + f.rawKey}), http.StatusOK)
}

func TestInternalApiAuth_LegacyHeader_OK_WithWarningAndCounter(t *testing.T) {
	f := newInternalAuthFixture(t)
	before := f.legacyCount()

	w := f.do(t, map[string]string{"X-API-Key": f.rawKey})
	assertStatus(t, w, http.StatusOK)
	if got := w.Header().Get("Warning"); got != wantLegacyWarning {
		t.Errorf("Warning = %q, want %q", got, wantLegacyWarning)
	}
	if d := f.legacyCount() - before; d != 1 {
		t.Errorf("legacy-header counter delta = %v, want 1 (route template label, not raw path)", d)
	}
}

func TestInternalApiAuth_NoCredential_401(t *testing.T) {
	f := newInternalAuthFixture(t)
	w := f.do(t, nil)
	assertStatus(t, w, http.StatusUnauthorized)
	if msg := parseResponseBody(t, w)["message"]; msg != "API key required" {
		t.Errorf("message = %v, want API key required", msg)
	}
}

func TestInternalApiAuth_WrongKey_401_EitherHeader(t *testing.T) {
	f := newInternalAuthFixture(t)
	before := f.legacyCount()
	const wrong = "lurus_ik_totally_invalid_key_00000000"

	for name, h := range map[string]map[string]string{
		"bearer":    {"Authorization": "Bearer " + wrong},
		"x-api-key": {"X-API-Key": wrong},
	} {
		w := f.do(t, h)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, w.Code)
			continue
		}
		if msg := parseResponseBody(t, w)["message"]; msg != "Invalid or expired API key" {
			t.Errorf("%s: message = %v", name, msg)
		}
		if got := w.Header().Get("Warning"); got != "" {
			t.Errorf("%s: a rejected request must not carry the deprecation Warning, got %q", name, got)
		}
	}
	if d := f.legacyCount() - before; d != 0 {
		t.Errorf("legacy-header counter moved by %v on rejected requests", d)
	}
}

// A wrong Bearer is final: the valid X-API-Key on the same request is not
// consulted. Falling through would hide a drifted bearer key behind the
// legacy header and give a guesser two credentials per request.
func TestInternalApiAuth_WrongBearer_ValidLegacyHeader_401(t *testing.T) {
	f := newInternalAuthFixture(t)
	w := f.do(t, map[string]string{
		"Authorization": "Bearer lurus_ik_totally_invalid_key_00000000",
		"X-API-Key":     f.rawKey,
	})
	assertStatus(t, w, http.StatusUnauthorized)
}

// An empty Bearer credential is still a Bearer claim: 401, no fallback.
func TestInternalApiAuth_EmptyBearer_ValidLegacyHeader_401(t *testing.T) {
	f := newInternalAuthFixture(t)
	w := f.do(t, map[string]string{"Authorization": "Bearer   ", "X-API-Key": f.rawKey})
	assertStatus(t, w, http.StatusUnauthorized)
}

// Both headers present and different: the Bearer one decides, and the
// response carries no deprecation Warning because X-API-Key was not used.
func TestInternalApiAuth_ValidBearer_WrongLegacyHeader_200(t *testing.T) {
	f := newInternalAuthFixture(t)
	w := f.do(t, map[string]string{
		"Authorization": "Bearer " + f.rawKey,
		"X-API-Key":     "lurus_ik_totally_invalid_key_00000000",
	})
	assertStatus(t, w, http.StatusOK)
	if got := w.Header().Get("Warning"); got != "" {
		t.Errorf("Warning = %q, want none (Bearer decided)", got)
	}
}

// An Authorization header that is not a Bearer credential is ignored and the
// request falls back to X-API-Key; without X-API-Key it is 401.
func TestInternalApiAuth_NonBearerAuthorization_FallsBackToLegacyHeader(t *testing.T) {
	f := newInternalAuthFixture(t)
	for _, auth := range []string{"Basic dXNlcjpwYXNz", f.rawKey, "Bearer" + f.rawKey} {
		w := f.do(t, map[string]string{"Authorization": auth, "X-API-Key": f.rawKey})
		if w.Code != http.StatusOK {
			t.Errorf("Authorization %q + valid X-API-Key: status = %d, want 200", auth, w.Code)
		}
		if got := w.Header().Get("Warning"); got != wantLegacyWarning {
			t.Errorf("Authorization %q: Warning = %q, want the legacy warning", auth, got)
		}

		w = f.do(t, map[string]string{"Authorization": auth})
		if w.Code != http.StatusUnauthorized {
			t.Errorf("Authorization %q alone: status = %d, want 401", auth, w.Code)
		}
	}
}
