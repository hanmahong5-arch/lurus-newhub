package relay

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/ali"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/aws"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/baidu"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/baidu_v2"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/claude"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/cloudflare"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/cohere"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/coze"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/deepseek"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/dify"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/gemini"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/jimeng"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/jina"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/minimax"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/mistral"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/mokaai"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/moonshot"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/ollama"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/openai"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/palm"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/perplexity"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/replicate"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/siliconflow"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/submodel"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/systemone"
	taskali "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/ali"
	taskdoubao "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/doubao"
	taskGemini "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/gemini"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/task/hailuo"
	taskjimeng "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/jimeng"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/task/kling"
	taskmusic "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/music"
	tasksora "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/sora"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/task/suno"
	taskvertex "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/vertex"
	taskVidu "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/vidu"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/tencent"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/vertex"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/volcengine"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/xai"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/xunfei"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/zhipu"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/zhipu_4v"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// TestGetAdaptor_DispatchTable asserts that every apiType maps to the intended
// provider adaptor concrete type. This is the provider-dispatch switch — sending
// a request through the wrong adaptor would serialize a tenant's request into a
// foreign vendor wire format, so the exact concrete type per apiType is a
// correctness contract, not cosmetic.
func TestGetAdaptor_DispatchTable(t *testing.T) {
	cases := []struct {
		apiType int
		want    provider.Adaptor
	}{
		{constant.APITypeAli, &ali.Adaptor{}},
		{constant.APITypeAnthropic, &claude.Adaptor{}},
		{constant.APITypeBaidu, &baidu.Adaptor{}},
		{constant.APITypeGemini, &gemini.Adaptor{}},
		{constant.APITypeOpenAI, &openai.Adaptor{}},
		{constant.APITypePaLM, &palm.Adaptor{}},
		{constant.APITypeTencent, &tencent.Adaptor{}},
		{constant.APITypeXunfei, &xunfei.Adaptor{}},
		{constant.APITypeZhipu, &zhipu.Adaptor{}},
		{constant.APITypeZhipuV4, &zhipu_4v.Adaptor{}},
		{constant.APITypeOllama, &ollama.Adaptor{}},
		{constant.APITypePerplexity, &perplexity.Adaptor{}},
		{constant.APITypeAws, &aws.Adaptor{}},
		{constant.APITypeCohere, &cohere.Adaptor{}},
		{constant.APITypeDify, &dify.Adaptor{}},
		{constant.APITypeJina, &jina.Adaptor{}},
		{constant.APITypeCloudflare, &cloudflare.Adaptor{}},
		{constant.APITypeSiliconFlow, &siliconflow.Adaptor{}},
		{constant.APITypeVertexAi, &vertex.Adaptor{}},
		{constant.APITypeMistral, &mistral.Adaptor{}},
		{constant.APITypeDeepSeek, &deepseek.Adaptor{}},
		{constant.APITypeMokaAI, &mokaai.Adaptor{}},
		{constant.APITypeVolcEngine, &volcengine.Adaptor{}},
		{constant.APITypeBaiduV2, &baidu_v2.Adaptor{}},
		// OpenRouter and Xinference deliberately reuse the OpenAI wire format.
		{constant.APITypeOpenRouter, &openai.Adaptor{}},
		{constant.APITypeXinference, &openai.Adaptor{}},
		{constant.APITypeXai, &xai.Adaptor{}},
		{constant.APITypeCoze, &coze.Adaptor{}},
		{constant.APITypeJimeng, &jimeng.Adaptor{}},
		{constant.APITypeMoonshot, &moonshot.Adaptor{}},
		{constant.APITypeSubmodel, &submodel.Adaptor{}},
		{constant.APITypeMiniMax, &minimax.Adaptor{}},
		{constant.APITypeReplicate, &replicate.Adaptor{}},
		{constant.APITypeSystemOne, &systemone.Adaptor{}},
	}

	seen := map[int]bool{}
	for _, tc := range cases {
		if seen[tc.apiType] {
			t.Fatalf("duplicate apiType %d in test table", tc.apiType)
		}
		seen[tc.apiType] = true

		got := GetAdaptor(tc.apiType)
		if got == nil {
			t.Errorf("GetAdaptor(%d) = nil, want %T", tc.apiType, tc.want)
			continue
		}
		if reflect.TypeOf(got) != reflect.TypeOf(tc.want) {
			t.Errorf("GetAdaptor(%d) = %T, want %T", tc.apiType, got, tc.want)
		}
	}
}

// TestGetAdaptor_UnknownReturnsNil proves the switch's default arm returns nil
// so callers surface an "invalid api type" error rather than dispatching to a
// zero-value/wrong adaptor.
func TestGetAdaptor_UnknownReturnsNil(t *testing.T) {
	for _, apiType := range []int{-1, constant.APITypeDummy, constant.APITypeDummy + 100, 9999} {
		if got := GetAdaptor(apiType); got != nil {
			t.Errorf("GetAdaptor(%d) = %T, want nil", apiType, got)
		}
	}
}

