package systemone

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// stubbed wraps a canned response so the body is closed by the test cleanup.
type stubbed struct{ R *http.Response }

func respWith(t *testing.T, status int, body string) stubbed {
	t.Helper()
	resp := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return stubbed{R: resp}
}

// decodeFixtureBody returns the fixture's JSON body as generic maps.
func decodeFixtureBody(t *testing.T, f fixture) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(f.Response.Body, &m); err != nil {
		t.Fatalf("%s: %v", f.Name, err)
	}
	return m
}

func mustUsage(t *testing.T, got any) *dto.Usage {
	t.Helper()
	u, ok := got.(*dto.Usage)
	if !ok || u == nil {
		t.Fatalf("DoResponse returned %T, want *dto.Usage", got)
	}
	return u
}

// Every real 200 capture from a running laya-serve must normalise to the
// hosted wire shape and bill input tokens only.
func TestDoResponse_LayaGolden(t *testing.T) {
	var n int
	for _, f := range loadFixtures(t, "laya") {
		if f.Response.Status != http.StatusOK {
			continue
		}
		n++
		t.Run(f.Name, func(t *testing.T) {
			ex, info := replay(t, f, constant.ChannelTypeSystemOneCompatible, "")
			upstream := decodeFixtureBody(t, f)
			c, w := newCtx()

			got, apiErr := (&Adaptor{}).DoResponse(c, ex.Resp, info)
			if apiErr != nil {
				t.Fatalf("DoResponse: %v", apiErr)
			}
			usage := mustUsage(t, got)
			upUsage := upstream["usage"].(map[string]any)
			wantIn := int(upUsage["input_tokens"].(float64))
			if usage.PromptTokens != wantIn || usage.CompletionTokens != 0 || usage.TotalTokens != wantIn {
				t.Errorf("usage = %+v, want prompt=total=%d completion=0", usage, wantIn)
			}

			if w.Code != http.StatusOK {
				t.Errorf("status = %d", w.Code)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("content-type = %q", ct)
			}
			var out map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("body is not JSON: %v", err)
			}
			if want := []string{"answers", "model", "usage"}; !reflect.DeepEqual(keysOf(out), want) {
				t.Errorf("top-level keys = %v, want %v (routing must be dropped)", keysOf(out), want)
			}
			// Laya always says laya-rl-agent; the caller sees the name it asked for.
			if string(out["model"]) != `"`+info.OriginModelName+`"` {
				t.Errorf("model = %s, want the requested public name %q", out["model"], info.OriginModelName)
			}
			if strings.Contains(w.Body.String(), "laya-rl-agent") {
				t.Errorf("upstream model name leaked: %s", w.Body.String())
			}

			var gotUsage map[string]any
			if err := json.Unmarshal(out["usage"], &gotUsage); err != nil {
				t.Fatal(err)
			}
			wantUsage := map[string]any{"input_tokens": upUsage["input_tokens"], "output_tokens": upUsage["output_tokens"]}
			if upUsage["truncated"] == true {
				wantUsage["truncated"] = true
			}
			if !reflect.DeepEqual(gotUsage, wantUsage) {
				t.Errorf("usage = %v, want %v (extras dropped, truncated only when true)", gotUsage, wantUsage)
			}

			// Answers are passed through verbatim, extras included.
			var gotAnswers any
			if err := json.Unmarshal(out["answers"], &gotAnswers); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotAnswers, upstream["answers"]) {
				t.Errorf("answers changed in transit:\n got %v\nwant %v", gotAnswers, upstream["answers"])
			}
		})
	}
	if n < 30 {
		t.Fatalf("only %d 200 fixtures replayed; the corpus shrank", n)
	}
}

