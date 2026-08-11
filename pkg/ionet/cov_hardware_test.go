package ionet

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// --- GetAvailableReplicas -------------------------------------------------

func TestGetAvailableReplicas_Validation(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.GetAvailableReplicas(0, 1); err == nil || err.Error() != "hardware_id must be greater than 0" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.GetAvailableReplicas(-1, 1); err == nil || err.Error() != "hardware_id must be greater than 0" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.GetAvailableReplicas(1, 0); err == nil || err.Error() != "gpu_count must be at least 1" {
		t.Errorf("err = %v", err)
	}
}

func TestGetAvailableReplicas_Success_Mapping(t *testing.T) {
	var capturedQuery url.Values
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		u, _ := url.Parse(req.URL)
		capturedQuery = u.Query()
		return jsonResponse(200, `{"data":[
			{"id":1,"iso2":"US","name":"Ashburn","available_replicas":5},
			{"id":2,"iso2":"DE","name":"Frankfurt","available_replicas":0}
		]}`), nil
	}}
	c := newTestClient(mc)
	resp, err := c.GetAvailableReplicas(42, 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedQuery.Get("hardware_id") != "42" || capturedQuery.Get("hardware_qty") != "8" {
		t.Errorf("query = %v, want hardware_id=42 hardware_qty=8", capturedQuery)
	}
	if len(resp.Replicas) != 2 {
		t.Fatalf("Replicas len = %d, want 2", len(resp.Replicas))
	}
	r0 := resp.Replicas[0]
	if r0.LocationID != 1 || r0.LocationName != "Ashburn" || r0.AvailableCount != 5 {
		t.Errorf("Replicas[0] = %+v, want mapped from payload item", r0)
	}
	// HardwareID/MaxGPUs come from the caller's inputs, not the payload.
	if r0.HardwareID != 42 || r0.MaxGPUs != 8 {
		t.Errorf("Replicas[0] HardwareID/MaxGPUs = %d/%d, want 42/8 (input echoed)", r0.HardwareID, r0.MaxGPUs)
	}
	if resp.Replicas[1].AvailableCount != 0 {
		t.Errorf("Replicas[1].AvailableCount = %d, want 0", resp.Replicas[1].AvailableCount)
	}
}

func TestGetAvailableReplicas_DoError_And_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c := newTestClient(mc)
	if _, err := c.GetAvailableReplicas(1, 1); err == nil || !strings.Contains(err.Error(), "failed to get available replicas") {
		t.Errorf("err = %v", err)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.GetAvailableReplicas(1, 1); err == nil || !strings.Contains(err.Error(), "failed to parse available replicas response") {
		t.Errorf("err = %v", err)
	}
}

// --- GetMaxGPUsPerContainer / ListHardwareTypes ----------------------------

func TestGetMaxGPUsPerContainer_Success_And_Errors(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/hardware/max-gpus-per-container" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{"data":{"total":3,"hardware":[{"hardware_id":1,"max_gpus_per_container":8}]}}`), nil
	}}
	c := newTestClient(mc)
	resp, err := c.GetMaxGPUsPerContainer()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 3 || len(resp.Hardware) != 1 || resp.Hardware[0].MaxGPUsPerContainer != 8 {
		t.Errorf("unexpected response: %+v", resp)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.GetMaxGPUsPerContainer(); err == nil || !strings.Contains(err.Error(), "failed to get max GPUs per container") {
		t.Errorf("err = %v", err)
	}

	mc3 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c3 := newTestClient(mc3)
	if _, err := c3.GetMaxGPUsPerContainer(); err == nil || !strings.Contains(err.Error(), "failed to parse max GPU response") {
		t.Errorf("err = %v", err)
	}
}

func TestListHardwareTypes_PropagatesUnderlyingError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("upstream down")
	}}
	c := newTestClient(mc)
	_, _, err := c.ListHardwareTypes()
	if err == nil || !strings.Contains(err.Error(), "failed to list hardware types") {
		t.Errorf("err = %v", err)
	}
}

func TestListHardwareTypes_NameFallback_And_AvailableBool(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `{"data":{"total":0,"hardware":[
			{"hardware_id":7,"hardware_name":"  ","brand_name":"  NVIDIA  ","available":0,"max_gpus_per_container":4},
			{"hardware_id":9,"hardware_name":"H100","brand_name":"NVIDIA","available":3,"max_gpus_per_container":8}
		]}}`), nil
	}}
	c := newTestClient(mc)
	types, total, err := c.ListHardwareTypes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("types len = %d, want 2", len(types))
	}
	// blank hardware_name falls back to "Hardware <id>"
	if types[0].Name != "Hardware 7" {
		t.Errorf("types[0].Name = %q, want fallback 'Hardware 7'", types[0].Name)
	}
	if types[0].BrandName != "NVIDIA" {
		t.Errorf("types[0].BrandName = %q, want trimmed NVIDIA", types[0].BrandName)
	}
	if types[0].Available {
		t.Errorf("types[0].Available = true, want false when Available count is 0")
	}
	if types[1].Name != "H100" {
		t.Errorf("types[1].Name = %q, want H100 (no fallback needed)", types[1].Name)
	}
	if !types[1].Available {
		t.Errorf("types[1].Available = false, want true when Available count > 0")
	}
	// Total was 0 in the payload, so it must be recomputed as the sum of
	// per-hardware Available counts: 0 + 3 = 3.
	if total != 3 {
		t.Errorf("total = %d, want 3 (summed fallback since payload Total=0)", total)
	}
}

