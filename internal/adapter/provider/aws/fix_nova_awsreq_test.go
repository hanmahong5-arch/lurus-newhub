package aws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/gin-gonic/gin"
)

func fixNovaTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func fixNovaRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			// ak|sk|region 三段式 = ClientModeAKSK 凭证格式
			ApiKey:            "test-ak|test-sk|us-east-1",
			UpstreamModelName: "nova-lite-v1:0",
		},
	}
}

// 回归：AK/SK 模式下 Nova 分支必须把构造好的 InvokeModelInput 挂到 a.AwsReq，
// 否则 DoResponse 的 Nova 路径拿到 nil interface，类型断言直接 panic。
// 本用例不触网：doAwsClientRequest 只构造 client 和请求体，真正的 InvokeModel
// 调用发生在 DoResponse。
func TestFixAwsNovaDoRequestPersistsAwsReq(t *testing.T) {
	c := fixNovaTestContext(t)
	info := fixNovaRelayInfo()
	a := &Adaptor{ClientMode: ClientModeAKSK, IsNova: true}

	body := strings.NewReader(`{"schemaVersion":"messages-v1","messages":[{"role":"user","content":[{"text":"hi"}]}]}`)
	if _, err := a.DoRequest(c, info, body); err != nil {
		t.Fatalf("DoRequest returned an error: %v", err)
	}

	if a.AwsReq == nil {
		t.Fatal("a.AwsReq is nil: the Nova branch dropped the built request")
	}
	awsReq, ok := a.AwsReq.(*bedrockruntime.InvokeModelInput)
	if !ok {
		t.Fatalf("expected *bedrockruntime.InvokeModelInput, got %T", a.AwsReq)
	}
	if awsReq.ModelId == nil || *awsReq.ModelId == "" {
		t.Fatal("expected a non-empty ModelId on the persisted request")
	}
	if !strings.Contains(*awsReq.ModelId, "nova-") {
		t.Fatalf("expected a nova model id, got %q", *awsReq.ModelId)
	}
	if !strings.Contains(string(awsReq.Body), "messages-v1") {
		t.Fatalf("expected the marshaled nova body to be attached, got %q", string(awsReq.Body))
	}
}

// 回归：即便 a.AwsReq 未准备好，Nova 响应路径也应返回干净错误而不是 panic。
func TestFixAwsNovaHandlerWithoutPreparedRequestReturnsError(t *testing.T) {
	c := fixNovaTestContext(t)
	info := fixNovaRelayInfo()

	apiErr, usage := handleNovaRequest(c, info, &Adaptor{ClientMode: ClientModeAKSK, IsNova: true})
	if apiErr == nil {
		t.Fatal("expected an error when a.AwsReq was never prepared")
	}
	if usage != nil {
		t.Fatalf("expected nil usage, got %v", usage)
	}
}
