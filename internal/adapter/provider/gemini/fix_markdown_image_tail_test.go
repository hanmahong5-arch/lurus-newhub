package gemini

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// mdTailRelayInfo builds a minimal RelayInfo for the markdown-image conversion path.
func mdTailRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.0-flash"},
	}
}

// mdTailConvert runs CovertOpenAI2Gemini on a single user message and returns its parts.
func mdTailConvert(t *testing.T, content string) []dto.GeminiPart {
	t.Helper()
	// The image cap is normally loaded from env at boot; it is 0 in tests.
	origMax := constant.GeminiVisionMaxImageNum
	t.Cleanup(func() { constant.GeminiVisionMaxImageNum = origMax })
	constant.GeminiVisionMaxImageNum = 16

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req := dto.GeneralOpenAIRequest{
		Model: "gemini-2.0-flash",
		Messages: []dto.Message{
			{Role: "user", Content: content},
		},
	}
	got, err := CovertOpenAI2Gemini(c, req, mdTailRelayInfo())
	if err != nil {
		t.Fatalf("CovertOpenAI2Gemini returned error: %v", err)
	}
	if len(got.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(got.Contents))
	}
	return got.Contents[0].Parts
}

// TestCovertOpenAI2Gemini_MarkdownImage_KeepsTrailingText guards the text that
// follows the LAST inline markdown image: it used to be swallowed by the split
// loop (the leftover text was only emitted when no image was found at all), so
// trailing instructions never reached the upstream model.
func TestCovertOpenAI2Gemini_MarkdownImage_KeepsTrailingText(t *testing.T) {
	parts := mdTailConvert(t, "before ![pic](data:image/png;base64,aGVsbG8=) after")

	if len(parts) != 3 {
		t.Fatalf("expected 3 parts (text, image, trailing text), got %d: %+v", len(parts), parts)
	}
	if parts[0].Text != "before " {
		t.Errorf("parts[0].Text = %q, want %q", parts[0].Text, "before ")
	}
	if parts[1].InlineData == nil {
		t.Fatalf("parts[1] should carry inline image data, got %+v", parts[1])
	}
	if parts[1].InlineData.MimeType != "image/png" || parts[1].InlineData.Data != "aGVsbG8=" {
		t.Errorf("parts[1].InlineData = %+v, want mime image/png data aGVsbG8=", parts[1].InlineData)
	}
	if parts[2].Text != " after" {
		t.Errorf("parts[2].Text = %q, want %q", parts[2].Text, " after")
	}
}

// TestCovertOpenAI2Gemini_MarkdownImage_KeepsTailAfterEarlyBreak covers the
// early-break paths of the same loop: once a data-URL image was consumed, any
// remaining text that no longer matches "](data:" was dropped wholesale.
func TestCovertOpenAI2Gemini_MarkdownImage_KeepsTailAfterEarlyBreak(t *testing.T) {
	parts := mdTailConvert(t, "![a](data:image/png;base64,aGVsbG8=) mid ![b](http://example.com/x.png) tail")

	if len(parts) != 2 {
		t.Fatalf("expected 2 parts (image, remaining text), got %d: %+v", len(parts), parts)
	}
	if parts[0].InlineData == nil {
		t.Fatalf("parts[0] should carry inline image data, got %+v", parts[0])
	}
	want := " mid ![b](http://example.com/x.png) tail"
	if parts[1].Text != want {
		t.Errorf("parts[1].Text = %q, want %q", parts[1].Text, want)
	}
}

// TestCovertOpenAI2Gemini_MarkdownImage_NoRegressionOnPlainAndTrailingImage
// pins the two behaviours the fix must not change: plain text stays a single
// part, and an image at the very end must not produce an empty trailing part.
func TestCovertOpenAI2Gemini_MarkdownImage_NoRegressionOnPlainAndTrailingImage(t *testing.T) {
	plain := mdTailConvert(t, "no image here")
	if len(plain) != 1 || plain[0].Text != "no image here" {
		t.Fatalf("plain text: got %+v, want single text part", plain)
	}

	ending := mdTailConvert(t, "before ![pic](data:image/png;base64,aGVsbG8=)")
	if len(ending) != 2 {
		t.Fatalf("trailing image: expected 2 parts, got %d: %+v", len(ending), ending)
	}
	if ending[0].Text != "before " {
		t.Errorf("ending[0].Text = %q, want %q", ending[0].Text, "before ")
	}
	if ending[1].InlineData == nil {
		t.Errorf("ending[1] should carry inline image data, got %+v", ending[1])
	}
}
