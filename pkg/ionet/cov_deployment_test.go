package ionet

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func validDeployRequest() *DeploymentRequest {
	return &DeploymentRequest{
		ResourcePrivateName: "my-app",
		LocationIDs:         []int{1, 2},
		HardwareID:          10,
		GPUsPerContainer:    1,
		DurationHours:       1,
		RegistryConfig:      RegistryConfig{ImageURL: "docker.io/example/app:latest"},
		ContainerConfig:     ContainerConfig{ReplicaCount: 1},
	}
}

// --- DeployContainer --------------------------------------------------

func TestDeployContainer_Validation(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})

	if _, err := c.DeployContainer(nil); err == nil || err.Error() != "deployment request cannot be nil" {
		t.Errorf("nil req: err = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*DeploymentRequest)
		wantErr string
	}{
		{"missing name", func(r *DeploymentRequest) { r.ResourcePrivateName = "" }, "resource_private_name is required"},
		{"missing locations", func(r *DeploymentRequest) { r.LocationIDs = nil }, "location_ids is required"},
		{"bad hardware", func(r *DeploymentRequest) { r.HardwareID = 0 }, "hardware_id is required"},
		{"missing image", func(r *DeploymentRequest) { r.RegistryConfig.ImageURL = "" }, "registry_config.image_url is required"},
		{"gpus zero", func(r *DeploymentRequest) { r.GPUsPerContainer = 0 }, "gpus_per_container must be at least 1"},
		{"duration zero", func(r *DeploymentRequest) { r.DurationHours = 0 }, "duration_hours must be at least 1"},
		{"replica zero", func(r *DeploymentRequest) { r.ContainerConfig.ReplicaCount = 0 }, "container_config.replica_count must be at least 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validDeployRequest()
			tt.mutate(req)
			_, err := c.DeployContainer(req)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestDeployContainer_Success(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.Method != "POST" || req.URL != "https://unit-test.invalid/deploy" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
		}
		if !strings.Contains(string(req.Body), "my-app") {
			t.Errorf("body missing resource name: %s", req.Body)
		}
		return jsonResponse(200, `{"deployment_id":"dep-xyz","status":"pending"}`), nil
	}}
	c := newTestClient(mc)
	resp, err := c.DeployContainer(validDeployRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.DeploymentID != "dep-xyz" || resp.Status != "pending" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestDeployContainer_DoError_And_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("boom")
	}}
	c := newTestClient(mc)
	if _, err := c.DeployContainer(validDeployRequest()); err == nil || !strings.Contains(err.Error(), "failed to deploy container") {
		t.Errorf("err = %v", err)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.DeployContainer(validDeployRequest()); err == nil || !strings.Contains(err.Error(), "failed to parse deployment response") {
		t.Errorf("err = %v", err)
	}
}

// --- ListDeployments -------------------------------------------------

func TestListDeployments_NilOpts_NoQueryString(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/deployments" {
			t.Errorf("URL = %q, want no query string for nil opts", req.URL)
		}
		return jsonResponse(200, `{"data":{"deployments":[],"total":0}}`), nil
	}}
	c := newTestClient(mc)
	if _, err := c.ListDeployments(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListDeployments_OptsAndDerivedFields(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		u, _ := url.Parse(req.URL)
		q := u.Query()
		if q.Get("status") != "running" {
			t.Errorf("status = %q, want running", q.Get("status"))
		}
		if q.Get("page") != "2" {
			t.Errorf("page = %q, want 2", q.Get("page"))
		}
		return jsonResponse(200, `{"data":{"deployments":[
			{"id":"a","hardware_quantity":4},
			{"id":"b","hardware_quantity":8}
		],"total":2}}`), nil
	}}
	c := newTestClient(mc)
	list, err := c.ListDeployments(&ListDeploymentsOptions{Status: "running", Page: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list.Deployments) != 2 {
		t.Fatalf("Deployments len = %d, want 2", len(list.Deployments))
	}
	// GPUCount and Replicas must be derived from HardwareQuantity.
	for _, d := range list.Deployments {
		if d.GPUCount != d.HardwareQuantity || d.Replicas != d.HardwareQuantity {
			t.Errorf("deployment %+v: GPUCount/Replicas not derived from HardwareQuantity", d)
		}
	}
	if list.Deployments[0].GPUCount != 4 || list.Deployments[1].GPUCount != 8 {
		t.Errorf("unexpected GPUCount values: %+v", list.Deployments)
	}
}

