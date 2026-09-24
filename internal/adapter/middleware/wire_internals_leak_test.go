package middleware

// wire_internals_leak_test.go — cycle14 L4 (wire messages). Each test below
// asserts the EXACT bytes a relay client receives for one rejection, because
// every defect in this lane was invisible to a "contains" assertion: the
// doubled prefix, the Go type name and the "(distributor)" tag all sit inside
// a message that already contained the right substring.
//
// The assertions include the " (request id: )" tail that
// common.MessageWithRequestId appends, because that tail is part of the bytes
// the client actually receives. A test here going red because that helper
// changed is the intended behaviour, not a false alarm.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// l4MountDistribute mounts the real Distribute() behind StampRelayFormat, so
// the rejection renders through the production envelope path rather than a
// hand-built one.
func l4MountDistribute(ctxSetup func(c *gin.Context)) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if ctxSetup != nil {
			ctxSetup(c)
		}
		c.Next()
	})
	pass := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	v1 := r.Group("/v1")
	v1.Use(StampRelayFormat())
	v1.POST("/chat/completions", Distribute(), pass)
	return r
}

// l4Post sends a request with an explicit Content-Type (empty string = send
// no Content-Type header at all, which is a distinct case for the body
// parser).
func l4Post(r *gin.Engine, path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// l4WireMessage pulls error.message out of the OpenAI-shaped envelope.
func l4WireMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", w.Body.String(), err)
	}
	return env.Error.Message
}

// Defect (1) — the doubled prefix. getModelFromRequest already prefixes
// "invalid request, " and Distribute prefixes "Invalid request, " again, so
// the operator pulled this off the wire:
//
//	Invalid request, invalid request, unexpected end of JSON input
func TestDistribute_MalformedJSON_PrefixIsNotDoubled(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := l4MountDistribute(nil)
	w := l4Post(r, "/v1/chat/completions", "application/json", "")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	got := l4WireMessage(t, w)
	const want = "Invalid request, unexpected end of JSON input (request id: )"
	if got != want {
		t.Errorf("wire message = %q, want %q", got, want)
	}
}

