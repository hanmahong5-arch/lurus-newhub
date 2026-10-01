package systemone

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// verdict is what the relay layer acts on: status shown to the caller, whether
// the channel is failed over / auto-disabled (channel: code), whether the
// request is retried elsewhere, and whether the breaker is charged.
type verdict struct {
	status          int
	channelFault    bool // channel: code => retried on another channel and auto-disabled
	skipRetry       bool // caller's fault => never retried, never disables the channel
	upstreamFailure bool // charged to the circuit breaker
}

func verdictOf(e *types.NewAPIError) verdict {
	return verdict{
		status:          e.StatusCode,
		channelFault:    types.IsChannelError(e),
		skipRetry:       types.IsSkipRetryError(e),
		upstreamFailure: types.IsUpstreamFailure(e),
	}
}

var (
	callerFault = func(status int) verdict { return verdict{status: status, skipRetry: true} }
	credFault   = verdict{status: http.StatusBadGateway, channelFault: true, upstreamFailure: true}
	overloaded  = verdict{status: http.StatusServiceUnavailable, upstreamFailure: true}
	rateLimited = verdict{status: http.StatusTooManyRequests}
	timedOut    = verdict{status: http.StatusGatewayTimeout, upstreamFailure: true}
)

// wantVerdict is the contract section 9 table, expressed per upstream status.
func wantVerdict(status int) verdict {
	switch status {
	case 400, 413, 422:
		return callerFault(status)
	case 401, 403, 404, 405:
		return credFault
	case 429:
		return rateLimited
	case 503, 529:
		return overloaded
	case 408, 504, 524:
		return timedOut
	default:
		return verdict{status: http.StatusBadGateway, upstreamFailure: true}
	}
}

func TestSystemOneError_GoldenClassification(t *testing.T) {
	cases := []struct {
		dir         string
		channelType int
	}{
		{"laya", constant.ChannelTypeSystemOneCompatible},
		{"typesafe", constant.ChannelTypeTypeSafe},
	}
	var n int
	for _, tc := range cases {
		for _, f := range loadFixtures(t, tc.dir) {
			if f.Response.Status == http.StatusOK {
				continue
			}
			n++
			t.Run(tc.dir+"/"+f.Name, func(t *testing.T) {
				ex, info := replay(t, f, tc.channelType, "secret-key-123")
				c, _ := newCtx()
				apiErr := (&Adaptor{}).SystemOneError(c, info, ex.Resp)
				if apiErr == nil {
					t.Fatal("SystemOneError returned nil for a non-200")
				}
				if got, want := verdictOf(apiErr), wantVerdict(f.Response.Status); got != want {
					t.Errorf("verdict = %+v, want %+v (upstream status %d)", got, want, f.Response.Status)
				}
				msg := apiErr.Error()
				if strings.Contains(msg, "secret-key-123") || strings.Contains(msg, "sk-test-not-a-real-key") {
					t.Errorf("credential material in the caller-facing message: %q", msg)
				}
				if strings.Contains(msg, "<html") {
					t.Errorf("upstream markup echoed to the caller: %q", msg)
				}
				if wantVerdict(f.Response.Status).skipRetry && msg == "" {
					t.Errorf("caller-fault message is empty")
				}
			})
		}
	}
	if n < 30 {
		t.Fatalf("only %d non-200 fixtures replayed; the corpus shrank", n)
	}
}

// The credential faults are the channel's problem, never the caller's: the
// caller must not see 401/403 (they would assume their own key is wrong), and
// the upstream's wording about "your API key" must not be echoed.
func TestSystemOneError_CredentialFaultIsHiddenFromCaller(t *testing.T) {
	for _, tc := range []struct{ dir, name string }{
		{"typesafe", "401_bad_key"}, {"typesafe", "403_no_key"}, {"laya", "09b_wrong_key"},
	} {
		f := loadFixture(t, tc.dir, tc.name)
		ct := constant.ChannelTypeTypeSafe
		if tc.dir == "laya" {
			ct = constant.ChannelTypeSystemOneCompatible
		}
		ex, info := replay(t, f, ct, "k")
		c, _ := newCtx()
		apiErr := (&Adaptor{}).SystemOneError(c, info, ex.Resp)
		if apiErr.StatusCode != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502", tc.name, apiErr.StatusCode)
		}
		if apiErr.GetErrorCode() != types.ErrorCodeChannelInvalidKey {
			t.Errorf("%s: code = %q, want %q", tc.name, apiErr.GetErrorCode(), types.ErrorCodeChannelInvalidKey)
		}
		if strings.Contains(strings.ToLower(apiErr.Error()), "your api key") {
			t.Errorf("%s: upstream wording leaked: %q", tc.name, apiErr.Error())
		}
		if !strings.Contains(apiErr.Error(), "status") {
			t.Errorf("%s: operators need the upstream status in the message: %q", tc.name, apiErr.Error())
		}
	}
}

