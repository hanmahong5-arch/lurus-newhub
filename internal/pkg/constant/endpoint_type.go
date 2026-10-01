package constant

type EndpointType string

const (
	EndpointTypeOpenAI          EndpointType = "openai"
	EndpointTypeOpenAIResponse  EndpointType = "openai-response"
	EndpointTypeAnthropic       EndpointType = "anthropic"
	EndpointTypeGemini          EndpointType = "gemini"
	EndpointTypeJinaRerank      EndpointType = "jina-rerank"
	EndpointTypeImageGeneration EndpointType = "image-generation"
	EndpointTypeEmbeddings      EndpointType = "embeddings"
	EndpointTypeOpenAIVideo     EndpointType = "openai-video"
	EndpointTypeSystemOne       EndpointType = "systemone"
	//EndpointTypeMidjourney     EndpointType = "midjourney-proxy"
	//EndpointTypeSuno           EndpointType = "suno-proxy"
	//EndpointTypeKling          EndpointType = "kling"
	//EndpointTypeJimeng         EndpointType = "jimeng"
)
