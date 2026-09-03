package types

import "net/http"

// GeminiErrorFrame is the Gemini API error envelope: {"error":{code,message,
// status}}. A struct rather than a map so "error" is the first key on the
// wire — google-genai checks for the literal `{"error":` prefix, so key order
// is load-bearing, and so is the Code/Message/Status field order below.
//
// This lived unexported in app/relay/helper, where only the streaming path
// could reach it. The non-streaming path had no Gemini case at all and
// answered a Gemini caller with an OpenAI envelope, so the SDK could not
// recognise its own error shape.
type GeminiErrorFrame struct {
	Error GeminiError `json:"error"`
}

type GeminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// ToGeminiError renders the error in Gemini's native envelope.
func (e *NewAPIError) ToGeminiError() GeminiErrorFrame {
	if e == nil {
		return GeminiErrorFrame{Error: GeminiError{
			Code:    http.StatusInternalServerError,
			Status:  GeminiStatus(http.StatusInternalServerError),
			Message: "",
		}}
	}
	return GeminiErrorFrame{Error: GeminiError{
		Code:    e.StatusCode,
		Message: e.ToOpenAIError().Message,
		Status:  GeminiStatus(e.StatusCode),
	}}
}

// GeminiStatus maps an HTTP status to the google.rpc.Code name the Gemini API
// puts in error.status.
func GeminiStatus(code int) string {
	switch code {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusInternalServerError:
		return "INTERNAL"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	}
	return "UNKNOWN"
}
