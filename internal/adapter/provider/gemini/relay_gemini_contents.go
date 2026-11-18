package gemini

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

func appendToolFunctionResponse(geminiRequest *dto.GeminiChatRequest, message dto.Message, toolCallIDs map[string]string) {
	if len(geminiRequest.Contents) == 0 || geminiRequest.Contents[len(geminiRequest.Contents)-1].Role == "model" {
		geminiRequest.Contents = append(geminiRequest.Contents, dto.GeminiChatContent{
			Role: "user",
		})
	}
	parts := &geminiRequest.Contents[len(geminiRequest.Contents)-1].Parts
	name := ""
	if message.Name != nil {
		name = *message.Name
	} else if val, exists := toolCallIDs[message.ToolCallId]; exists {
		name = val
	}
	var contentMap map[string]interface{}
	contentStr := message.StringContent()

	if err := json.Unmarshal([]byte(contentStr), &contentMap); err != nil {
		var contentSlice []interface{}
		if err := json.Unmarshal([]byte(contentStr), &contentSlice); err == nil {
			contentMap = map[string]interface{}{"result": contentSlice}
		} else {
			contentMap = map[string]interface{}{"content": contentStr}
		}
	}

	functionResp := &dto.GeminiFunctionResponse{
		Name:     name,
		Response: contentMap,
	}

	*parts = append(*parts, dto.GeminiPart{
		FunctionResponse: functionResp,
	})
}

func convertOpenAIMessageToGemini(c *gin.Context, message dto.Message, attachThoughtSignature bool, toolCallIDs map[string]string) (dto.GeminiChatContent, bool, error) {
	var parts []dto.GeminiPart
	content := dto.GeminiChatContent{
		Role: message.Role,
	}
	shouldAttachThoughtSignature := attachThoughtSignature && (message.Role == "assistant" || message.Role == "model")
	signatureAttached := false

	if message.ToolCalls != nil {
		for _, call := range message.ParseToolCalls() {
			args := map[string]interface{}{}
			if call.Function.Arguments != "" {
				if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil {
					return content, false, fmt.Errorf("invalid arguments for function %s, args: %s", call.Function.Name, call.Function.Arguments)
				}
			}
			toolCall := dto.GeminiPart{
				FunctionCall: &dto.FunctionCall{
					FunctionName: call.Function.Name,
					Arguments:    args,
				},
			}
			if shouldAttachThoughtSignature && !signatureAttached && hasFunctionCallContent(toolCall.FunctionCall) && len(toolCall.ThoughtSignature) == 0 {
				toolCall.ThoughtSignature = json.RawMessage(strconv.Quote(thoughtSignatureBypassValue))
				signatureAttached = true
			}
			parts = append(parts, toolCall)
			toolCallIDs[call.ID] = call.Function.Name
		}
	}

	openaiContent := message.ParseContent()
	imageNum := 0
	for _, part := range openaiContent {
		var err error
		parts, imageNum, err = appendOpenAIContentPart(c, parts, part, imageNum, shouldAttachThoughtSignature)
		if err != nil {
			return content, false, err
		}
	}

	if shouldAttachThoughtSignature && !signatureAttached && len(parts) > 0 {
		for i := range parts {
			if parts[i].Text != "" {
				parts[i].ThoughtSignature = json.RawMessage(strconv.Quote(thoughtSignatureBypassValue))
				break
			}
		}
	}

	content.Parts = parts
	if content.Role == "assistant" {
		content.Role = "model"
	}
	return content, len(content.Parts) > 0, nil
}

