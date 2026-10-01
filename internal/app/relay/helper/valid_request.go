package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func GetAndValidateRequest(c *gin.Context, format types.RelayFormat) (request dto.Request, err error) {
	relayMode := relayconstant.Path2RelayMode(c.Request.URL.Path)

	switch format {
	case types.RelayFormatOpenAI:
		request, err = GetAndValidateTextRequest(c, relayMode)
	case types.RelayFormatGemini:
		if strings.Contains(c.Request.URL.Path, ":embedContent") {
			request, err = GetAndValidateGeminiEmbeddingRequest(c)
		} else if strings.Contains(c.Request.URL.Path, ":batchEmbedContents") {
			request, err = GetAndValidateGeminiBatchEmbeddingRequest(c)
		} else {
			request, err = GetAndValidateGeminiRequest(c)
		}
	case types.RelayFormatClaude:
		request, err = GetAndValidateClaudeRequest(c)
	case types.RelayFormatOpenAIResponses:
		request, err = GetAndValidateResponsesRequest(c)
	case types.RelayFormatOpenAIResponsesCompact:
		request, err = GetAndValidateResponsesCompactionRequest(c)

	case types.RelayFormatOpenAIImage:
		request, err = GetAndValidOpenAIImageRequest(c, relayMode)
	case types.RelayFormatEmbedding:
		request, err = GetAndValidateEmbeddingRequest(c, relayMode)
	case types.RelayFormatRerank:
		request, err = GetAndValidateRerankRequest(c)
	case types.RelayFormatSystemOne:
		request, err = GetAndValidateSystemOneRequest(c)
	case types.RelayFormatOpenAIAudio:
		request, err = GetAndValidAudioRequest(c, relayMode)
	case types.RelayFormatOpenAIRealtime:
		request = &dto.BaseRequest{}
	default:
		return nil, fmt.Errorf("unsupported relay format: %s", format)
	}
	return request, err
}

func GetAndValidAudioRequest(c *gin.Context, relayMode int) (*dto.AudioRequest, error) {
	audioRequest := &dto.AudioRequest{}
	err := common.UnmarshalBodyReusable(c, audioRequest)
	if err != nil {
		return nil, err
	}
	switch relayMode {
	case relayconstant.RelayModeAudioSpeech:
		if audioRequest.Model == "" {
			return nil, errors.New("model is required")
		}
	default:
		if audioRequest.Model == "" {
			return nil, errors.New("model is required")
		}
		if audioRequest.ResponseFormat == "" {
			audioRequest.ResponseFormat = "json"
		}
	}
	return audioRequest, nil
}

