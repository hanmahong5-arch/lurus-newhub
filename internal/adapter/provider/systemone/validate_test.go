package systemone

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func answersOf(t *testing.T, id, body string) map[string]json.RawMessage {
	t.Helper()
	return map[string]json.RawMessage{id: json.RawMessage(body)}
}

func choiceReq() *dto.SystemOneRequest {
	return &dto.SystemOneRequest{Questions: map[string]dto.SystemOneQuestion{
		"dept": {Type: "choice", Criteria: json.RawMessage(`{"billing":"b","tech":"t"}`)},
	}}
}

func scoreReq() *dto.SystemOneRequest {
	return &dto.SystemOneRequest{Questions: map[string]dto.SystemOneQuestion{
		"urg": {Type: "score", Criteria: json.RawMessage(`["low","mid","high"]`)},
	}}
}

func TestValidateAnswers(t *testing.T) {
	cases := []struct {
		name string
		req  *dto.SystemOneRequest
		id   string
		body string
		ok   bool
	}{
		{"choice consistent", choiceReq(), "dept", `{"type":"choice","choice":"billing","probabilities":{"billing":0.7,"tech":0.3}}`, true},
		{"abstain null choice", choiceReq(), "dept", `{"type":"choice","choice":null,"probabilities":{"billing":0.5,"tech":0.5}}`, true},
		{"sum within tolerance", choiceReq(), "dept", `{"type":"choice","choice":"billing","probabilities":{"billing":0.705,"tech":0.3}}`, true},
		{"sum 0.05 off", choiceReq(), "dept", `{"type":"choice","choice":"billing","probabilities":{"billing":0.75,"tech":0.3}}`, false},
		{"sum 0.05 under", choiceReq(), "dept", `{"type":"choice","choice":"billing","probabilities":{"billing":0.7,"tech":0.25}}`, false},
		{"chosen not max", choiceReq(), "dept", `{"type":"choice","choice":"tech","probabilities":{"billing":0.7,"tech":0.3}}`, false},
		{"chosen within 0.01 of max", choiceReq(), "dept", `{"type":"choice","choice":"tech","probabilities":{"billing":0.505,"tech":0.495}}`, true},
		{"chosen option has no probability", choiceReq(), "dept", `{"type":"choice","choice":"sales","probabilities":{"billing":0.7,"tech":0.3}}`, false},
		{"negative probability", choiceReq(), "dept", `{"type":"choice","choice":"billing","probabilities":{"billing":1.3,"tech":-0.3}}`, false},
		{"unrequested option", choiceReq(), "dept", `{"type":"choice","choice":"sales","probabilities":{"sales":1.0}}`, false},
		{"unknown question id", choiceReq(), "other", `{"type":"choice","choice":null}`, false},
		{"type mismatch", choiceReq(), "dept", `{"type":"score","score":1,"probabilities":{"0":1.0}}`, false},
		{"score consistent", scoreReq(), "urg", `{"type":"score","score":1.3,"legend":{"0":"low","1":"mid","2":"high"},"probabilities":{"0":0.1,"1":0.5,"2":0.4}}`, true},
		{"score off by more than 0.02", scoreReq(), "urg", `{"type":"score","score":1.9,"legend":{"0":"low","1":"mid","2":"high"},"probabilities":{"0":0.1,"1":0.5,"2":0.4}}`, false},
		{"score off by 0.015 within 0.02", scoreReq(), "urg", `{"type":"score","score":1.315,"legend":{"0":"low","1":"mid","2":"high"},"probabilities":{"0":0.1,"1":0.5,"2":0.4}}`, true},
		{"legend text differs", scoreReq(), "urg", `{"type":"score","score":1.3,"legend":{"0":"low","1":"MID","2":"high"},"probabilities":{"0":0.1,"1":0.5,"2":0.4}}`, false},
		{"legend level count differs", scoreReq(), "urg", `{"type":"score","score":0,"legend":{"0":"low","1":"mid"},"probabilities":{"0":1.0}}`, false},
		{"score not keyed by index", scoreReq(), "urg", `{"type":"score","score":1,"legend":{"0":"low","1":"mid","2":"high"},"probabilities":{"a":1.0}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAnswers(tc.req, answersOf(t, tc.id, tc.body))
			if tc.ok && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("want rejection")
			}
		})
	}
}

// Without the originating request only the self-consistency rules apply.
func TestValidateAnswers_NoRequest(t *testing.T) {
	if err := validateAnswers(nil, answersOf(t, "q", `{"type":"choice","choice":"a","probabilities":{"a":0.6,"b":0.4}}`)); err != nil {
		t.Fatal(err)
	}
	if err := validateAnswers(nil, answersOf(t, "q", `{"type":"choice","choice":"b","probabilities":{"a":0.6,"b":0.4}}`)); err == nil {
		t.Fatal("chosen option below max must fail even without a request")
	}
}

// A contract violation is a 502 invalid_provider_response that must not switch
// channel, nothing may reach the caller, and a reported usage is still billed.
func TestDoResponse_ContractViolation(t *testing.T) {
	bad := `{"model":"m","answers":{"dept":{"type":"choice","choice":"billing","probabilities":{"billing":0.9,"tech":0.9}}},"usage":{"input_tokens":42,"output_tokens":0}}`
	badNoUsage := `{"model":"m","answers":{"dept":{"type":"choice","choice":"billing","probabilities":{"billing":0.9,"tech":0.9}}}}`

	run := func(body string) (any, *types.NewAPIError, int) {
		c, w := newCtx()
		info := infoFor(constant.ChannelTypeSystemOneCompatible, "http://x/", "k")
		info.Request = choiceReq()
		usage, apiErr := (&Adaptor{}).DoResponse(c, respWith(t, 200, body).R, info)
		return usage, apiErr, w.Body.Len()
	}

	usage, apiErr, written := run(bad)
	if apiErr == nil {
		t.Fatal("want error")
	}
	if apiErr.StatusCode != http.StatusBadGateway || apiErr.GetErrorCode() != types.ErrorCodeInvalidProviderResponse {
		t.Errorf("got %d %q", apiErr.StatusCode, apiErr.GetErrorCode())
	}
	if !types.IsSkipRetryError(apiErr) {
		t.Error("must be skip-retry so no sibling channel is tried")
	}
	if types.IsChannelError(apiErr) {
		t.Error("must not carry a channel: code (that would disable the channel)")
	}
	if written != 0 {
		t.Errorf("%d bytes reached the caller", written)
	}
	u, _ := usage.(*dto.Usage)
	if u == nil || u.PromptTokens != 42 || u.TotalTokens != 42 {
		t.Errorf("reported usage must be billed, got %+v", usage)
	}

	usage2, apiErr2, _ := run(badNoUsage)
	if apiErr2 == nil {
		t.Fatal("want error")
	}
	if usage2 != nil {
		t.Errorf("an estimate must not be billed on a rejected reply, got %+v", usage2)
	}
}