func appendOpenAIContentPart(c *gin.Context, parts []dto.GeminiPart, part dto.MediaContent, imageNum int, shouldAttachThoughtSignature bool) ([]dto.GeminiPart, int, error) {
	switch part.Type {
	case dto.ContentTypeText:
		if part.Text == "" {
			return parts, imageNum, nil
		}
		return appendMarkdownTextOrImages(parts, part.Text, imageNum, shouldAttachThoughtSignature)
	case dto.ContentTypeImageURL:
		imageNum++
		if constant.GeminiVisionMaxImageNum != -1 && imageNum > constant.GeminiVisionMaxImageNum {
			return nil, imageNum, fmt.Errorf("too many images in the message, max allowed is %d", constant.GeminiVisionMaxImageNum)
		}
		if strings.HasPrefix(part.GetImageMedia().Url, "http") {
			fileData, err := app.GetFileBase64FromUrl(c, part.GetImageMedia().Url, "formatting image for Gemini")
			if err != nil {
				return nil, imageNum, fmt.Errorf("get file base64 from url '%s' failed: %w", part.GetImageMedia().Url, err)
			}
			if _, ok := geminiSupportedMimeTypes[strings.ToLower(fileData.MimeType)]; !ok {
				url := part.GetImageMedia().Url
				return nil, imageNum, fmt.Errorf("mime type is not supported by Gemini: '%s', url: '%s', supported types are: %v", fileData.MimeType, url, getSupportedMimeTypesList())
			}
			parts = append(parts, dto.GeminiPart{
				InlineData: &dto.GeminiInlineData{
					MimeType: fileData.MimeType,
					Data:     fileData.Base64Data,
				},
			})
			return parts, imageNum, nil
		}
		format, base64String, err := app.DecodeBase64FileData(part.GetImageMedia().Url)
		if err != nil {
			return nil, imageNum, fmt.Errorf("decode base64 image data failed: %s", err.Error())
		}
		parts = append(parts, dto.GeminiPart{
			InlineData: &dto.GeminiInlineData{
				MimeType: format,
				Data:     base64String,
			},
		})
	case dto.ContentTypeFile:
		if part.GetFile().FileId != "" {
			return nil, imageNum, fmt.Errorf("only base64 file is supported in gemini")
		}
		format, base64String, err := app.DecodeBase64FileData(part.GetFile().FileData)
		if err != nil {
			return nil, imageNum, fmt.Errorf("decode base64 file data failed: %s", err.Error())
		}
		parts = append(parts, dto.GeminiPart{
			InlineData: &dto.GeminiInlineData{
				MimeType: format,
				Data:     base64String,
			},
		})
	case dto.ContentTypeInputAudio:
		if part.GetInputAudio().Data == "" {
			return nil, imageNum, fmt.Errorf("only base64 audio is supported in gemini")
		}
		base64String, err := app.DecodeBase64AudioData(part.GetInputAudio().Data)
		if err != nil {
			return nil, imageNum, fmt.Errorf("decode base64 audio data failed: %s", err.Error())
		}
		parts = append(parts, dto.GeminiPart{
			InlineData: &dto.GeminiInlineData{
				MimeType: "audio/" + part.GetInputAudio().Format,
				Data:     base64String,
			},
		})
	}
	return parts, imageNum, nil
}

func appendMarkdownTextOrImages(parts []dto.GeminiPart, rawText string, imageNum int, shouldAttachThoughtSignature bool) ([]dto.GeminiPart, int, error) {
	text := rawText
	hasMarkdownImage := false
	for {
		startIdx := strings.Index(text, "![")
		if startIdx == -1 {
			break
		}
		bracketIdx := strings.Index(text[startIdx:], "](data:")
		if bracketIdx == -1 {
			break
		}
		bracketIdx += startIdx
		closeIdx := strings.Index(text[bracketIdx+2:], ")")
		if closeIdx == -1 {
			break
		}
		closeIdx += bracketIdx + 2

		hasMarkdownImage = true
		if startIdx > 0 {
			textBefore := text[:startIdx]
			if textBefore != "" {
				parts = append(parts, dto.GeminiPart{
					Text: textBefore,
				})
			}
		}
		dataUrl := text[bracketIdx+2 : closeIdx]
		imageNum++
		if constant.GeminiVisionMaxImageNum != -1 && imageNum > constant.GeminiVisionMaxImageNum {
			return nil, imageNum, fmt.Errorf("too many images in the message, max allowed is %d", constant.GeminiVisionMaxImageNum)
		}
		format, base64String, err := app.DecodeBase64FileData(dataUrl)
		if err != nil {
			return nil, imageNum, fmt.Errorf("decode markdown base64 image data failed: %s", err.Error())
		}
		imgPart := dto.GeminiPart{
			InlineData: &dto.GeminiInlineData{
				MimeType: format,
				Data:     base64String,
			},
		}
		if shouldAttachThoughtSignature {
			imgPart.ThoughtSignature = json.RawMessage(strconv.Quote(thoughtSignatureBypassValue))
		}
		parts = append(parts, imgPart)
		text = text[closeIdx+1:]
	}
	if !hasMarkdownImage {
		parts = append(parts, dto.GeminiPart{
			Text: rawText,
		})
	} else if text != "" {
		parts = append(parts, dto.GeminiPart{
			Text: text,
		})
	}
	return parts, imageNum, nil
}
