package handler

// faultsim_test.go — oracle for the task-vendor fault simulator (cycle-8
// L8): FaultSimTaskSubmit/FaultSimTaskFetch imitate the Suno wire so
// POST/GET /v1/tasks/:platform is provable on UAT with no vendor key. The
// router-level auth/registration wiring (env-gated existence,
// FAULTSIM_TOKEN required) already has its own lock in
// router/faultsim_wiring_test.go for the chat-completions sibling; this
// file is the handler-level oracle for what the two new endpoints answer.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestFaultSimTask_SubmitThenFetchSuccessWithDataArtefacts drives a submit
// then a fetch for the id it returned, and asserts the fetch answers
// SUCCESS with the two data: URL artefacts (text/plain and image) the L8
// spec calls for — the round trip the UAT probe (deploy/k8s/r6-uat/README.md)
// exercises over HTTP.
func TestFaultSimTask_SubmitThenFetchSuccessWithDataArtefacts(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "test-token")
	gin.SetMode(gin.TestMode)

	subW := httptest.NewRecorder()
	subC, _ := gin.CreateTestContext(subW)
	subC.Request = httptest.NewRequest(http.MethodPost, "/faultsim/suno/submit/MUSIC", strings.NewReader(`{}`))
	subC.Request.Header.Set("X-Faultsim-Token", "test-token")
	subC.Params = gin.Params{{Key: "action", Value: "MUSIC"}}
	FaultSimTaskSubmit(subC)

	if subW.Code != http.StatusOK {
		t.Fatalf("submit status = %d, want 200; body=%s", subW.Code, subW.Body.String())
	}
	var subResp struct {
		Code string `json:"code"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(subW.Body.Bytes(), &subResp); err != nil {
		t.Fatalf("decode submit response: %v; body=%s", err, subW.Body.String())
	}
	if subResp.Code != "success" || subResp.Data == "" {
		t.Fatalf("submit response = %+v, want code=success and a non-empty task id", subResp)
	}
	taskID := subResp.Data

	fetchW := httptest.NewRecorder()
	fetchC, _ := gin.CreateTestContext(fetchW)
	fetchC.Request = httptest.NewRequest(http.MethodPost, "/faultsim/suno/fetch", strings.NewReader(`{"ids":["`+taskID+`"]}`))
	fetchC.Request.Header.Set("X-Faultsim-Token", "test-token")
	FaultSimTaskFetch(fetchC)

	if fetchW.Code != http.StatusOK {
		t.Fatalf("fetch status = %d, want 200; body=%s", fetchW.Code, fetchW.Body.String())
	}
	var fetchResp struct {
		Code string `json:"code"`
		Data []struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
			Data   struct {
				URL      string `json:"url"`
				ImageURL string `json:"image_url"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(fetchW.Body.Bytes(), &fetchResp); err != nil {
		t.Fatalf("decode fetch response: %v; body=%s", err, fetchW.Body.String())
	}
	if fetchResp.Code != "success" || len(fetchResp.Data) != 1 {
		t.Fatalf("fetch response = %+v, want exactly one item", fetchResp)
	}
	item := fetchResp.Data[0]
	if item.TaskID != taskID {
		t.Errorf("fetched task_id = %q, want %q", item.TaskID, taskID)
	}
	if item.Status != "SUCCESS" {
		t.Errorf("status = %q, want SUCCESS", item.Status)
	}
	if !strings.HasPrefix(item.Data.URL, "data:text/plain;base64,") {
		t.Errorf("url artefact = %q, want a data: text/plain URL", item.Data.URL)
	}
	if !strings.HasPrefix(item.Data.ImageURL, "data:image/") {
		t.Errorf("image_url artefact = %q, want a data: image URL", item.Data.ImageURL)
	}
}

func TestFaultSimTask_SubmitRequiresToken(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "test-token")
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/faultsim/suno/submit/MUSIC", nil)
	FaultSimTaskSubmit(c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a token", w.Code)
	}
}

func TestFaultSimTask_FetchRequiresToken(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "test-token")
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/faultsim/suno/fetch", strings.NewReader(`{"ids":["x"]}`))
	FaultSimTaskFetch(c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a token", w.Code)
	}
}
