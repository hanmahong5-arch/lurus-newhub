package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/pkg/ionet"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// R2Depl shared helpers (prefixed to avoid collisions in this shared package)
// ============================================================================

const (
	r2deplOptEnabled = "model_deployment.ionet.enabled"
	r2deplOptAPIKey  = "model_deployment.ionet.api_key"
)

// r2deplSetIonet sets the io.net deployment option flags for the duration of a
// test and restores the previous values on cleanup.
func r2deplSetIonet(t *testing.T, enabled bool, apiKey string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	prevE, hadE := common.OptionMap[r2deplOptEnabled]
	prevK, hadK := common.OptionMap[r2deplOptAPIKey]
	if enabled {
		common.OptionMap[r2deplOptEnabled] = "true"
	} else {
		common.OptionMap[r2deplOptEnabled] = "false"
	}
	common.OptionMap[r2deplOptAPIKey] = apiKey
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if hadE {
			common.OptionMap[r2deplOptEnabled] = prevE
		} else {
			delete(common.OptionMap, r2deplOptEnabled)
		}
		if hadK {
			common.OptionMap[r2deplOptAPIKey] = prevK
		} else {
			delete(common.OptionMap, r2deplOptAPIKey)
		}
	})
}

// r2deplCtx builds a bare gin test context with an attached request. body may be
// nil. params/query are applied by the caller after construction.
func r2deplCtx(method, target string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	c.Request = r
	return c, w
}

// r2deplBody parses a JSON response body into a generic map.
func r2deplBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("failed to parse response: %v, raw=%s", err, w.Body.String())
	}
	return m
}

func r2deplAssertFail(t *testing.T, w *httptest.ResponseRecorder, wantMsgSubstr string) {
	t.Helper()
	m := r2deplBody(t, w)
	if ok, _ := m["success"].(bool); ok {
		t.Fatalf("expected success=false, got true; body=%s", w.Body.String())
	}
	if wantMsgSubstr != "" {
		msg, _ := m["message"].(string)
		if !containsR2(msg, wantMsgSubstr) {
			t.Fatalf("expected message to contain %q, got %q", wantMsgSubstr, msg)
		}
	}
}

func containsR2(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}

// ============================================================================
// deployment.go — disabled-provider guard on every io.net handler
// ============================================================================

func TestR2Depl_DeploymentHandlers_NotEnabled(t *testing.T) {
	r2deplSetIonet(t, false, "")

	handlers := map[string]gin.HandlerFunc{
		"GetAllDeployments":            GetAllDeployments,
		"SearchDeployments":            SearchDeployments,
		"GetDeployment":                GetDeployment,
		"UpdateDeploymentName":         UpdateDeploymentName,
		"UpdateDeployment":             UpdateDeployment,
		"ExtendDeployment":             ExtendDeployment,
		"DeleteDeployment":             DeleteDeployment,
		"CreateDeployment":             CreateDeployment,
		"GetHardwareTypes":             GetHardwareTypes,
		"GetLocations":                 GetLocations,
		"GetAvailableReplicas":         GetAvailableReplicas,
		"GetPriceEstimation":           GetPriceEstimation,
		"CheckClusterNameAvailability": CheckClusterNameAvailability,
		"GetDeploymentLogs":            GetDeploymentLogs,
		"ListDeploymentContainers":     ListDeploymentContainers,
		"GetContainerDetails":          GetContainerDetails,
	}

	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			c, w := r2deplCtx(http.MethodPost, "/x", []byte(`{}`))
			h(c)
			// Every io.net handler acquires the client first; with the provider
			// disabled they must all short-circuit with the not-enabled error.
			r2deplAssertFail(t, w, "not enabled or api key missing")
		})
	}
}

// ============================================================================
// deployment.go — GetModelDeploymentSettings + api-key guard truthiness
// ============================================================================

