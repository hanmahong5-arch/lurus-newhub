package dify

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func fixRemoteImageContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

// 回归：user 消息里携带远端（http/https）image_url 时，必须构造出
// transfer_mode=remote_url 的 DifyFile，而不是向 nil 指针写字段把请求打崩。
// 远端分支不会上传文件，因此本用例不触网。
func TestFixDifyRequestOpenAI2Dify_RemoteImageBuildsFile(t *testing.T) {
	c := fixRemoteImageContext(t)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "http://dify.invalid",
			ApiKey:         "test-key",
		},
	}

	request := dto.GeneralOpenAIRequest{
		User: "fix-remote-image-user",
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					dto.MediaContent{Type: dto.ContentTypeText, Text: "describe this"},
					dto.MediaContent{
						Type: dto.ContentTypeImageURL,
						ImageUrl: &dto.MessageImageUrl{
							Url:      "https://example.invalid/cat.png",
							MimeType: "image/png",
						},
					},
				},
			},
		},
	}

	difyReq := requestOpenAI2Dify(c, info, request)

	if len(difyReq.Files) != 1 {
		t.Fatalf("expected exactly 1 file, got %d (%+v)", len(difyReq.Files), difyReq.Files)
	}
	file := difyReq.Files[0]
	if file.TransferMode != "remote_url" {
		t.Fatalf("expected transfer_mode=remote_url, got %q", file.TransferMode)
	}
	if file.URL != "https://example.invalid/cat.png" {
		t.Fatalf("expected the remote url to be carried over, got %q", file.URL)
	}
	if file.Type != "image/png" {
		t.Fatalf("expected type=image/png, got %q", file.Type)
	}
	if file.UploadFileId != "" {
		t.Fatalf("remote images must not carry an upload id, got %q", file.UploadFileId)
	}
	if difyReq.User != "fix-remote-image-user" {
		t.Fatalf("expected the request user to be preserved, got %q", difyReq.User)
	}
}
