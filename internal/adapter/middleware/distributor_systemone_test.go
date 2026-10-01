package middleware

import (
	"net/http"
	"testing"
)

// POST /v1/systemone carries its model as a top-level body field, exactly as
// chat does, so the generic body branch must select the channel from it. A
// path-specific branch added later (the way /v1/videos and /suno/ have one)
// must not swallow it: with no model the request would route to nothing.
func TestGetModelRequest_SystemOneBodyModel(t *testing.T) {
	body := `{"model":"model-a","state":{"body":"x"},"questions":{"q":{"type":"noul","instructions":"y"}}}`
	c, _ := newTestContext(http.MethodPost, "/v1/systemone", body, "application/json")

	mr, shouldSelect, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "model-a" {
		t.Errorf("model = %q, want model-a (read from the top-level body field)", mr.Model)
	}
	if !shouldSelect {
		t.Error("shouldSelectChannel = false, want true: system one is a channel-routed relay")
	}
}

// A body with no model must reach the validator as an empty model (which it
// rejects with 400), not be defaulted to some other model by a path rule.
func TestGetModelRequest_SystemOneNoModelStaysEmpty(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/systemone", `{"state":"x","questions":{}}`, "application/json")
	mr, _, err := getModelRequest(c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if mr.Model != "" {
		t.Errorf("model = %q, want empty", mr.Model)
	}
}