func TestR2Depl_GetModelDeploymentSettings(t *testing.T) {
	t.Run("enabled_and_configured", func(t *testing.T) {
		r2deplSetIonet(t, true, "sk-io-test-key")
		c, w := r2deplCtx(http.MethodGet, "/settings", nil)
		GetModelDeploymentSettings(c)
		m := r2deplBody(t, w)
		if ok, _ := m["success"].(bool); !ok {
			t.Fatalf("expected success=true; body=%s", w.Body.String())
		}
		data, _ := m["data"].(map[string]interface{})
		if data == nil {
			t.Fatalf("expected data map; body=%s", w.Body.String())
		}
		if data["provider"] != "io.net" {
			t.Errorf("provider = %v, want io.net", data["provider"])
		}
		if data["enabled"] != true || data["configured"] != true || data["can_connect"] != true {
			t.Errorf("unexpected settings: %+v", data)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		r2deplSetIonet(t, false, "")
		c, w := r2deplCtx(http.MethodGet, "/settings", nil)
		GetModelDeploymentSettings(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if data["enabled"] != false || data["can_connect"] != false {
			t.Errorf("expected disabled settings, got %+v", data)
		}
	})
}

// ============================================================================
// deployment.go — post-client validation branches (provider enabled)
// ============================================================================

func TestR2Depl_DeploymentValidationBranches(t *testing.T) {
	r2deplSetIonet(t, true, "sk-io-test-key")

	t.Run("GetAvailableReplicas_missing_hardware_id", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/replicas", nil)
		GetAvailableReplicas(c)
		r2deplAssertFail(t, w, "hardware_id parameter is required")
	})

	t.Run("GetAvailableReplicas_invalid_hardware_id", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/replicas?hardware_id=notanint", nil)
		GetAvailableReplicas(c)
		r2deplAssertFail(t, w, "invalid hardware_id parameter")
	})

	t.Run("CheckClusterNameAvailability_missing_name", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/check-name", nil)
		CheckClusterNameAvailability(c)
		r2deplAssertFail(t, w, "name parameter is required")
	})

	t.Run("GetDeploymentLogs_missing_deployment_id", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/logs", nil)
		GetDeploymentLogs(c)
		r2deplAssertFail(t, w, "deployment ID is required")
	})

	t.Run("GetDeploymentLogs_missing_container_id", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/logs", nil)
		c.Params = gin.Params{{Key: "id", Value: "dep-1"}}
		GetDeploymentLogs(c)
		r2deplAssertFail(t, w, "container_id parameter is required")
	})

	t.Run("GetDeployment_missing_id", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/deployment", nil)
		GetDeployment(c)
		r2deplAssertFail(t, w, "deployment ID is required")
	})

	t.Run("GetContainerDetails_missing_container_id", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/container", nil)
		c.Params = gin.Params{{Key: "id", Value: "dep-1"}}
		GetContainerDetails(c)
		r2deplAssertFail(t, w, "container ID is required")
	})

	t.Run("UpdateDeploymentName_missing_name_binding", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodPut, "/deployment/dep-1", []byte(`{}`))
		c.Params = gin.Params{{Key: "id", Value: "dep-1"}}
		UpdateDeploymentName(c)
		r2deplAssertFail(t, w, "")
	})

	t.Run("UpdateDeploymentName_whitespace_name", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodPut, "/deployment/dep-1", []byte(`{"name":"   "}`))
		c.Params = gin.Params{{Key: "id", Value: "dep-1"}}
		UpdateDeploymentName(c)
		r2deplAssertFail(t, w, "deployment name cannot be empty")
	})

	t.Run("UpdateDeployment_invalid_json", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodPut, "/deployment/dep-1", []byte(`{bad`))
		c.Params = gin.Params{{Key: "id", Value: "dep-1"}}
		UpdateDeployment(c)
		r2deplAssertFail(t, w, "")
	})

	t.Run("ExtendDeployment_invalid_json", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodPut, "/deployment/dep-1", []byte(`{bad`))
		c.Params = gin.Params{{Key: "id", Value: "dep-1"}}
		ExtendDeployment(c)
		r2deplAssertFail(t, w, "")
	})

	t.Run("CreateDeployment_invalid_json", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodPost, "/deployment", []byte(`{bad`))
		CreateDeployment(c)
		r2deplAssertFail(t, w, "")
	})

	t.Run("GetPriceEstimation_invalid_json", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodPost, "/price", []byte(`{bad`))
		GetPriceEstimation(c)
		r2deplAssertFail(t, w, "")
	})
}