func TestListHardwareTypes_UsesPayloadTotalWhenNonZero(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `{"data":{"total":99,"hardware":[{"hardware_id":1,"available":1}]}}`), nil
	}}
	c := newTestClient(mc)
	_, total, err := c.ListHardwareTypes()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 99 {
		t.Errorf("total = %d, want 99 (payload Total used as-is, not recomputed sum of 1)", total)
	}
}

// --- ListLocations -------------------------------------------------------

func TestListLocations_ISO2Normalization_And_TotalFallback(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `{"data":{"total":0,"locations":[
			{"id":1,"name":"US East","iso2":" us ","available":4},
			{"id":2,"name":"EU West","iso2":"de","available":6}
		]}}`), nil
	}}
	c := newTestClient(mc)
	resp, err := c.ListLocations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Locations[0].ISO2 != "US" {
		t.Errorf("ISO2[0] = %q, want US (trimmed+uppercased)", resp.Locations[0].ISO2)
	}
	if resp.Locations[1].ISO2 != "DE" {
		t.Errorf("ISO2[1] = %q, want DE (uppercased)", resp.Locations[1].ISO2)
	}
	if resp.Total != 10 {
		t.Errorf("Total = %d, want 10 (4+6 fallback sum since payload Total=0)", resp.Total)
	}
}

func TestListLocations_UsesPayloadTotalWhenNonZero(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `{"data":{"total":50,"locations":[{"id":1,"available":1}]}}`), nil
	}}
	c := newTestClient(mc)
	resp, err := c.ListLocations()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 50 {
		t.Errorf("Total = %d, want 50 (payload value preserved)", resp.Total)
	}
}

func TestListLocations_DoError_And_DecodeError(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c := newTestClient(mc)
	if _, err := c.ListLocations(); err == nil || !strings.Contains(err.Error(), "failed to list locations") {
		t.Errorf("err = %v", err)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.ListLocations(); err == nil || !strings.Contains(err.Error(), "failed to parse locations response") {
		t.Errorf("err = %v", err)
	}
}

// --- GetHardwareType -------------------------------------------------

func TestGetHardwareType_Validation(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.GetHardwareType(0); err == nil || err.Error() != "hardware ID must be greater than 0" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.GetHardwareType(-5); err == nil || err.Error() != "hardware ID must be greater than 0" {
		t.Errorf("err = %v", err)
	}
}

func TestGetHardwareType_Success_And_InvalidJSON(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/hardware/types/17" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{"id":17,"name":"A100"}`), nil
	}}
	c := newTestClient(mc)
	hw, err := c.GetHardwareType(17)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hw.ID != 17 || hw.Name != "A100" {
		t.Errorf("unexpected hw: %+v", hw)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.GetHardwareType(1); err == nil || !strings.Contains(err.Error(), "failed to parse hardware type") {
		t.Errorf("err = %v", err)
	}

	mc3 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c3 := newTestClient(mc3)
	if _, err := c3.GetHardwareType(1); err == nil || !strings.Contains(err.Error(), "failed to get hardware type") {
		t.Errorf("err = %v", err)
	}
}

// --- GetLocation -------------------------------------------------------

func TestGetLocation_Validation(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.GetLocation(0); err == nil || err.Error() != "location ID must be greater than 0" {
		t.Errorf("err = %v", err)
	}
}

func TestGetLocation_Success_And_InvalidJSON(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/locations/5" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{"id":5,"name":"Tokyo"}`), nil
	}}
	c := newTestClient(mc)
	loc, err := c.GetLocation(5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loc.ID != 5 || loc.Name != "Tokyo" {
		t.Errorf("unexpected loc: %+v", loc)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.GetLocation(1); err == nil || !strings.Contains(err.Error(), "failed to parse location") {
		t.Errorf("err = %v", err)
	}

	mc3 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c3 := newTestClient(mc3)
	if _, err := c3.GetLocation(1); err == nil || !strings.Contains(err.Error(), "failed to get location") {
		t.Errorf("err = %v", err)
	}
}

// --- GetLocationAvailability -------------------------------------------

func TestGetLocationAvailability_Validation(t *testing.T) {
	c := newTestClient(&mockHTTPClient{})
	if _, err := c.GetLocationAvailability(0); err == nil || err.Error() != "location ID must be greater than 0" {
		t.Errorf("err = %v", err)
	}
}

func TestGetLocationAvailability_Success_And_InvalidJSON(t *testing.T) {
	mc := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		if req.URL != "https://unit-test.invalid/locations/5/availability" {
			t.Errorf("unexpected endpoint: %s", req.URL)
		}
		return jsonResponse(200, `{"location_id":5,"available":true}`), nil
	}}
	c := newTestClient(mc)
	avail, err := c.GetLocationAvailability(5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if avail.LocationID != 5 || !avail.Available {
		t.Errorf("unexpected avail: %+v", avail)
	}

	mc2 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return jsonResponse(200, `not-json`), nil
	}}
	c2 := newTestClient(mc2)
	if _, err := c2.GetLocationAvailability(1); err == nil || !strings.Contains(err.Error(), "failed to parse location availability") {
		t.Errorf("err = %v", err)
	}

	mc3 := &mockHTTPClient{DoFunc: func(req *HTTPRequest) (*HTTPResponse, error) {
		return nil, errors.New("down")
	}}
	c3 := newTestClient(mc3)
	if _, err := c3.GetLocationAvailability(1); err == nil || !strings.Contains(err.Error(), "failed to get location availability") {
		t.Errorf("err = %v", err)
	}
}
