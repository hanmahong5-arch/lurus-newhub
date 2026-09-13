package music

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// TaskAdaptor translates the OpenAI-compatible /v1/audio/music request
// to the Suno upstream format and back.
type TaskAdaptor struct {
	ChannelType int
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	var req dto.MusicSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return app.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if req.Prompt == "" {
		return app.TaskErrorWrapperLocal(fmt.Errorf("prompt is required"), "invalid_request", http.StatusBadRequest)
	}

	info.Action = "MUSIC"
	c.Set("music_request", &req)
	return nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	baseURL := info.ChannelBaseUrl
	return fmt.Sprintf("%s/suno/submit/music", baseURL), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+info.ApiKey)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	raw, ok := c.Get("music_request")
	if !ok {
		return nil, fmt.Errorf("music request not found in context")
	}
	musicReq := raw.(*dto.MusicSubmitReq)

	// Build prompt: combine style hint with user prompt.
	prompt := musicReq.Prompt
	if musicReq.Style != "" {
		prompt = fmt.Sprintf("[Style: %s] %s", musicReq.Style, prompt)
	}

	// Translate to Suno submit format.
	sunoReq := &dto.SunoSubmitReq{
		GptDescriptionPrompt: prompt,
		MakeInstrumental:     musicReq.Instrumental,
	}

	// Map model name to Suno model version.
	switch musicReq.Model {
	case "suno-v4":
		sunoReq.Mv = "chirp-v4"
	case "suno-v3.5":
		sunoReq.Mv = "chirp-v3-5"
	default:
		sunoReq.Mv = "chirp-v3-0"
	}

	data, err := json.Marshal(sunoReq)
	if err != nil {
		return nil, fmt.Errorf("marshal suno request: %w", err)
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return provider.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = app.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}

	var sunoResp dto.TaskResponse[string]
	if err := json.Unmarshal(responseBody, &sunoResp); err != nil {
		taskErr = app.TaskErrorWrapper(err, "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}
	if !sunoResp.IsSuccess() {
		taskErr = app.TaskErrorWrapper(fmt.Errorf("%s", sunoResp.Message), sunoResp.Code, http.StatusInternalServerError)
		return
	}

	// Return standardized response with task_id to the client.
	out := map[string]any{
		"task_id": sunoResp.Data,
		"status":  "queued",
	}
	outBytes, _ := json.Marshal(out)

	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(outBytes)

	return sunoResp.Data, nil, nil
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string, forceHTTP1 ...bool) (*http.Response, error) {
	requestURL := fmt.Sprintf("%s/suno/fetch", baseURL)
	byteBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", requestURL, bytes.NewBuffer(byteBody))
	if err != nil {
		return nil, err
	}
	defer req.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := app.GetHttpClientFor(proxy, len(forceHTTP1) > 0 && forceHTTP1[0])
	if err != nil {
		return nil, fmt.Errorf("new proxy http client: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	// Parse Suno fetch response to extract status and audio URL.
	var resp dto.TaskResponse[any]
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal fetch response: %w", err)
	}

	ti := &relaycommon.TaskInfo{}

	dataMap, ok := resp.Data.(map[string]any)
	if !ok {
		return ti, nil
	}

	if status, ok := dataMap["status"].(string); ok {
		ti.Status = status
	}
	if progress, ok := dataMap["progress"].(string); ok {
		ti.Progress = progress
	}

	// Try to extract audio URL from nested Suno song data.
	if data, ok := dataMap["data"].(map[string]any); ok {
		if audioURL, ok := data["audio_url"].(string); ok && audioURL != "" {
			ti.Url = audioURL
		}
	}

	return ti, nil
}
