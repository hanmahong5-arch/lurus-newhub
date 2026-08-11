package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

// A message may carry a name without any content. The name is still sent
// upstream, so it must be counted (3 tokens each) and included in the text the
// estimator hashes/scans — previously the whole name block sat inside the
// `if message.Content != nil` guard and was skipped.
func TestFixTokenCountMeta_NameCountedWithoutContent(t *testing.T) {
	name := "fix-name-only-caller"
	req := &GeneralOpenAIRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: "tool", Name: &name, Content: nil},
		},
	}

	meta := req.GetTokenCountMeta()

	if meta.NameCount != 1 {
		t.Fatalf("NameCount = %d, want 1 for a name-only message", meta.NameCount)
	}
	if !strings.Contains(meta.CombineText, name) {
		t.Errorf("CombineText %q does not contain the message name %q", meta.CombineText, name)
	}
}

// Guard for the untouched shape: name + content must still be counted once and
// keep the role/name/content ordering in CombineText.
func TestFixTokenCountMeta_NameStillCountedWithContent(t *testing.T) {
	name := "fix-name-with-content"
	req := &GeneralOpenAIRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: "user", Name: &name, Content: "hello fix"},
		},
	}

	meta := req.GetTokenCountMeta()

	if meta.NameCount != 1 {
		t.Fatalf("NameCount = %d, want 1", meta.NameCount)
	}
	nameIdx := strings.Index(meta.CombineText, name)
	contentIdx := strings.Index(meta.CombineText, "hello fix")
	if nameIdx < 0 || contentIdx < 0 {
		t.Fatalf("CombineText %q must contain both the name and the content", meta.CombineText)
	}
	if nameIdx > contentIdx {
		t.Errorf("CombineText %q: name must precede content", meta.CombineText)
	}
}

// The Responses API carries `detail` next to `image_url`; dropping it made every
// image estimate at high detail, over-counting the pre-consumed quota.
func TestFixResponsesInput_ImageDetailIsParsed(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "detail on item with string image_url",
			input: `[{"role":"user","content":[{"type":"input_image","image_url":"https://example.local/a.png","detail":"low"}]}]`,
			want:  "low",
		},
		{
			name:  "detail inside image_url object",
			input: `[{"role":"user","content":[{"type":"input_image","image_url":{"url":"https://example.local/a.png","detail":"low"}}]}]`,
			want:  "low",
		},
		{
			name:  "item level detail wins over nested",
			input: `[{"role":"user","content":[{"type":"input_image","image_url":{"url":"https://example.local/a.png","detail":"high"},"detail":"low"}]}]`,
			want:  "low",
		},
		{
			name:  "no detail stays empty",
			input: `[{"role":"user","content":[{"type":"input_image","image_url":"https://example.local/a.png"}]}]`,
			want:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &OpenAIResponsesRequest{Model: "gpt-4o", Input: json.RawMessage(tc.input)}

			inputs := req.ParseInput()
			if len(inputs) != 1 {
				t.Fatalf("ParseInput returned %d items, want 1", len(inputs))
			}
			if inputs[0].Detail != tc.want {
				t.Fatalf("MediaInput.Detail = %q, want %q", inputs[0].Detail, tc.want)
			}

			// The detail must reach the token-count metadata, which is what the
			// image estimate keys off.
			meta := req.GetTokenCountMeta()
			if len(meta.Files) != 1 {
				t.Fatalf("meta.Files = %d, want 1", len(meta.Files))
			}
			if meta.Files[0].Detail != tc.want {
				t.Errorf("FileMeta.Detail = %q, want %q", meta.Files[0].Detail, tc.want)
			}
		})
	}
}