func TestListDeployments_DoError_And_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c := newTestClient(mc)
	if _, err := c.ListDeployments(nil); err == nil || !strings.Contains(err.Error(), "failed to list deployments") {
		t.Errorf("err = %v", err)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.ListDeployments(nil); err == nil || !strings.Contains(err.Error(), "failed to parse deployments list") {
		t.Errorf("err = %v", err)
	}
}

// --- GetDeployment / UpdateDeployment / ExtendDeployment / DeleteDeployment --

func TestGetDeployment_EmptyID(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.GetDeployment(""); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

func TestGetDeployment_DoError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c := newTestClient(mc)
	if _, err := c.GetDeployment("dep1"); err == nil || !strings.Contains(err.Error(), "failed to get deployment details") {
		t.Errorf("err = %v", err)
	}
}

func TestGetDeployment_Success_FlexibleTime(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/deployment/dep1" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{"data":{"id":"dep1","status":"active","created_at":"2026-03-04T05:06:07","started_at":"2026-03-04T05:07:00.5"}}`), nil
	}}
	c := newTestClient(mc)
	detail, err := c.GetDeployment("dep1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.ID != "dep1" || detail.Status != "active" {
		t.Errorf("unexpected detail: %+v", detail)
	}
	if detail.CreatedAt.IsZero() {
		t.Error("CreatedAt should be normalized, not zero")
	}
	if detail.StartedAt == nil || detail.StartedAt.IsZero() {
		t.Error("StartedAt should be normalized and non-nil")
	}
}

