package fakeupstream

import (
	"net/http"
	"strings"
	"time"
)

// --- OpenAI chat completions ------------------------------------------------

func openAIUsage(u Usage) map[string]any {
	return map[string]any{
		"prompt_tokens":         u.PromptTokens,
		"completion_tokens":     u.CompletionTokens,
		"total_tokens":          u.PromptTokens + u.CompletionTokens,
		"prompt_tokens_details": map[string]any{"cached_tokens": u.CachedTokens},
	}
}

func openAIChunk(id, model string, delta map[string]any, finish any) map[string]any {
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
}

func (s *Server) serveOpenAIChat(c *call) {
	id := "chatcmpl-fake"
	if !c.stream {
		writeJSON(c.w, http.StatusOK, map[string]any{
			"id":      id,
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   c.model,
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": s.cfg.Reply},
				"finish_reason": "stop",
			}},
			"usage": openAIUsage(c.usage),
		})
		return
	}
	st := startSSE(c.w)
	if st.data(openAIChunk(id, c.model, map[string]any{"role": "assistant", "content": ""}, nil)) != nil {
		return
	}
	for _, word := range strings.SplitAfter(s.cfg.Reply, " ") {
		if st.data(openAIChunk(id, c.model, map[string]any{"content": word}, nil)) != nil {
			return
		}
	}
	if st.data(openAIChunk(id, c.model, map[string]any{}, "stop")) != nil {
		return
	}
	// Like the real API, usage is streamed only when the caller asked for it.
	if opts, _ := c.body["stream_options"].(map[string]any); opts != nil {
		if include, _ := opts["include_usage"].(bool); include {
			chunk := openAIChunk(id, c.model, nil, nil)
			chunk["choices"] = []any{}
			chunk["usage"] = openAIUsage(c.usage)
			if st.data(chunk) != nil {
				return
			}
		}
	}
	_ = st.data("[DONE]")
}

// --- OpenAI Responses -------------------------------------------------------

func (s *Server) responsesBody(model, status string, u Usage) map[string]any {
	return map[string]any{
		"id":         "resp_fake",
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     status,
		"model":      model,
		"output": []any{map[string]any{
			"type":    "message",
			"id":      "msg_fake",
			"status":  "completed",
			"role":    "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": s.cfg.Reply, "annotations": []any{}}},
		}},
		"usage": map[string]any{
			"input_tokens":          u.PromptTokens,
			"output_tokens":         u.CompletionTokens,
			"total_tokens":          u.PromptTokens + u.CompletionTokens,
			"input_tokens_details":  map[string]any{"cached_tokens": u.CachedTokens},
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
		},
	}
}

func (s *Server) serveResponses(c *call) {
	if !c.stream {
		writeJSON(c.w, http.StatusOK, s.responsesBody(c.model, "completed", c.usage))
		return
	}
	st := startSSE(c.w)
	created := s.responsesBody(c.model, "in_progress", Usage{})
	delete(created, "usage")
	created["output"] = []any{}
	if st.event("response.created", map[string]any{"type": "response.created", "response": created}) != nil {
		return
	}
	if st.event("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "msg_fake", "output_index": 0, "content_index": 0, "delta": s.cfg.Reply}) != nil {
		return
	}
	_ = st.event("response.completed", map[string]any{"type": "response.completed", "response": s.responsesBody(c.model, "completed", c.usage)})
}

// --- Anthropic messages -----------------------------------------------------

// anthropicUsage reports input_tokens EXCLUDING the cache read, the way the
// Anthropic wire does.
func anthropicUsage(u Usage) map[string]any {
	return map[string]any{
		"input_tokens":                u.PromptTokens - u.CachedTokens,
		"output_tokens":               u.CompletionTokens,
		"cache_read_input_tokens":     u.CachedTokens,
		"cache_creation_input_tokens": 0,
	}
}

func anthropicMessageStart(id, model string, u Usage) map[string]any {
	usage := anthropicUsage(u)
	usage["output_tokens"] = 1
	return map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": usage,
		},
	}
}

func (s *Server) serveAnthropic(c *call) {
	id := "msg_fake"
	if !c.stream {
		writeJSON(c.w, http.StatusOK, map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": c.model,
			"content":       []any{map[string]any{"type": "text", "text": s.cfg.Reply}},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         anthropicUsage(c.usage),
		})
		return
	}
	st := startSSE(c.w)
	steps := []struct {
		name string
		v    any
	}{
		{"message_start", anthropicMessageStart(id, c.model, c.usage)},
		{"content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": s.cfg.Reply}}},
		{"content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}},
		{"message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": c.usage.CompletionTokens}}},
		{"message_stop", map[string]any{"type": "message_stop"}},
	}
	for _, step := range steps {
		if st.event(step.name, step.v) != nil {
			return
		}
	}
}

// --- Gemini generateContent -------------------------------------------------

// parseGeminiCall splits "gemini-2.5-flash:streamGenerateContent".
func parseGeminiCall(call string) (model string, stream bool) {
	model, method, _ := strings.Cut(call, ":")
	return model, method == "streamGenerateContent"
}

func geminiUsage(u Usage) map[string]any {
	return map[string]any{
		"promptTokenCount":        u.PromptTokens,
		"candidatesTokenCount":    u.CompletionTokens,
		"totalTokenCount":         u.PromptTokens + u.CompletionTokens,
		"cachedContentTokenCount": u.CachedTokens,
	}
}

func geminiCandidate(text string, finish string) map[string]any {
	cand := map[string]any{
		"index":   0,
		"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}},
	}
	if finish != "" {
		cand["finishReason"] = finish
	}
	return cand
}

func (s *Server) serveGemini(c *call) {
	if !c.stream {
		writeJSON(c.w, http.StatusOK, map[string]any{
			"candidates":    []any{geminiCandidate(s.cfg.Reply, "STOP")},
			"usageMetadata": geminiUsage(c.usage),
			"modelVersion":  c.model,
		})
		return
	}
	st := startSSE(c.w)
	if st.data(map[string]any{"candidates": []any{geminiCandidate(s.cfg.Reply, "")}, "modelVersion": c.model}) != nil {
		return
	}
	_ = st.data(map[string]any{
		"candidates":    []any{geminiCandidate("", "STOP")},
		"usageMetadata": geminiUsage(c.usage),
		"modelVersion":  c.model,
	})
}

// --- System One -------------------------------------------------------------

// serveSystemOne answers every question with its first option. The product
// is billed on input tokens only; output_tokens is reported because the
// hosted API reports it.
func (s *Server) serveSystemOne(c *call) {
	answers := map[string]any{}
	questions, _ := c.body["questions"].(map[string]any)
	for name, q := range questions {
		qm, _ := q.(map[string]any)
		qType, _ := qm["type"].(string)
		switch qType {
		case "choice":
			label := "a"
			if crit, ok := qm["criteria"].(map[string]any); ok {
				for k := range crit {
					label = k
					break
				}
			}
			answers[name] = map[string]any{"type": "choice", "choice": label, "probabilities": map[string]any{label: 1.0}, "confidence": 1.0}
		case "score":
			answers[name] = map[string]any{"type": "score", "score": 0.5, "confidence": 1.0}
		default:
			answers[name] = map[string]any{"type": "noul", "value": true, "probability": 0.9, "confidence": 0.9}
		}
	}
	writeJSON(c.w, http.StatusOK, map[string]any{
		"model":   c.model,
		"answers": answers,
		"usage":   map[string]any{"input_tokens": c.usage.PromptTokens, "output_tokens": c.usage.CompletionTokens},
	})
}
