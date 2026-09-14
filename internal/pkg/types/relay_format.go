package types

type RelayFormat string

const (
	RelayFormatOpenAI          RelayFormat = "openai"
	RelayFormatClaude                      = "claude"
	RelayFormatGemini                      = "gemini"
	RelayFormatOpenAIResponses             = "openai_responses"
	// RelayFormatOpenAIResponsesCompact is POST /v1/responses/compact
	// (wire-formats-03): the documented field-subset pass-through of
	// /v1/responses. See dto.OpenAIResponsesCompactionRequest.
	RelayFormatOpenAIResponsesCompact = "openai_responses_compact"
	RelayFormatOpenAIAudio            = "openai_audio"
	RelayFormatOpenAIImage            = "openai_image"
	RelayFormatOpenAIRealtime         = "openai_realtime"
	RelayFormatRerank                 = "rerank"
	RelayFormatEmbedding              = "embedding"

	RelayFormatTask    = "task"
	RelayFormatMjProxy = "mj_proxy"
)