// ============================================================================
// deployment.go — TestIoNetConnection input-validation branches
// ============================================================================

func TestR2Depl_TestIoNetConnection_Validation(t *testing.T) {
	t.Run("invalid_payload", func(t *testing.T) {
		r2deplSetIonet(t, false, "")
		c, w := r2deplCtx(http.MethodPost, "/test", []byte(`{bad json`))
		TestIoNetConnection(c)
		r2deplAssertFail(t, w, "invalid request payload")
	})

	t.Run("api_key_required_when_none_stored", func(t *testing.T) {
		r2deplSetIonet(t, false, "")
		c, w := r2deplCtx(http.MethodPost, "/test", []byte(``))
		TestIoNetConnection(c)
		r2deplAssertFail(t, w, "api_key is required")
	})
}

// ============================================================================
// deployment.go — pure helpers
// ============================================================================

func TestR2Depl_MapIoNetDeployment(t *testing.T) {
	created := time.Unix(1_700_000_000, 0)

	t.Run("hours_and_minutes", func(t *testing.T) {
		out := mapIoNetDeployment(ionet.Deployment{
			ID:                      "d1",
			Name:                    "cluster-a",
			Status:                  "RUNNING",
			ComputeMinutesRemaining: 125, // 2h 5m
			BrandName:               "NVIDIA",
			HardwareName:            "A100",
			HardwareQuantity:        4,
			CreatedAt:               created,
		})
		if out["status"] != "running" {
			t.Errorf("status = %v, want running", out["status"])
		}
		if out["time_remaining"] != "2 hour 5 minutes" {
			t.Errorf("time_remaining = %v", out["time_remaining"])
		}
		if out["hardware_info"] != "NVIDIA A100 x4" {
			t.Errorf("hardware_info = %v", out["hardware_info"])
		}
		if out["created_at"] != created.Unix() {
			t.Errorf("created_at = %v, want %d", out["created_at"], created.Unix())
		}
	})

	t.Run("minutes_only", func(t *testing.T) {
		out := mapIoNetDeployment(ionet.Deployment{ComputeMinutesRemaining: 45, CreatedAt: created})
		if out["time_remaining"] != "45 minutes" {
			t.Errorf("time_remaining = %v, want 45 minutes", out["time_remaining"])
		}
	})

	t.Run("completed_and_zero_created", func(t *testing.T) {
		out := mapIoNetDeployment(ionet.Deployment{ComputeMinutesRemaining: 0})
		if out["time_remaining"] != "completed" {
			t.Errorf("time_remaining = %v, want completed", out["time_remaining"])
		}
		// Zero CreatedAt falls back to time.Now(); must be a plausible epoch.
		if ts, ok := out["created_at"].(int64); !ok || ts < 1_600_000_000 {
			t.Errorf("created_at fallback = %v", out["created_at"])
		}
	})
}

func TestR2Depl_ComputeStatusCounts(t *testing.T) {
	deployments := []ionet.Deployment{
		{Status: "RUNNING"},
		{Status: "running"},
		{Status: " Completed "},
		{Status: "failed"},
	}
	counts := computeStatusCounts(10, deployments)
	if counts["all"] != 10 {
		t.Errorf("all = %d, want 10", counts["all"])
	}
	if counts["running"] != 2 {
		t.Errorf("running = %d, want 2", counts["running"])
	}
	if counts["completed"] != 1 {
		t.Errorf("completed = %d, want 1", counts["completed"])
	}
	if counts["failed"] != 1 {
		t.Errorf("failed = %d, want 1", counts["failed"])
	}
	if counts["destroyed"] != 0 {
		t.Errorf("destroyed = %d, want 0", counts["destroyed"])
	}
}