func TestDoResponse_LayaTruncationSurfaced(t *testing.T) {
	for _, name := range []string{"14a_state_38k_chars_under_cap", "06l_chinese_long_state_beyond_context", "14c_state_string_beyond_context_49k"} {
		f := loadFixture(t, "laya", name)
		ex, info := replay(t, f, constant.ChannelTypeSystemOneCompatible, "")
		c, w := newCtx()
		if _, apiErr := (&Adaptor{}).DoResponse(c, ex.Resp, info); apiErr != nil {
			t.Fatal(apiErr)
		}
		var out dto.SystemOneResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if !out.Usage.Truncated {
			t.Errorf("%s: truncated was not surfaced: %s", name, w.Body.String())
		}
	}
	// The common case stays byte-for-byte the hosted shape: no "truncated" key.
	f := loadFixture(t, "laya", "01_choice")
	ex, info := replay(t, f, constant.ChannelTypeSystemOneCompatible, "")
	c, w := newCtx()
	if _, apiErr := (&Adaptor{}).DoResponse(c, ex.Resp, info); apiErr != nil {
		t.Fatal(apiErr)
	}
	if strings.Contains(w.Body.String(), "truncated") {
		t.Errorf("truncated:false must be omitted: %s", w.Body.String())
	}
}

// Hosted answers keep the versioned model id the upstream reports and the
// upstream's own output_tokens; billing still counts input only.
func TestDoResponse_TypeSafeGolden(t *testing.T) {
	cases := []struct {
		fixture      string
		wantIn, want int
	}{
		{"200_noul", 307, 20},
		{"200_choice", 318, 34},
		{"200_score", 304, 18},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			f := loadFixture(t, "typesafe", tc.fixture)
			ex, info := replay(t, f, constant.ChannelTypeTypeSafe, "up-key")
			if ex.Authorization != "Bearer up-key" {
				t.Errorf("upstream Authorization = %q", ex.Authorization)
			}
			c, w := newCtx()
			got, apiErr := (&Adaptor{}).DoResponse(c, ex.Resp, info)
			if apiErr != nil {
				t.Fatal(apiErr)
			}
			if u := mustUsage(t, got); u.PromptTokens != tc.wantIn || u.CompletionTokens != 0 || u.TotalTokens != tc.wantIn {
				t.Errorf("usage = %+v, want prompt=total=%d completion=0", u, tc.wantIn)
			}
			var out dto.SystemOneResponse
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Model != "jev-1.13.0" {
				t.Errorf("model = %q, want the upstream's versioned id", out.Model)
			}
			if out.Usage.InputTokens != tc.wantIn || out.Usage.OutputTokens != tc.want {
				t.Errorf("usage = %+v", out.Usage)
			}
			upstream := decodeFixtureBody(t, f)
			var gotAnswers any
			if err := json.Unmarshal(w.Body.Bytes(), &struct {
				Answers *any `json:"answers"`
			}{&gotAnswers}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotAnswers, upstream["answers"]) {
				t.Errorf("answers changed: got %v want %v", gotAnswers, upstream["answers"])
			}
		})
	}
}

// The gateway's own x-request-id (set by middleware before the relay runs) must
// survive, and the upstream's ids must not be echoed as ours.
func TestDoResponse_KeepsGatewayRequestID(t *testing.T) {
	f := loadFixture(t, "typesafe", "200_noul")
	ex, info := replay(t, f, constant.ChannelTypeTypeSafe, "k")
	ex.Resp.Header.Set("X-Request-Id", "upstream-id")
	ex.Resp.Header.Set("X-Typesafe-Request-Id", "req_upstream")
	ex.Resp.Header.Set("Cf-Ray", "ray")
	ex.Resp.Header.Set("Server", "cloudflare")
	c, w := newCtx()
	c.Writer.Header().Set("X-Request-Id", "gateway-id")

	if _, apiErr := (&Adaptor{}).DoResponse(c, ex.Resp, info); apiErr != nil {
		t.Fatal(apiErr)
	}
	if got := w.Header().Get("X-Request-Id"); got != "gateway-id" {
		t.Errorf("X-Request-Id = %q, want the gateway's own", got)
	}
	for _, h := range []string{"X-Typesafe-Request-Id", "Cf-Ray", "Server"} {
		if v := w.Header().Get(h); v != "" {
			t.Errorf("%s = %q leaked from upstream", h, v)
		}
	}
}

