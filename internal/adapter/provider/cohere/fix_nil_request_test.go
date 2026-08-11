package cohere

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// 回归：ConvertOpenAIRequest 收到 nil 请求时必须返回业务错误，
// 而不是解引用 nil 指针把请求 goroutine 打崩（与其余 26 个适配器一致）。
func TestFixCohereConvertOpenAIRequest_NilRequestReturnsError(t *testing.T) {
	a := &Adaptor{}

	got, err := a.ConvertOpenAIRequest(nil, nil, nil)
	if err == nil {
		t.Fatalf("expected an error for a nil request, got nil (converted=%v)", got)
	}
	if got != nil {
		t.Fatalf("expected nil conversion result for a nil request, got %v", got)
	}
}

// 正常路径不受守卫影响。
func TestFixCohereConvertOpenAIRequest_NonNilRequestStillConverts(t *testing.T) {
	a := &Adaptor{}
	req := &dto.GeneralOpenAIRequest{
		Model: "command-r",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	}

	got, err := a.ConvertOpenAIRequest(nil, nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	converted, ok := got.(*CohereRequest)
	if !ok {
		t.Fatalf("expected *CohereRequest, got %T", got)
	}
	if converted.Model != "command-r" {
		t.Fatalf("expected model command-r, got %q", converted.Model)
	}
	if converted.Message != "hello" {
		t.Fatalf("expected message hello, got %q", converted.Message)
	}
}