func TestR2Depl_RequireIDHelpers(t *testing.T) {
	t.Run("deployment_id_present", func(t *testing.T) {
		c, _ := r2deplCtx(http.MethodGet, "/x", nil)
		c.Params = gin.Params{{Key: "id", Value: "  dep-9  "}}
		got, ok := requireDeploymentID(c)
		if !ok || got != "dep-9" {
			t.Errorf("requireDeploymentID = %q,%v", got, ok)
		}
	})
	t.Run("deployment_id_missing", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/x", nil)
		_, ok := requireDeploymentID(c)
		if ok {
			t.Fatal("expected ok=false for missing id")
		}
		r2deplAssertFail(t, w, "deployment ID is required")
	})
	t.Run("container_id_present", func(t *testing.T) {
		c, _ := r2deplCtx(http.MethodGet, "/x", nil)
		c.Params = gin.Params{{Key: "container_id", Value: "ctr-1"}}
		got, ok := requireContainerID(c)
		if !ok || got != "ctr-1" {
			t.Errorf("requireContainerID = %q,%v", got, ok)
		}
	})
	t.Run("container_id_missing", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/x", nil)
		_, ok := requireContainerID(c)
		if ok {
			t.Fatal("expected ok=false for missing container id")
		}
		r2deplAssertFail(t, w, "container ID is required")
	})
}

// ============================================================================
// task.go — checkTaskNeedUpdate + GetAllTask/GetUserTask (DB-backed)
// ============================================================================

func TestR2Depl_CheckTaskNeedUpdate(t *testing.T) {
	base := func() *repo.Task {
		return &repo.Task{
			SubmitTime: 100, StartTime: 200, FinishTime: 300,
			Status: "", FailReason: "", Progress: "50%",
			Data: json.RawMessage(`{"a":1}`),
		}
	}
	baseNew := func() dto.SunoDataResponse {
		return dto.SunoDataResponse{
			SubmitTime: 100, StartTime: 200, FinishTime: 300,
			Status: "", FailReason: "",
			Data: json.RawMessage(`{"a":1}`),
		}
	}

	t.Run("no_change", func(t *testing.T) {
		if checkTaskNeedUpdate(base(), baseNew()) {
			t.Error("expected false when nothing changed")
		}
	})
	t.Run("submit_time_changed", func(t *testing.T) {
		n := baseNew()
		n.SubmitTime = 999
		if !checkTaskNeedUpdate(base(), n) {
			t.Error("expected true when submit_time changed")
		}
	})
	t.Run("status_changed", func(t *testing.T) {
		n := baseNew()
		n.Status = "processing"
		if !checkTaskNeedUpdate(base(), n) {
			t.Error("expected true when status changed")
		}
	})
	t.Run("terminal_status_progress_not_100", func(t *testing.T) {
		old := base()
		old.Status = repo.TaskStatusFailure
		old.Progress = "50%"
		n := baseNew()
		n.Status = string(repo.TaskStatusFailure)
		if !checkTaskNeedUpdate(old, n) {
			t.Error("expected true for terminal status with progress != 100%")
		}
	})
	t.Run("data_changed", func(t *testing.T) {
		n := baseNew()
		n.Data = json.RawMessage(`{"a":2}`)
		if !checkTaskNeedUpdate(base(), n) {
			t.Error("expected true when data changed")
		}
	})
}