// TestGetAdaptor_AIProxyLibraryUnmapped documents that APITypeAIProxyLibrary has
// no arm in the switch and therefore falls through to nil.
func TestGetAdaptor_AIProxyLibraryUnmapped(t *testing.T) {
	if got := GetAdaptor(constant.APITypeAIProxyLibrary); got != nil {
		t.Errorf("GetAdaptor(AIProxyLibrary) = %T, want nil", got)
	}
}

// TestGetTaskAdaptor_DispatchTable asserts the async-task platform dispatch. The
// task adaptor governs how customers' long-running video/music/image jobs are
// submitted and polled; a wrong mapping routes a poll to the wrong vendor API.
func TestGetTaskAdaptor_DispatchTable(t *testing.T) {
	// String-platform cases.
	if got := GetTaskAdaptor(constant.TaskPlatformSuno); reflect.TypeOf(got) != reflect.TypeOf(&suno.TaskAdaptor{}) {
		t.Errorf("suno => %T, want *suno.TaskAdaptor", got)
	}
	if got := GetTaskAdaptor(constant.TaskPlatformMusic); reflect.TypeOf(got) != reflect.TypeOf(&taskmusic.TaskAdaptor{}) {
		t.Errorf("music => %T, want *music.TaskAdaptor", got)
	}

	// Numeric channel-type platforms (submitted as decimal strings).
	numeric := []struct {
		channelType int
		want        provider.TaskAdaptor
	}{
		{constant.ChannelTypeAli, &taskali.TaskAdaptor{}},
		{constant.ChannelTypeKling, &kling.TaskAdaptor{}},
		{constant.ChannelTypeJimeng, &taskjimeng.TaskAdaptor{}},
		{constant.ChannelTypeVertexAi, &taskvertex.TaskAdaptor{}},
		{constant.ChannelTypeVidu, &taskVidu.TaskAdaptor{}},
		{constant.ChannelTypeDoubaoVideo, &taskdoubao.TaskAdaptor{}},
		{constant.ChannelTypeSora, &tasksora.TaskAdaptor{}},
		{constant.ChannelTypeOpenAI, &tasksora.TaskAdaptor{}},
		{constant.ChannelTypeGemini, &taskGemini.TaskAdaptor{}},
		{constant.ChannelTypeMiniMax, &hailuo.TaskAdaptor{}},
	}
	for _, tc := range numeric {
		platform := constant.TaskPlatform(strconv.Itoa(tc.channelType))
		got := GetTaskAdaptor(platform)
		if got == nil {
			t.Errorf("GetTaskAdaptor(%q) = nil, want %T", platform, tc.want)
			continue
		}
		if reflect.TypeOf(got) != reflect.TypeOf(tc.want) {
			t.Errorf("GetTaskAdaptor(%q) = %T, want %T", platform, got, tc.want)
		}
	}
}

// TestGetTaskAdaptor_UnknownReturnsNil covers both the non-numeric unknown
// string arm and the numeric-but-unmapped channel-type arm.
func TestGetTaskAdaptor_UnknownReturnsNil(t *testing.T) {
	if got := GetTaskAdaptor(constant.TaskPlatform("definitely-not-a-platform")); got != nil {
		t.Errorf("unknown string platform => %T, want nil", got)
	}
	// Numeric but not in the channel-type switch (999 is unassigned).
	if got := GetTaskAdaptor(constant.TaskPlatform("999")); got != nil {
		t.Errorf("unmapped numeric platform => %T, want nil", got)
	}
}

// TestGetTaskPlatform prefers the numeric channel_type when present, otherwise
// falls back to the free-form platform string.
func TestGetTaskPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("channel_type wins when > 0", func(t *testing.T) {
		c, _ := gin.CreateTestContext(nil)
		c.Set("channel_type", constant.ChannelTypeKling)
		c.Set("platform", "suno")
		if got := GetTaskPlatform(c); got != constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeKling)) {
			t.Errorf("GetTaskPlatform = %q, want %q", got, strconv.Itoa(constant.ChannelTypeKling))
		}
	})

	t.Run("falls back to platform string when channel_type absent", func(t *testing.T) {
		c, _ := gin.CreateTestContext(nil)
		c.Set("platform", "suno")
		if got := GetTaskPlatform(c); got != constant.TaskPlatformSuno {
			t.Errorf("GetTaskPlatform = %q, want suno", got)
		}
	})

	t.Run("empty when neither set", func(t *testing.T) {
		c, _ := gin.CreateTestContext(nil)
		if got := GetTaskPlatform(c); got != "" {
			t.Errorf("GetTaskPlatform = %q, want empty", got)
		}
	})
}