func TestSystemOneError_RetryAfterIsCarriedInSeconds(t *testing.T) {
	cases := []struct {
		dir, name string
		ct        int
		want      string
	}{
		{"typesafe", "429_rate_limited", constant.ChannelTypeTypeSafe, "2"}, // retry-after-ms 1500 rounds up
		{"typesafe", "529_overloaded", constant.ChannelTypeTypeSafe, "7"},
		{"laya", "16_busy_503", constant.ChannelTypeSystemOneCompatible, "1"},
		{"typesafe", "422_validation", constant.ChannelTypeTypeSafe, ""},
		{"typesafe", "401_bad_key", constant.ChannelTypeTypeSafe, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex, info := replay(t, loadFixture(t, tc.dir, tc.name), tc.ct, "k")
			c, _ := newCtx()
			apiErr := (&Adaptor{}).SystemOneError(c, info, ex.Resp)
			if got := apiErr.UpstreamHeader.Get("Retry-After"); got != tc.want {
				t.Errorf("Retry-After = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSystemOneError_RetryAfterParsing(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"ms wins over seconds", map[string]string{"retry-after-ms": "250", "retry-after": "9"}, "1"},
		{"exact seconds", map[string]string{"retry-after": "30"}, "30"},
		{"zero ms is no hint", map[string]string{"retry-after-ms": "0"}, ""},
		{"garbage ignored", map[string]string{"retry-after-ms": "soon", "retry-after": "later"}, ""},
		{"negative ignored", map[string]string{"retry-after": "-5"}, ""},
		{"absurd value is capped", map[string]string{"retry-after": "99999999"}, "3600"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := respWith(t, http.StatusTooManyRequests, `{"detail":"slow down"}`).R
			for k, v := range tc.headers {
				resp.Header.Set(k, v)
			}
			c, _ := newCtx()
			apiErr := (&Adaptor{}).SystemOneError(c, infoFor(constant.ChannelTypeTypeSafe, "", "k"), resp)
			if got := apiErr.UpstreamHeader.Get("Retry-After"); got != tc.want {
				t.Errorf("Retry-After = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSystemOneError_CallerMessagesCarryTheUpstreamDetail(t *testing.T) {
	cases := []struct {
		dir, name string
		ct        int
		want      []string
	}{
		{"laya", "10c_question_unknown_type", constant.ChannelTypeSystemOneCompatible, []string{"question 'q'", "unknown type 'bogus'"}},
		{"laya", "11d_65_questions", constant.ChannelTypeSystemOneCompatible, []string{"too many questions (65 > 64)"}},
		{"laya", "10i_invalid_json", constant.ChannelTypeSystemOneCompatible, []string{"valid JSON"}},
		// FastAPI's pydantic array is flattened to "location: message".
		{"typesafe", "422_validation", constant.ChannelTypeTypeSafe, []string{"questions.is_urgent.criteria", "Field required"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex, info := replay(t, loadFixture(t, tc.dir, tc.name), tc.ct, "k")
			c, _ := newCtx()
			msg := (&Adaptor{}).SystemOneError(c, info, ex.Resp).Error()
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("message %q lacks %q", msg, w)
				}
			}
		})
	}
}

func TestSystemOneError_DetailShapes(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"string detail", `{"detail":"bad thing"}`, "bad thing"},
		{"object detail", `{"detail":{"error_type":"invalid_request_error","message":"bad object"}}`, "bad object"},
		{"pydantic array", `{"detail":[{"loc":["body","state"],"msg":"Field required","type":"missing"}]}`, "body.state: Field required"},
		{"two pydantic entries", `{"detail":[{"loc":["a"],"msg":"m1"},{"loc":["b"],"msg":"m2"}]}`, "a: m1; b: m2"},
		{"non-json body", `plain text`, "status 422"},
		{"empty body", ``, "status 422"},
		{"detail of wrong type", `{"detail":42}`, "status 422"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newCtx()
			apiErr := (&Adaptor{}).SystemOneError(c, infoFor(constant.ChannelTypeTypeSafe, "", "k"), respWith(t, http.StatusUnprocessableEntity, tc.body).R)
			if !strings.Contains(apiErr.Error(), tc.want) {
				t.Errorf("message %q lacks %q", apiErr.Error(), tc.want)
			}
		})
	}
}

func TestSystemOneError_MessageIsBounded(t *testing.T) {
	huge := `{"detail":"` + strings.Repeat("a", 1<<20) + `"}`
	c, _ := newCtx()
	apiErr := (&Adaptor{}).SystemOneError(c, infoFor(constant.ChannelTypeTypeSafe, "", "k"), respWith(t, http.StatusUnprocessableEntity, huge).R)
	if l := len(apiErr.Error()); l > 1024 {
		t.Errorf("caller-facing message is %d bytes; an upstream must not be able to inflate our error body", l)
	}
	if apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d", apiErr.StatusCode)
	}
}

// Server-side bodies (5xx, edge pages, auth) are never shown to the caller.
func TestSystemOneError_ServerSideBodiesAreNotEchoed(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500, 502, 503, 504, 524, 529} {
		c, _ := newCtx()
		resp := respWith(t, status, `{"detail":"internal path /srv/laya/secret.py token=abc123"}`).R
		apiErr := (&Adaptor{}).SystemOneError(c, infoFor(constant.ChannelTypeSystemOneCompatible, "http://x", ""), resp)
		if strings.Contains(apiErr.Error(), "abc123") || strings.Contains(apiErr.Error(), "/srv/") {
			t.Errorf("status %d echoed the upstream body: %q", status, apiErr.Error())
		}
	}
}