func TestR2Depl_GetTaskHandlers_DB(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.AutoMigrate(&repo.Task{}); err != nil {
		t.Fatalf("migrate Task: %v", err)
	}
	// Two tasks for user 2 (one Suno platform), one for another user.
	seed := []*repo.Task{
		{TaskID: "t-1", Platform: constant.TaskPlatformSuno, UserId: ctx.NormalUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%", CreatedAt: time.Now().Unix()},
		{TaskID: "t-2", Platform: constant.TaskPlatformSuno, UserId: ctx.NormalUser.Id, ChannelId: 1, Status: repo.TaskStatusSubmitted, Progress: "10%", CreatedAt: time.Now().Unix()},
		{TaskID: "t-3", Platform: constant.TaskPlatformSuno, UserId: 99999, ChannelId: 2, Status: repo.TaskStatusSuccess, Progress: "100%", CreatedAt: time.Now().Unix()},
	}
	for _, s := range seed {
		if err := ctx.DB.Create(s).Error; err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}

	t.Run("GetAllTask", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10", nil)
		// Seed spans two different (and, for user 99999, nonexistent-user)
		// owners with no shared tenant — this subtest is exercising the
		// unscoped admin view, so it must present as root (cycle-11 L8:
		// GetAllTask now scopes every non-root caller to its tenant,
		// fail-closed on an empty tenant_id — see TestV1Task_ListTenantScoped
		// in v1_cross_tenant_idor_test.go for that behavior's own lock).
		c.Set("role", common.RoleRootUser)
		GetAllTask(c)
		m := r2deplBody(t, w)
		if ok, _ := m["success"].(bool); !ok {
			t.Fatalf("expected success; body=%s", w.Body.String())
		}
		data, _ := m["data"].(map[string]interface{})
		if data == nil {
			t.Fatalf("expected data; body=%s", w.Body.String())
		}
		if total, _ := data["total"].(float64); total < 3 {
			t.Errorf("GetAllTask total = %v, want >=3", data["total"])
		}
	})

	t.Run("GetUserTask_scoped", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/tasks?page=1&page_size=10", nil)
		c.Set("id", ctx.NormalUser.Id)
		GetUserTask(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if data == nil {
			t.Fatalf("expected data; body=%s", w.Body.String())
		}
		// Only the two tasks owned by NormalUser must be visible.
		if total, _ := data["total"].(float64); total != 2 {
			t.Errorf("GetUserTask total = %v, want 2", data["total"])
		}
	})
}

// ============================================================================
// task_video.go — redactVideoResponseBody + truncateBase64
// ============================================================================

func TestR2Depl_RedactVideoResponseBody(t *testing.T) {
	t.Run("invalid_json_passthrough", func(t *testing.T) {
		in := []byte(`not json at all`)
		out := redactVideoResponseBody(in)
		if !bytes.Equal(in, out) {
			t.Errorf("expected passthrough for invalid json, got %s", out)
		}
	})

	t.Run("redacts_base64_and_truncates_video", func(t *testing.T) {
		longVid := make([]byte, 400)
		for i := range longVid {
			longVid[i] = 'A'
		}
		payload := map[string]any{
			"response": map[string]any{
				"bytesBase64Encoded": "should-be-removed",
				"video":              string(longVid),
				"videos": []any{
					map[string]any{"bytesBase64Encoded": "x", "keep": "yes"},
				},
			},
		}
		raw, _ := json.Marshal(payload)
		out := redactVideoResponseBody(raw)

		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("output not json: %v", err)
		}
		resp := got["response"].(map[string]any)
		if _, exists := resp["bytesBase64Encoded"]; exists {
			t.Error("bytesBase64Encoded should be deleted")
		}
		vid := resp["video"].(string)
		if len(vid) != 256+3 || vid[len(vid)-3:] != "..." {
			t.Errorf("video not truncated correctly: len=%d", len(vid))
		}
		vids := resp["videos"].([]any)
		vm := vids[0].(map[string]any)
		if _, exists := vm["bytesBase64Encoded"]; exists {
			t.Error("nested bytesBase64Encoded should be deleted")
		}
		if vm["keep"] != "yes" {
			t.Error("non-base64 fields must be preserved")
		}
	})

	t.Run("no_response_key", func(t *testing.T) {
		raw := []byte(`{"other":1}`)
		out := redactVideoResponseBody(raw)
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("output not json: %v", err)
		}
		if got["other"].(float64) != 1 {
			t.Errorf("unexpected output: %s", out)
		}
	})
}

func TestR2Depl_TruncateBase64(t *testing.T) {
	if got := truncateBase64("short"); got != "short" {
		t.Errorf("short string changed: %q", got)
	}
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'B'
	}
	got := truncateBase64(string(long))
	if len(got) != 256+3 || got[len(got)-3:] != "..." {
		t.Errorf("long truncate wrong: len=%d suffix=%q", len(got), got[len(got)-3:])
	}
}