func TestGetDeployment_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c := newTestClient(mc)
	if _, err := c.GetDeployment("dep1"); err == nil || !strings.Contains(err.Error(), "failed to parse deployment details") {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateDeployment_Validation(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.UpdateDeployment("", &UpdateDeploymentRequest{}); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.UpdateDeployment("dep1", nil); err == nil || err.Error() != "update request cannot be nil" {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateDeployment_Success_And_Errors(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.Method != "PATCH" {
			t.Errorf("method = %s, want PATCH", req.Method)
		}
		return jsonResponse(200, `{"status":"updated","deployment_id":"dep1"}`), nil
	}}
	c := newTestClient(mc)
	resp, err := c.UpdateDeployment("dep1", &UpdateDeploymentRequest{ImageURL: "new:image"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "updated" {
		t.Errorf("unexpected response: %+v", resp)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `bad-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.UpdateDeployment("dep1", &UpdateDeploymentRequest{}); err == nil || !strings.Contains(err.Error(), "failed to parse update deployment response") {
		t.Errorf("err = %v", err)
	}

	mc3 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c3 := newTestClient(mc3)
	if _, err := c3.UpdateDeployment("dep1", &UpdateDeploymentRequest{}); err == nil || !strings.Contains(err.Error(), "failed to update deployment") {
		t.Errorf("err = %v", err)
	}
}

func TestExtendDeployment_Validation(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.ExtendDeployment("", &ExtendDurationRequest{DurationHours: 1}); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.ExtendDeployment("dep1", nil); err == nil || err.Error() != "extend request cannot be nil" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.ExtendDeployment("dep1", &ExtendDurationRequest{DurationHours: 0}); err == nil || err.Error() != "duration_hours must be at least 1" {
		t.Errorf("err = %v", err)
	}
}

func TestExtendDeployment_Success(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/deployment/dep1/extend" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{"data":{"id":"dep1","status":"extended"}}`), nil
	}}
	c := newTestClient(mc)
	detail, err := c.ExtendDeployment("dep1", &ExtendDurationRequest{DurationHours: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.Status != "extended" {
		t.Errorf("unexpected detail: %+v", detail)
	}
}

func TestExtendDeployment_DoError_And_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c := newTestClient(mc)
	if _, err := c.ExtendDeployment("dep1", &ExtendDurationRequest{DurationHours: 1}); err == nil || !strings.Contains(err.Error(), "failed to extend deployment") {
		t.Errorf("err = %v", err)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.ExtendDeployment("dep1", &ExtendDurationRequest{DurationHours: 1}); err == nil || !strings.Contains(err.Error(), "failed to parse extended deployment details") {
		t.Errorf("err = %v", err)
	}
}

func TestDeleteDeployment_EmptyID(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.DeleteDeployment(""); err == nil || err.Error() != "deployment ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

func TestDeleteDeployment_Success_And_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.Method != "DELETE" {
			t.Errorf("method = %s, want DELETE", req.Method)
		}
		return jsonResponse(200, `{"status":"deleted","deployment_id":"dep1"}`), nil
	}}
	c := newTestClient(mc)
	resp, err := c.DeleteDeployment("dep1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "deleted" {
		t.Errorf("unexpected response: %+v", resp)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `bad`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.DeleteDeployment("dep1"); err == nil || !strings.Contains(err.Error(), "failed to parse delete deployment response") {
		t.Errorf("err = %v", err)
	}

	mc3 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c3 := newTestClient(mc3)
	if _, err := c3.DeleteDeployment("dep1"); err == nil || !strings.Contains(err.Error(), "failed to delete deployment") {
		t.Errorf("err = %v", err)
	}
}

// --- GetPriceEstimation -------------------------------------------------

func validPriceRequest() *PriceEstimationRequest {
	return &PriceEstimationRequest{
		LocationIDs:      []int{1},
		HardwareID:       10,
		GPUsPerContainer: 2,
		DurationHours:    4,
		ReplicaCount:     1,
	}
}

func TestGetPriceEstimation_Validation(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.GetPriceEstimation(nil); err == nil || err.Error() != "price estimation request cannot be nil" {
		t.Errorf("err = %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(*PriceEstimationRequest)
		wantErr string
	}{
		{"no locations", func(r *PriceEstimationRequest) { r.LocationIDs = nil }, "location_ids is required"},
		{"no hardware", func(r *PriceEstimationRequest) { r.HardwareID = 0 }, "hardware_id is required"},
		{"no replicas", func(r *PriceEstimationRequest) { r.ReplicaCount = 0 }, "replica_count must be at least 1"},
		{"no duration/hardware qty", func(r *PriceEstimationRequest) {
			r.DurationHours = 0
			r.DurationQty = 0
		}, "duration_qty must be at least 1"},
		{"no hardware qty fallback", func(r *PriceEstimationRequest) {
			r.GPUsPerContainer = 0
			r.HardwareQty = 0
		}, "hardware_qty must be at least 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validPriceRequest()
			tt.mutate(req)
			_, err := c.GetPriceEstimation(req)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestGetPriceEstimation_CurrencyAndDurationDefaults(t *testing.T) {
	var capturedQuery url.Values
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		u, _ := url.Parse(req.URL)
		capturedQuery = u.Query()
		return jsonResponse(200, `{"data":{"total_cost_usdc":100,"ionet_fee":5,"currency_conversion_fee":2}}`), nil
	}}
	c := newTestClient(mc)

	req := validPriceRequest()
	req.Currency = "   " // whitespace-only -> defaults to usdc
	req.DurationType = ""
	resp, err := c.GetPriceEstimation(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Currency != "USDC" {
		t.Errorf("Currency = %q, want USDC (default, uppercased)", resp.Currency)
	}
	if capturedQuery.Get("currency") != "usdc" {
		t.Errorf("query currency = %q, want usdc", capturedQuery.Get("currency"))
	}
	if capturedQuery.Get("duration_type") != "hourly" {
		t.Errorf("query duration_type = %q, want hourly default", capturedQuery.Get("duration_type"))
	}
	// EstimatedCost / breakdown must reflect the parsed API response, not zero values.
	if resp.EstimatedCost != 100 {
		t.Errorf("EstimatedCost = %v, want 100", resp.EstimatedCost)
	}
	if resp.PriceBreakdown.ComputeCost != 93 { // 100 - 5 - 2
		t.Errorf("ComputeCost = %v, want 93 (total - ionet_fee - conversion_fee)", resp.PriceBreakdown.ComputeCost)
	}
	// HourlyRate = total / durationHoursForRate; durationHoursForRate falls
	// back to DurationHours=4 for the default "hour" duration type.
	if resp.PriceBreakdown.HourlyRate != 25 { // 100/4
		t.Errorf("HourlyRate = %v, want 25 (100/4h)", resp.PriceBreakdown.HourlyRate)
	}
}

func TestGetPriceEstimation_DurationTypeBranches(t *testing.T) {
	tests := []struct {
		name             string
		durationType     string
		durationQty      int
		wantAPIDuration  string
		wantHoursForRate int
	}{
		{"day", "day", 2, "daily", 48},
		{"days plural", "days", 1, "daily", 24},
		{"weekly", "weekly", 1, "weekly", 168},
		{"month", "month", 1, "monthly", 720},
		{"unknown falls back to hourly label", "fortnight", 3, "hourly", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedQuery url.Values
			mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
				u, _ := url.Parse(req.URL)
				capturedQuery = u.Query()
				return jsonResponse(200, `{"data":{"total_cost_usdc":240}}`), nil
			}}
			c := newTestClient(mc)
			req := validPriceRequest()
			req.DurationHours = 0 // force reliance on DurationQty/duration type math
			req.DurationType = tt.durationType
			req.DurationQty = tt.durationQty
			resp, err := c.GetPriceEstimation(req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if capturedQuery.Get("duration_type") != tt.wantAPIDuration {
				t.Errorf("duration_type = %q, want %q", capturedQuery.Get("duration_type"), tt.wantAPIDuration)
			}
			wantRate := 240.0 / float64(tt.wantHoursForRate)
			if resp.PriceBreakdown.HourlyRate != wantRate {
				t.Errorf("HourlyRate = %v, want %v (240/%dh)", resp.PriceBreakdown.HourlyRate, wantRate, tt.wantHoursForRate)
			}
		})
	}
}

func TestGetPriceEstimation_DoError_And_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c := newTestClient(mc)
	if _, err := c.GetPriceEstimation(validPriceRequest()); err == nil || !strings.Contains(err.Error(), "failed to get price estimation") {
		t.Errorf("err = %v", err)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.GetPriceEstimation(validPriceRequest()); err == nil || !strings.Contains(err.Error(), "failed to parse price estimation response") {
		t.Errorf("err = %v", err)
	}
}

// --- CheckClusterNameAvailability ----------------------------------------

func TestCheckClusterNameAvailability_EmptyName(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.CheckClusterNameAvailability(""); err == nil || err.Error() != "cluster name cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

func TestCheckClusterNameAvailability_TrueAndFalse(t *testing.T) {
	for _, want := range []bool{true, false} {
		body := "false"
		if want {
			body = "true"
		}
		mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
			return jsonResponse(200, body), nil
		}}
		c := newTestClient(mc)
		got, err := c.CheckClusterNameAvailability("my-cluster")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != want {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestCheckClusterNameAvailability_InvalidJSON(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-a-bool`), nil
	}}
	c := newTestClient(mc)
	if _, err := c.CheckClusterNameAvailability("x"); err == nil || !strings.Contains(err.Error(), "failed to parse cluster name availability response") {
		t.Errorf("err = %v", err)
	}
}

func TestCheckClusterNameAvailability_DoError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c := newTestClient(mc)
	if _, err := c.CheckClusterNameAvailability("x"); err == nil || !strings.Contains(err.Error(), "failed to check cluster name availability") {
		t.Errorf("err = %v", err)
	}
}

// --- UpdateClusterName ------------------------------------------------

func TestUpdateClusterName_Validation(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.UpdateClusterName("", &UpdateClusterNameRequest{Name: "n"}); err == nil || err.Error() != "cluster ID cannot be empty" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.UpdateClusterName("id1", nil); err == nil || err.Error() != "update cluster name request cannot be nil" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.UpdateClusterName("id1", &UpdateClusterNameRequest{Name: ""}); err == nil || err.Error() != "cluster name cannot be empty" {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateClusterName_Success_And_InvalidJSON(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.Method != "PUT" || req.URL != "https://unit-test.invalid/clusters/id1/update-name" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL)
		}
		return jsonResponse(200, `{"status":"ok","message":"renamed"}`), nil
	}}
	c := newTestClient(mc)
	resp, err := c.UpdateClusterName("id1", &UpdateClusterNameRequest{Name: "new-名前"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "ok" || resp.Message != "renamed" {
		t.Errorf("unexpected response: %+v", resp)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `bad`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.UpdateClusterName("id1", &UpdateClusterNameRequest{Name: "x"}); err == nil || !strings.Contains(err.Error(), "failed to parse update cluster name response") {
		t.Errorf("err = %v", err)
	}

	mc3 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c3 := newTestClient(mc3)
	if _, err := c3.UpdateClusterName("id1", &UpdateClusterNameRequest{Name: "x"}); err == nil || !strings.Contains(err.Error(), "failed to update cluster name") {
		t.Errorf("err = %v", err)
	}
}