func TestSystemOneError_UnexpectedStatuses(t *testing.T) {
	for _, status := range []int{200, 204, 301, 402, 409, 418, 500, 502} {
		c, _ := newCtx()
		apiErr := (&Adaptor{}).SystemOneError(c, infoFor(constant.ChannelTypeTypeSafe, "", "k"), respWith(t, status, `{}`).R)
		if apiErr == nil {
			t.Fatalf("status %d: nil error", status)
		}
		if got, want := verdictOf(apiErr), wantVerdict(status); got != want {
			t.Errorf("status %d: verdict = %+v, want %+v", status, got, want)
		}
		if !strings.Contains(apiErr.Error(), "status") {
			t.Errorf("status %d: message does not name the upstream status: %q", status, apiErr.Error())
		}
	}
}

func TestSystemOneError_NilResponse(t *testing.T) {
	c, _ := newCtx()
	apiErr := (&Adaptor{}).SystemOneError(c, infoFor(constant.ChannelTypeTypeSafe, "", "k"), nil)
	if apiErr == nil || apiErr.StatusCode != http.StatusBadGateway || !types.IsUpstreamFailure(apiErr) {
		t.Fatalf("got %+v", apiErr)
	}
}

type closeSpy struct {
	r      *strings.Reader
	closed bool
}

func (s *closeSpy) Read(p []byte) (int, error) { return s.r.Read(p) }
func (s *closeSpy) Close() error               { s.closed = true; return nil }

func TestSystemOneError_ClosesTheBody(t *testing.T) {
	spy := &closeSpy{r: strings.NewReader(`{"detail":"x"}`)}
	resp := &http.Response{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{}, Body: spy}
	c, _ := newCtx()
	if (&Adaptor{}).SystemOneError(c, infoFor(constant.ChannelTypeTypeSafe, "", "k"), resp) == nil {
		t.Fatal("nil error")
	}
	if !spy.closed {
		t.Error("the upstream body was left open")
	}
}

func TestSystemOneError_ReadFailureStillClassifiesByStatus(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: readFailBody{}}
	c, _ := newCtx()
	apiErr := (&Adaptor{}).SystemOneError(c, infoFor(constant.ChannelTypeTypeSafe, "", "k"), resp)
	if got := verdictOf(apiErr); got != overloaded {
		t.Errorf("verdict = %+v", got)
	}
}

type readFailBody struct{}

func (readFailBody) Read([]byte) (int, error) { return 0, errors.New("boom") }
func (readFailBody) Close() error             { return nil }

func TestSystemOneError_EchoedKeyIsRedacted(t *testing.T) {
	const key = "sk-live-0123456789"
	c, _ := newCtx()
	resp := respWith(t, http.StatusUnprocessableEntity, `{"detail":"bad state for key `+key+`"}`).R
	apiErr := (&Adaptor{}).SystemOneError(c, infoFor(constant.ChannelTypeTypeSafe, "", key), resp)
	if strings.Contains(apiErr.Error(), key) || !strings.Contains(apiErr.Error(), "[redacted]") {
		t.Errorf("message = %q", apiErr.Error())
	}
}