// ============================================================================
// midjourney.go — checkMjTaskNeedUpdate + GetAllMidjourney/GetUserMidjourney
// ============================================================================

func TestR2Depl_CheckMjTaskNeedUpdate(t *testing.T) {
	base := func() *repo.Midjourney {
		return &repo.Midjourney{
			Code: 1, Progress: "100%", PromptEn: "cat", State: "s",
			SubmitTime: 1, StartTime: 2, FinishTime: 3,
			ImageUrl: "u", Status: "SUCCESS", FailReason: "",
			VideoUrl: "", VideoUrls: "",
		}
	}
	baseNew := func() dto.MidjourneyDto {
		return dto.MidjourneyDto{
			Progress: "100%", PromptEn: "cat", State: "s",
			SubmitTime: 1, StartTime: 2, FinishTime: 3,
			ImageUrl: "u", Status: "SUCCESS", FailReason: "",
			VideoUrl: "",
		}
	}

	t.Run("no_change", func(t *testing.T) {
		if checkMjTaskNeedUpdate(base(), baseNew()) {
			t.Error("expected false when nothing changed")
		}
	})
	t.Run("code_not_one", func(t *testing.T) {
		old := base()
		old.Code = 0
		if !checkMjTaskNeedUpdate(old, baseNew()) {
			t.Error("expected true when code != 1")
		}
	})
	t.Run("progress_changed", func(t *testing.T) {
		n := baseNew()
		n.Progress = "50%"
		if !checkMjTaskNeedUpdate(base(), n) {
			t.Error("expected true when progress changed")
		}
	})
	t.Run("video_url_changed", func(t *testing.T) {
		n := baseNew()
		n.VideoUrl = "https://v"
		if !checkMjTaskNeedUpdate(base(), n) {
			t.Error("expected true when video url changed")
		}
	})
	t.Run("video_urls_added", func(t *testing.T) {
		n := baseNew()
		n.VideoUrls = []dto.ImgUrls{{Url: "https://v1"}}
		if !checkMjTaskNeedUpdate(base(), n) {
			t.Error("expected true when video urls added")
		}
	})
}

func TestR2Depl_GetMidjourneyHandlers_DB(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.AutoMigrate(&repo.Midjourney{}); err != nil {
		t.Fatalf("migrate Midjourney: %v", err)
	}
	seed := []*repo.Midjourney{
		{Code: 1, MjId: "mj-1", UserId: ctx.NormalUser.Id, ChannelId: 1, Status: "SUCCESS", Progress: "100%", SubmitTime: time.Now().Unix()},
		{Code: 1, MjId: "mj-2", UserId: ctx.NormalUser.Id, ChannelId: 1, Status: "SUBMITTED", Progress: "10%", SubmitTime: time.Now().Unix()},
		{Code: 1, MjId: "mj-3", UserId: 88888, ChannelId: 2, Status: "SUCCESS", Progress: "100%", SubmitTime: time.Now().Unix()},
	}
	for _, s := range seed {
		if err := ctx.DB.Create(s).Error; err != nil {
			t.Fatalf("seed midjourney: %v", err)
		}
	}

	t.Run("GetAllMidjourney", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/mj?page=1&page_size=10", nil)
		// Same reasoning as the GetAllTask subtest above: this exercises the
		// unscoped admin view across two different owners with no shared
		// tenant, so it must present as root (cycle-11 L8 tenant scoping).
		c.Set("role", common.RoleRootUser)
		GetAllMidjourney(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if data == nil {
			t.Fatalf("expected data; body=%s", w.Body.String())
		}
		if total, _ := data["total"].(float64); total < 3 {
			t.Errorf("GetAllMidjourney total = %v, want >=3", data["total"])
		}
	})

	t.Run("GetUserMidjourney_scoped", func(t *testing.T) {
		c, w := r2deplCtx(http.MethodGet, "/mj?page=1&page_size=10", nil)
		c.Set("id", ctx.NormalUser.Id)
		GetUserMidjourney(c)
		data, _ := r2deplBody(t, w)["data"].(map[string]interface{})
		if data == nil {
			t.Fatalf("expected data; body=%s", w.Body.String())
		}
		if total, _ := data["total"].(float64); total != 2 {
			t.Errorf("GetUserMidjourney total = %v, want 2", data["total"])
		}
	})
}

