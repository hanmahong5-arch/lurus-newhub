package relay

import (
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
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
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/voyage"
	taskali "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/ali"
	taskdoubao "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/doubao"
	taskGemini "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/gemini"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/task/hailuo"
	taskjimeng "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/jimeng"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/task/kling"
	tasksora "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/sora"
	taskmusic "github.com/LurusTech/lurus-hub/internal/adapter/provider/task/music"
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
	"github.com/gin-gonic/gin"
)

func GetAdaptor(apiType int) provider.Adaptor {
	switch apiType {
	case constant.APITypeAli:
		return &ali.Adaptor{}
	case constant.APITypeAnthropic:
		return &claude.Adaptor{}
	case constant.APITypeBaidu:
		return &baidu.Adaptor{}
	case constant.APITypeGemini:
		return &gemini.Adaptor{}
	case constant.APITypeOpenAI:
		return &openai.Adaptor{}
	case constant.APITypePaLM:
		return &palm.Adaptor{}
	case constant.APITypeTencent:
		return &tencent.Adaptor{}
	case constant.APITypeXunfei:
		return &xunfei.Adaptor{}
	case constant.APITypeZhipu:
		return &zhipu.Adaptor{}
	case constant.APITypeZhipuV4:
		return &zhipu_4v.Adaptor{}
	case constant.APITypeOllama:
		return &ollama.Adaptor{}
	case constant.APITypePerplexity:
		return &perplexity.Adaptor{}
	case constant.APITypeAws:
		return &aws.Adaptor{}
	case constant.APITypeCohere:
		return &cohere.Adaptor{}
	case constant.APITypeDify:
		return &dify.Adaptor{}
	case constant.APITypeJina:
		return &jina.Adaptor{}
	case constant.APITypeCloudflare:
		return &cloudflare.Adaptor{}
	case constant.APITypeSiliconFlow:
		return &siliconflow.Adaptor{}
	case constant.APITypeVertexAi:
		return &vertex.Adaptor{}
	case constant.APITypeMistral:
		return &mistral.Adaptor{}
	case constant.APITypeDeepSeek:
		return &deepseek.Adaptor{}
	case constant.APITypeMokaAI:
		return &mokaai.Adaptor{}
	case constant.APITypeVolcEngine:
		return &volcengine.Adaptor{}
	case constant.APITypeBaiduV2:
		return &baidu_v2.Adaptor{}
	case constant.APITypeOpenRouter:
		return &openai.Adaptor{}
	case constant.APITypeXinference:
		return &openai.Adaptor{}
	case constant.APITypeXai:
		return &xai.Adaptor{}
	case constant.APITypeCoze:
		return &coze.Adaptor{}
	case constant.APITypeJimeng:
		return &jimeng.Adaptor{}
	case constant.APITypeMoonshot:
		return &moonshot.Adaptor{} // Moonshot uses Claude API
	case constant.APITypeSubmodel:
		return &submodel.Adaptor{}
	case constant.APITypeMiniMax:
		return &minimax.Adaptor{}
	case constant.APITypeReplicate:
		return &replicate.Adaptor{}
	case constant.APITypeSystemOne:
		return &systemone.Adaptor{}
	case constant.APITypeVoyage:
		return &voyage.Adaptor{}
	}
	return nil
}

func GetTaskPlatform(c *gin.Context) constant.TaskPlatform {
	channelType := c.GetInt("channel_type")
	if channelType > 0 {
		return constant.TaskPlatform(strconv.Itoa(channelType))
	}
	return constant.TaskPlatform(c.GetString("platform"))
}

func GetTaskAdaptor(platform constant.TaskPlatform) provider.TaskAdaptor {
	switch platform {
	//case constant.APITypeAIProxyLibrary:
	//	return &aiproxy.Adaptor{}
	case constant.TaskPlatformSuno:
		return &suno.TaskAdaptor{}
	case constant.TaskPlatformMusic:
		return &taskmusic.TaskAdaptor{}
	}
	if channelType, err := strconv.ParseInt(string(platform), 10, 64); err == nil {
		switch channelType {
		case constant.ChannelTypeAli:
			return &taskali.TaskAdaptor{}
		case constant.ChannelTypeKling:
			return &kling.TaskAdaptor{}
		case constant.ChannelTypeJimeng:
			return &taskjimeng.TaskAdaptor{}
		case constant.ChannelTypeVertexAi:
			return &taskvertex.TaskAdaptor{}
		case constant.ChannelTypeVidu:
			return &taskVidu.TaskAdaptor{}
		case constant.ChannelTypeDoubaoVideo:
			return &taskdoubao.TaskAdaptor{}
		case constant.ChannelTypeSora, constant.ChannelTypeOpenAI:
			return &tasksora.TaskAdaptor{}
		case constant.ChannelTypeGemini:
			return &taskGemini.TaskAdaptor{}
		case constant.ChannelTypeMiniMax:
			return &hailuo.TaskAdaptor{}
		}
	}
	return nil
}