// Defect (2) — internal Go identifiers on the wire, at the distributor's own
// parse site: ModelRequest is this package's private struct and "of type
// string" is a Go type, neither of which means anything to a customer.
func TestDistribute_WrongFieldType_NamesTheFieldNotTheGoStruct(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := l4MountDistribute(nil)
	w := l4Post(r, "/v1/chat/completions", "application/json", `{"model":5}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	got := l4WireMessage(t, w)
	const want = `Invalid request, invalid value for field "model": expected a string, got number (request id: )`
	if got != want {
		t.Errorf("wire message = %q, want %q", got, want)
	}
	for _, leak := range []string{"ModelRequest", "Go struct field", "json:", "of type"} {
		if strings.Contains(got, leak) {
			t.Errorf("wire message = %q, must not contain the internal token %q", got, leak)
		}
	}
}

// Defect (2), the operator's own bytes. The typed relay request is decoded
// through the same chokepoint, so a negative max_tokens produced:
//
//	json: cannot unmarshal number -5 into Go struct field GeneralOpenAIRequest.max_tokens of type uint
//
// Driven through common.UnmarshalBodyReusable directly because the typed
// request is parsed by the relay handler (not by this package), while the
// decode chokepoint the fix lives in is shared by both.
func TestUnmarshalBodyReusable_NegativeMaxTokens_NamesFieldNotGoType(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions",
		`{"model":"l4-probe-model","max_tokens":-5}`, "application/json")

	err := common.UnmarshalBodyReusable(c, &dto.GeneralOpenAIRequest{})
	if err == nil {
		t.Fatal("want a decode error for max_tokens:-5, got nil")
	}
	const want = `invalid value for field "max_tokens": expected a non-negative integer, got number -5`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	for _, leak := range []string{"GeneralOpenAIRequest", "Go struct field", "uint", "json:"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error = %q, must not contain the internal token %q", err.Error(), leak)
		}
	}
}

// Defect (2), nested field. encoding/json reports a nested field as a dotted
// JSON path, which is the caller's own spelling — but the relay wire runs
// every message through common.MaskSensitiveInfo, whose domain regex eats
// `a.b` shapes (it is why the UNFIXED version of the test above read
// "Go struct field ***.model"). This pins what a caller actually receives for
// a nested field, so a later widening of that regex cannot quietly turn the
// field name back into asterisks.
func TestDistribute_NestedWrongFieldType_NamesTheWholePath(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := l4MountDistribute(nil)
	w := l4Post(r, "/v1/chat/completions", "application/json", `{"model":"l4-probe-model","metadata":{"user_id":5}}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	got := l4WireMessage(t, w)
	const want = `Invalid request, invalid value for field "metadata.user_id": expected a string, got number (request id: )`
	if got != want {
		t.Errorf("wire message = %q, want %q", got, want)
	}
}

// Defect (3) — the internal component name. "(distributor)" tells a customer
// nothing and names an internal package; it belongs in the server log only.
func TestDistribute_NoChannel404_WireHasNoComponentName(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := l4MountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := l4Post(r, "/v1/chat/completions", "application/json", `{"model":"this-model-does-not-exist-xyz"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	got := l4WireMessage(t, w)
	const want = "no available channel for model this-model-does-not-exist-xyz in group default (request id: )"
	if got != want {
		t.Errorf("wire message = %q, want %q", got, want)
	}
}

// Defect (3), the 503 twin: the same message is written from the
// channel-exists-but-disabled branch, so fixing only the 404 site would leave
// the component name on the wire for every genuine outage.
func TestDistribute_NoChannel503_WireHasNoComponentName(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	ch := &repo.Channel{
		Id: 74, Name: "l4-disabled", TenantId: "default", Key: "k",
		Status: common.ChannelStatusManuallyDisabled, Models: "l4-probe-model", Group: "default",
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := ch.AddAbilities(db); err != nil {
		t.Fatalf("add abilities: %v", err)
	}

	r := l4MountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := l4Post(r, "/v1/chat/completions", "application/json", `{"model":"l4-probe-model"}`)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
	}
	got := l4WireMessage(t, w)
	const want = "no available channel for model l4-probe-model in group default (request id: )"
	if got != want {
		t.Errorf("wire message = %q, want %q", got, want)
	}
}

// Defect (4) — a misleading error for the wrong cause. The body parser skips
// any Content-Type that is neither json nor form, so the model never gets
// read and the caller is told the model name is missing. Answer the real
// reason instead.
func TestDistribute_UnsupportedContentType_AnswersTheRealReason(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := l4MountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := l4Post(r, "/v1/chat/completions", "text/plain", `{"model":"l4-probe-model"}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	got := l4WireMessage(t, w)
	const want = `Invalid request, unsupported content type "text/plain"; expected application/json, ` +
		`application/x-www-form-urlencoded or multipart/form-data (request id: )`
	if got != want {
		t.Errorf("wire message = %q, want %q", got, want)
	}
}

// Defect (4), the carve-out that keeps the fix from becoming an outage: a
// request that carries NO Content-Type at all is not an unsupported type, and
// neither is one with an empty body. Both keep the pre-existing behaviour
// (parse skipped, the model-name refusal further down), because every GET on
// a relay route reaches the same parser with no Content-Type and no body.
func TestDistribute_NoContentType_KeepsTheOldModelNameRefusal(t *testing.T) {
	_, cleanup := setupCoverDB(t)
	defer cleanup()

	r := l4MountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := l4Post(r, "/v1/chat/completions", "", `{"model":"l4-probe-model"}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	got := l4WireMessage(t, w)
	const want = "Model name not specified, model name cannot be empty (request id: )"
	if got != want {
		t.Errorf("wire message = %q, want %q", got, want)
	}
}

func TestUnmarshalBodyReusable_UnsupportedContentTypeEmptyBody_IsNotAnError(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", "", "text/plain")

	var probe struct {
		Model string `json:"model"`
	}
	if err := common.UnmarshalBodyReusable(c, &probe); err != nil {
		t.Errorf("empty body with an unsupported content type must stay a no-op, got %v", err)
	}
}