// ============================================================================
// playground.go — resolvePlaygroundBaseURL + Playground access-token guard
// ============================================================================

func TestR2Depl_ResolvePlaygroundBaseURL(t *testing.T) {
	prevOverride := playgroundUpstreamBaseURL
	prevPort, hadPort := os.LookupEnv("PORT")
	t.Cleanup(func() {
		playgroundUpstreamBaseURL = prevOverride
		if hadPort {
			_ = os.Setenv("PORT", prevPort)
		} else {
			_ = os.Unsetenv("PORT")
		}
	})

	t.Run("explicit_override", func(t *testing.T) {
		playgroundUpstreamBaseURL = "http://override.local:9999"
		if got := resolvePlaygroundBaseURL(); got != "http://override.local:9999" {
			t.Errorf("got %q, want override", got)
		}
	})

	t.Run("port_env", func(t *testing.T) {
		playgroundUpstreamBaseURL = ""
		_ = os.Setenv("PORT", "8123")
		if got := resolvePlaygroundBaseURL(); got != "http://127.0.0.1:8123" {
			t.Errorf("got %q, want port-derived", got)
		}
	})

	t.Run("default_port", func(t *testing.T) {
		playgroundUpstreamBaseURL = ""
		_ = os.Unsetenv("PORT")
		if got := resolvePlaygroundBaseURL(); got != "http://127.0.0.1:3000" {
			t.Errorf("got %q, want default 3000", got)
		}
	})
}

// ============================================================================
// task.go / midjourney.go — context-cancellation stop paths + empty dispatch
// ============================================================================

func TestR2Depl_BulkUpdateContextCancel(t *testing.T) {
	// A pre-cancelled context must make the pollers return immediately via the
	// ctx.Done() branch rather than block on the 15s ticker.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{}, 2)
	go func() { UpdateTaskBulkWithContext(ctx); done <- struct{}{} }()
	go func() { UpdateMidjourneyTaskBulkWithContext(ctx); done <- struct{}{} }()

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("bulk updater did not return on cancelled context")
		}
	}
}

func TestR2Depl_DispatchEmptyTaskLists(t *testing.T) {
	ctx := context.Background()
	// Channel present but no task IDs => inner updaters hit their len==0 guard
	// and return nil without any network / DB access.
	empty := map[int][]string{1: {}}
	taskM := map[string]*repo.Task{}

	if err := UpdateSunoTaskAll(ctx, empty, taskM); err != nil {
		t.Errorf("UpdateSunoTaskAll empty = %v, want nil", err)
	}
	if err := UpdateVideoTaskAll(ctx, constant.TaskPlatformMusic, empty, taskM); err != nil {
		t.Errorf("UpdateVideoTaskAll empty = %v, want nil", err)
	}

	// Dispatch routing: Suno, Midjourney (no-op), and default (video) branches.
	UpdateTaskByPlatform(ctx, constant.TaskPlatformSuno, empty, taskM)
	UpdateTaskByPlatform(ctx, constant.TaskPlatformMidjourney, empty, taskM)
	UpdateTaskByPlatform(ctx, constant.TaskPlatformMusic, empty, taskM)
}

func TestR2Depl_Playground_AccessTokenRejected(t *testing.T) {
	c, w := r2deplCtx(http.MethodPost, "/playground", []byte(`{}`))
	c.Set("use_access_token", true)
	Playground(c)
	if w.Code < 400 {
		t.Fatalf("expected error status, got %d", w.Code)
	}
	m := r2deplBody(t, w)
	if _, ok := m["error"]; !ok {
		t.Errorf("expected error field in body; got %s", w.Body.String())
	}
}