func GetAndValidateRerankRequest(c *gin.Context) (*dto.RerankRequest, error) {
	var rerankRequest *dto.RerankRequest
	err := common.UnmarshalBodyReusable(c, &rerankRequest)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("getAndValidateTextRequest failed: %s", err.Error()))
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	if rerankRequest.Query == "" {
		return nil, types.NewError(fmt.Errorf("query is empty"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if len(rerankRequest.Documents) == 0 {
		return nil, types.NewError(fmt.Errorf("documents is empty"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	return rerankRequest, nil
}

func GetAndValidateEmbeddingRequest(c *gin.Context, relayMode int) (*dto.EmbeddingRequest, error) {
	var embeddingRequest *dto.EmbeddingRequest
	err := common.UnmarshalBodyReusable(c, &embeddingRequest)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("getAndValidateTextRequest failed: %s", err.Error()))
		return nil, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	if embeddingRequest.Input == nil {
		return nil, fmt.Errorf("input is empty")
	}
	if relayMode == relayconstant.RelayModeModerations && embeddingRequest.Model == "" {
		embeddingRequest.Model = "omni-moderation-latest"
	}
	if relayMode == relayconstant.RelayModeEmbeddings && embeddingRequest.Model == "" {
		embeddingRequest.Model = c.Param("model")
	}
	return embeddingRequest, nil
}

func GetAndValidateResponsesRequest(c *gin.Context) (*dto.OpenAIResponsesRequest, error) {
	request := &dto.OpenAIResponsesRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if request.Model == "" {
		return nil, errors.New("model is required")
	}
	if request.Input == nil {
		return nil, errors.New("input is required")
	}
	return request, nil
}

// GetAndValidateResponsesCompactionRequest decodes into the compact DTO —
// the documented field subset for POST /v1/responses/compact (cycle-8 L6,
// wire-formats-03). Model/Input validation mirrors GetAndValidateResponsesRequest;
// the subset gate itself (channel-type allow-list) runs later, in
// relay.ResponsesHelper, once a channel has been selected.
func GetAndValidateResponsesCompactionRequest(c *gin.Context) (*dto.OpenAIResponsesCompactionRequest, error) {
	request := &dto.OpenAIResponsesCompactionRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if request.Model == "" {
		return nil, errors.New("model is required")
	}
	if request.Input == nil {
		return nil, errors.New("input is required")
	}
	return request, nil
}

func GetAndValidOpenAIImageRequest(c *gin.Context, relayMode int) (*dto.ImageRequest, error) {
	imageRequest := &dto.ImageRequest{}

	switch relayMode {
	case relayconstant.RelayModeImagesEdits:
		if strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
			_, err := c.MultipartForm()
			if err != nil {
				return nil, fmt.Errorf("failed to parse image edit form request: %w", err)
			}
			formData := c.Request.PostForm
			imageRequest.Prompt = formData.Get("prompt")
			imageRequest.Model = formData.Get("model")
			imageRequest.N = uint(common.String2Int(formData.Get("n")))
			imageRequest.Quality = formData.Get("quality")
			imageRequest.Size = formData.Get("size")
			if imageValue := formData.Get("image"); imageValue != "" {
				imageRequest.Image, _ = json.Marshal(imageValue)
			}

			if imageRequest.Model == "gpt-image-1" {
				if imageRequest.Quality == "" {
					imageRequest.Quality = "standard"
				}
			}
			if imageRequest.N == 0 {
				imageRequest.N = 1
			}

			hasWatermark := formData.Has("watermark")
			if hasWatermark {
				watermark := formData.Get("watermark") == "true"
				imageRequest.Watermark = &watermark
			}
			break
		}
		fallthrough
	default:
		err := common.UnmarshalBodyReusable(c, imageRequest)
		if err != nil {
			return nil, err
		}

		if imageRequest.Model == "" {
			//imageRequest.Model = "dall-e-3"
			return nil, errors.New("model is required")
		}

		if strings.Contains(imageRequest.Size, "×") {
			return nil, errors.New("size an unexpected error occurred in the parameter, please use 'x' instead of the multiplication sign '×'")
		}

		// Not "256x256", "512x512", or "1024x1024"
		if imageRequest.Model == "dall-e-2" || imageRequest.Model == "dall-e" {
			if imageRequest.Size != "" && imageRequest.Size != "256x256" && imageRequest.Size != "512x512" && imageRequest.Size != "1024x1024" {
				return nil, errors.New("size must be one of 256x256, 512x512, or 1024x1024 for dall-e-2 or dall-e")
			}
			if imageRequest.Size == "" {
				imageRequest.Size = "1024x1024"
			}
		} else if imageRequest.Model == "dall-e-3" {
			if imageRequest.Size != "" && imageRequest.Size != "1024x1024" && imageRequest.Size != "1024x1792" && imageRequest.Size != "1792x1024" {
				return nil, errors.New("size must be one of 1024x1024, 1024x1792 or 1792x1024 for dall-e-3")
			}
			if imageRequest.Quality == "" {
				imageRequest.Quality = "standard"
			}
			if imageRequest.Size == "" {
				imageRequest.Size = "1024x1024"
			}
		} else if imageRequest.Model == "gpt-image-1" {
			if imageRequest.Quality == "" {
				imageRequest.Quality = "auto"
			}
		}

		//if imageRequest.Prompt == "" {
		//	return nil, errors.New("prompt is required")
		//}

		if imageRequest.N == 0 {
			imageRequest.N = 1
		}
	}

	// Bound the billing multiplier on BOTH intake paths (multipart form above
	// and JSON body) — see dto.MaxImageN for why an unbounded n is a charge
	// problem even though n is unsigned.
	if imageRequest.N > dto.MaxImageN {
		return nil, fmt.Errorf("n must be an integer between 1 and %d", dto.MaxImageN)
	}

	return imageRequest, nil
}

func GetAndValidateClaudeRequest(c *gin.Context) (textRequest *dto.ClaudeRequest, err error) {
	textRequest = &dto.ClaudeRequest{}
	err = c.ShouldBindJSON(textRequest)
	if err != nil {
		return nil, err
	}
	if textRequest.Messages == nil || len(textRequest.Messages) == 0 {
		return nil, errors.New("field messages is required")
	}
	if textRequest.Model == "" {
		return nil, errors.New("field model is required")
	}

	//if textRequest.Stream {
	//	relayInfo.IsStream = true
	//}

	return textRequest, nil
}

func GetAndValidateTextRequest(c *gin.Context, relayMode int) (*dto.GeneralOpenAIRequest, error) {
	textRequest := &dto.GeneralOpenAIRequest{}
	err := common.UnmarshalBodyReusable(c, textRequest)
	if err != nil {
		return nil, err
	}

	if relayMode == relayconstant.RelayModeModerations && textRequest.Model == "" {
		textRequest.Model = "text-moderation-latest"
	}
	if relayMode == relayconstant.RelayModeEmbeddings && textRequest.Model == "" {
		textRequest.Model = c.Param("model")
	}

	if textRequest.MaxTokens > math.MaxInt32/2 {
		return nil, errors.New("max_tokens is invalid")
	}
	if textRequest.Model == "" {
		return nil, errors.New("model is required")
	}
	if textRequest.WebSearchOptions != nil {
		if textRequest.WebSearchOptions.SearchContextSize != "" {
			validSizes := map[string]bool{
				"high":   true,
				"medium": true,
				"low":    true,
			}
			if !validSizes[textRequest.WebSearchOptions.SearchContextSize] {
				return nil, errors.New("invalid search_context_size, must be one of: high, medium, low")
			}
		} else {
			textRequest.WebSearchOptions.SearchContextSize = "medium"
		}
	}
	switch relayMode {
	case relayconstant.RelayModeCompletions:
		if textRequest.Prompt == "" {
			return nil, errors.New("field prompt is required")
		}
	case relayconstant.RelayModeChatCompletions:
		// For FIM (Fill-in-the-middle) requests with prefix/suffix, messages is optional
		// It will be filled by provider-specific adaptors if needed (e.g., SiliconFlow)。Or it is allowed by model vendor(s) (e.g., DeepSeek)
		if len(textRequest.Messages) == 0 && textRequest.Prefix == nil && textRequest.Suffix == nil {
			return nil, errors.New("field messages is required")
		}
	case relayconstant.RelayModeEmbeddings:
	case relayconstant.RelayModeModerations:
		if textRequest.Input == nil || textRequest.Input == "" {
			return nil, errors.New("field input is required")
		}
	case relayconstant.RelayModeEdits:
		if textRequest.Instruction == "" {
			return nil, errors.New("field instruction is required")
		}
	}
	return textRequest, nil
}

func GetAndValidateGeminiRequest(c *gin.Context) (*dto.GeminiChatRequest, error) {
	request := &dto.GeminiChatRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	if len(request.Contents) == 0 && len(request.Requests) == 0 {
		return nil, errors.New("contents is required")
	}

	//if c.Query("alt") == "sse" {
	//	relayInfo.IsStream = true
	//}

	return request, nil
}

func GetAndValidateGeminiEmbeddingRequest(c *gin.Context) (*dto.GeminiEmbeddingRequest, error) {
	request := &dto.GeminiEmbeddingRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	return request, nil
}

func GetAndValidateGeminiBatchEmbeddingRequest(c *gin.Context) (*dto.GeminiBatchEmbeddingRequest, error) {
	request := &dto.GeminiBatchEmbeddingRequest{}
	err := common.UnmarshalBodyReusable(c, request)
	if err != nil {
		return nil, err
	}
	return request, nil
}

// System One request caps. Hub-side limits, tighter than a self-hosted
// server's, so one request cannot demand unbounded estimation work.
const (
	systemOneMaxQuestions     = 64
	systemOneMaxChoiceOptions = 255
	systemOneMaxScoreLevels   = 10
	systemOneMinScoreLevels   = 2
)

// systemOneRefusedFields are top-level fields this endpoint never accepts:
// the batch field (no batch endpoint) and the prediction-hook arguments
// (a hook is a callable that has to live where the server runs).
var systemOneRefusedFields = []string{
	"states", "hooks", "on_predict_start", "on_predict_end", "hooks_raise", "hooks_timeout",
}

// GetAndValidateSystemOneRequest checks the body of POST /v1/systemone. Every
// failure is a plain error so Relay answers 400 (a typed error would default
// to 500); only a read failure is passed through unwrapped so an oversized
// body still maps to 413.
func GetAndValidateSystemOneRequest(c *gin.Context) (*dto.SystemOneRequest, error) {
	body, err := common.GetRequestBody(c)
	if err != nil {
		return nil, err
	}
	var top map[string]json.RawMessage
	if err := common.Unmarshal(body, &top); err != nil {
		return nil, fmt.Errorf("invalid request body: %w", err)
	}
	for _, field := range systemOneRefusedFields {
		if _, ok := top[field]; ok {
			return nil, fmt.Errorf("field %q is not supported on this endpoint", field)
		}
	}
	if err := checkSystemOneQuestionShapes(top["questions"]); err != nil {
		return nil, err
	}
	req := &dto.SystemOneRequest{}
	if err := common.Unmarshal(body, req); err != nil {
		return nil, fmt.Errorf("invalid request body: %w", err)
	}
	if req.Model == "" {
		return nil, errors.New("model is required")
	}
	if len(req.State) == 0 || string(req.State) == "null" {
		return nil, errors.New("'state' is required")
	}
	if len(req.Questions) == 0 {
		return nil, errors.New("'questions' must be a non-empty object")
	}
	if len(req.Questions) > systemOneMaxQuestions {
		return nil, fmt.Errorf("too many questions: at most %d", systemOneMaxQuestions)
	}
	for id, q := range req.Questions {
		if err := validateSystemOneQuestion(id, q); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func validateSystemOneQuestion(id string, q dto.SystemOneQuestion) error {
	switch q.Type {
	case "choice", "score", "noul":
	case "":
		return fmt.Errorf("question '%s': type is required; use one of ['choice', 'noul', 'score']", id)
	default:
		return fmt.Errorf("question '%s': unknown type '%s'; use one of ['choice', 'noul', 'score']", id, q.Type)
	}
	if instr := string(q.Instructions); instr == "" || instr == "null" || instr == `""` {
		return fmt.Errorf("question '%s': instructions is required", id)
	}
	switch q.Type {
	case "choice":
		n, ok := systemOneCriteriaSize(q.Criteria)
		if !ok || n == 0 {
			return fmt.Errorf("question '%s': a choice question takes 'criteria' as a non-empty object of label -> description, or a non-empty list of labels", id)
		}
		if n > systemOneMaxChoiceOptions {
			return fmt.Errorf("question '%s': at most %d choice options", id, systemOneMaxChoiceOptions)
		}
	case "score":
		var levels []json.RawMessage
		if err := common.Unmarshal(q.Criteria, &levels); err != nil {
			return fmt.Errorf("question '%s': a score question takes 'criteria' as a list of level descriptions", id)
		}
		if len(levels) < systemOneMinScoreLevels || len(levels) > systemOneMaxScoreLevels {
			return fmt.Errorf("question '%s': a score question needs %d to %d levels", id, systemOneMinScoreLevels, systemOneMaxScoreLevels)
		}
	}
	return nil
}

// systemOneCriteriaSize counts a choice question's options; ok is false when
// criteria is neither an object nor a list.
func systemOneCriteriaSize(raw json.RawMessage) (n int, ok bool) {
	var asObject map[string]json.RawMessage
	if common.Unmarshal(raw, &asObject) == nil && asObject != nil {
		return len(asObject), true
	}
	var asList []json.RawMessage
	if common.Unmarshal(raw, &asList) == nil && asList != nil {
		return len(asList), true
	}
	return 0, false
}

// checkSystemOneQuestionShapes names the offending question when a value is
// not an object, which a typed unmarshal would only report as a Go type error.
func checkSystemOneQuestionShapes(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("'questions' is required")
	}
	var qs map[string]json.RawMessage
	if err := common.Unmarshal(raw, &qs); err != nil || qs == nil {
		return errors.New("'questions' must be an object")
	}
	for id, q := range qs {
		if !strings.HasPrefix(strings.TrimSpace(string(q)), "{") {
			return fmt.Errorf("question '%s' must be an object", id)
		}
	}
	return nil
}
