package handler

// cov_handler-deep-c_deployment_bind_test.go — closes a specific gap left by
// cov_handler-deploy_ionet_test.go's own honest_notes: that file thoroughly
// covers the "io.net disabled" guard (which returns before ANY binding), but
// never exercises the ShouldBindJSON error branch of CreateDeployment /
// UpdateDeployment / ExtendDeployment / GetPriceEstimation with the
// integration ENABLED — those calls short-circuit on a malformed body
// before ever reaching the (out-of-scope, real-network) client call, so
// they're reachable without touching io.net.

import (
	"net/http"
	"testing"
)

func TestDeploymentWriteEndpoints_EnabledButMalformedJSON_BindErrorBeforeNetwork(t *testing.T) {
	handlerDeployIonetOptionSnapshot(t)
	handlerDeploySetIonetOption(true, "sk-fake-enabled-key")

	r := handlerDeployNewRouter()
	r.POST("/deployments", CreateDeployment)
	r.PUT("/deployments/:id", UpdateDeployment)
	r.POST("/deployments/:id/extend", ExtendDeployment)
	r.POST("/price", GetPriceEstimation)

	cases := []struct {
		name         string
		method, path string
		body         string
	}{
		// duration_hours is typed int in every one of these request structs;
		// a JSON string value there is a genuine type-mismatch unmarshal
		// error, not just "missing optional field".
		{"CreateDeployment_TypeMismatch", http.MethodPost, "/deployments", `{"duration_hours":"not-a-number"}`},
		{"CreateDeployment_TruncatedJSON", http.MethodPost, "/deployments", `{"duration_hours":`},
		{"UpdateDeployment_TypeMismatch", http.MethodPut, "/deployments/dep-1", `{"traffic_port":"not-a-number"}`},
		{"ExtendDeployment_TypeMismatch", http.MethodPost, "/deployments/dep-1/extend", `{"duration_hours":"not-a-number"}`},
		{"GetPriceEstimation_TypeMismatch", http.MethodPost, "/price", `{"hardware_id":"not-a-number"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := handlerDeployDo(r, tc.method, tc.path, []byte(tc.body))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (envelope always 200), body=%s", w.Code, w.Body.String())
			}
			resp := handlerDeployParseBody(t, w)
			if resp["success"] != false {
				t.Fatalf("success = %v, want false for malformed JSON body, body=%s", resp["success"], w.Body.String())
			}
			// Must fail via bind, not the (impossible here) network call: the
			// gin binding error surfaces through common.ApiError, which is
			// NOT the literal guard-clause strings used by the disabled-gate
			// tests, so just assert a non-empty message was produced.
			if msg, _ := resp["message"].(string); msg == "" {
				t.Errorf("expected a non-empty bind-error message, body=%s", w.Body.String())
			}
		})
	}
}
