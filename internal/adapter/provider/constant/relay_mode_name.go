package constant

// RelayModeLabel maps a relay mode to a short, stable, low-cardinality name
// used by analytics (rankings by=relay_mode) and the llm.usage.recorded event.
// The names are an external contract: add new ones, never rename. Modes that
// have no dedicated name (midjourney, suno, video, ...) share "other", and
// RelayModeUnknown (0, rows written before the column was populated) is
// "unknown".
func RelayModeLabel(mode int) string {
	switch mode {
	case RelayModeChatCompletions:
		return "chat"
	case RelayModeCompletions, RelayModeEdits:
		return "completions"
	case RelayModeEmbeddings:
		return "embeddings"
	case RelayModeModerations:
		return "moderations"
	case RelayModeImagesGenerations, RelayModeImagesEdits:
		return "images"
	case RelayModeAudioSpeech, RelayModeAudioTranscription, RelayModeAudioTranslation:
		return "audio"
	case RelayModeRerank:
		return "rerank"
	case RelayModeSystemOne:
		return "systemone"
	case RelayModeResponses, RelayModeResponsesCompact, RelayModeResponsesRetrieve, RelayModeResponsesDelete:
		return "responses"
	case RelayModeRealtime:
		return "realtime"
	case RelayModeGemini:
		return "gemini"
	case RelayModeUnknown:
		return "unknown"
	}
	return "other"
}

// RelayModeLabelledModes lists every mode that has its own label, so SQL CASE
// expressions can be generated from the same table as RelayModeLabel instead
// of a second copy of the integers.
var RelayModeLabelledModes = []int{
	RelayModeChatCompletions, RelayModeCompletions, RelayModeEdits, RelayModeEmbeddings,
	RelayModeModerations, RelayModeImagesGenerations, RelayModeImagesEdits,
	RelayModeAudioSpeech, RelayModeAudioTranscription, RelayModeAudioTranslation,
	RelayModeRerank, RelayModeSystemOne, RelayModeResponses, RelayModeResponsesCompact,
	RelayModeResponsesRetrieve, RelayModeResponsesDelete, RelayModeRealtime, RelayModeGemini,
	RelayModeUnknown,
}