// A 200 without usage must never bill zero silently: fall back to the
// pre-consume estimate.
func TestDoResponse_MissingUsageBillsTheEstimate(t *testing.T) {
	f := loadFixture(t, "typesafe", "200_no_usage")
	ex, info := replay(t, f, constant.ChannelTypeTypeSafe, "k")
	info.SetEstimatePromptTokens(123)
	c, w := newCtx()

	got, apiErr := (&Adaptor{}).DoResponse(c, ex.Resp, info)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if u := mustUsage(t, got); u.PromptTokens != 123 || u.CompletionTokens != 0 || u.TotalTokens != 123 {
		t.Errorf("usage = %+v, want the 123-token estimate", u)
	}
	// The caller's body still carries a usage object, as the SDK requires it.
	var out dto.SystemOneResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Usage.InputTokens != 123 {
		t.Errorf("usage.input_tokens = %d", out.Usage.InputTokens)
	}
}

func TestDoResponse_RejectsMalformedSuccess(t *testing.T) {
	big := `{"model":"m","answers":{"q":{"type":"noul","noul":0.1,"pad":"` + strings.Repeat("x", maxResponseBytes) + `"}},"usage":{"input_tokens":1,"output_tokens":0}}`
	cases := []struct{ name, body, wantMsg string }{
		{"no answers", `{"model":"m","usage":{"input_tokens":3,"output_tokens":0}}`, "answers"},
		{"null answers", `{"model":"m","answers":null,"usage":{"input_tokens":3,"output_tokens":0}}`, "answers"},
		{"answers is an array", `{"model":"m","answers":[],"usage":{"input_tokens":3,"output_tokens":0}}`, "answers"},
		{"not json", `<html>oops</html>`, "not valid JSON"},
		{"empty body", ``, "not valid JSON"},
		{"over the size cap", big, "exceeds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := infoFor(constant.ChannelTypeTypeSafe, "", "k")
			c, w := newCtx()
			got, apiErr := (&Adaptor{}).DoResponse(c, respWith(t, http.StatusOK, tc.body).R, info)
			if apiErr == nil {
				t.Fatalf("expected an error, got usage %v and body %s", got, w.Body.String())
			}
			if !strings.Contains(apiErr.Error(), tc.wantMsg) {
				t.Errorf("error %q lacks %q", apiErr.Error(), tc.wantMsg)
			}
			// An upstream that answers 200 with garbage is an upstream failure:
			// retried on another channel, counted against the breaker.
			if apiErr.StatusCode != http.StatusBadGateway || !types.IsUpstreamFailure(apiErr) || types.IsSkipRetryError(apiErr) {
				t.Errorf("status=%d upstreamFailure=%v skipRetry=%v", apiErr.StatusCode, types.IsUpstreamFailure(apiErr), types.IsSkipRetryError(apiErr))
			}
			if w.Body.Len() != 0 {
				t.Errorf("a failed parse must not write a response; wrote %s", w.Body.String())
			}
			if strings.Contains(apiErr.Error(), "<html>") {
				t.Errorf("upstream markup echoed: %q", apiErr.Error())
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestDoResponse_BodyReadFailure(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(errReader{})}
	c, _ := newCtx()
	_, apiErr := (&Adaptor{}).DoResponse(c, resp, infoFor(constant.ChannelTypeTypeSafe, "", "k"))
	if apiErr == nil || apiErr.GetErrorCode() != types.ErrorCodeReadResponseBodyFailed || !types.IsUpstreamFailure(apiErr) {
		t.Fatalf("got %+v", apiErr)
	}
}

func TestDoResponse_EmptyAnswersIsAValidAnswer(t *testing.T) {
	// laya-serve answers a request with no questions with 200 and zero usage.
	f := loadFixture(t, "laya", "10j_empty_questions")
	ex, info := replay(t, f, constant.ChannelTypeSystemOneCompatible, "")
	c, w := newCtx()
	got, apiErr := (&Adaptor{}).DoResponse(c, ex.Resp, info)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if u := mustUsage(t, got); u.PromptTokens != 0 {
		t.Errorf("usage = %+v", u)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"answers":{}`)) {
		t.Errorf("body = %s", w.Body.String())
	}
}

var _ = relaycommon.RelayInfo{}
