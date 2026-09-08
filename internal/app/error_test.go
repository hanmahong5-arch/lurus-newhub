package app

import (
	"errors"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestMidjourneyErrorWrapper(t *testing.T) {
	tests := []struct {
		name         string
		code         int
		desc         string
		expectedCode int
		expectedDesc string
	}{
		{"basic error", 400, "Bad request", 400, "Bad request"},
		{"zero code", 0, "Zero code error", 0, "Zero code error"},
		{"negative code", -1, "Negative code", -1, "Negative code"},
		{"empty description", 500, "", 500, ""},
		{"unicode description", 200, "错误消息", 200, "错误消息"},
		{"long description", 404, "This is a very long error description that contains a lot of text to test how the wrapper handles longer messages", 404, "This is a very long error description that contains a lot of text to test how the wrapper handles longer messages"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MidjourneyErrorWrapper(tt.code, tt.desc)

			if result == nil {
				t.Fatal("MidjourneyErrorWrapper returned nil")
			}
			if result.Code != tt.expectedCode {
				t.Errorf("Code = %d, want %d", result.Code, tt.expectedCode)
			}
			if result.Description != tt.expectedDesc {
				t.Errorf("Description = %q, want %q", result.Description, tt.expectedDesc)
			}
		})
	}
}

func TestMidjourneyErrorWithStatusCodeWrapper(t *testing.T) {
	tests := []struct {
		name               string
		code               int
		desc               string
		statusCode         int
		expectedCode       int
		expectedDesc       string
		expectedStatusCode int
	}{
		{"basic", 400, "Bad request", http.StatusBadRequest, 400, "Bad request", http.StatusBadRequest},
		{"internal error", 500, "Internal error", http.StatusInternalServerError, 500, "Internal error", http.StatusInternalServerError},
		{"not found", 404, "Not found", http.StatusNotFound, 404, "Not found", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MidjourneyErrorWithStatusCodeWrapper(tt.code, tt.desc, tt.statusCode)

			if result == nil {
				t.Fatal("MidjourneyErrorWithStatusCodeWrapper returned nil")
			}
			if result.StatusCode != tt.expectedStatusCode {
				t.Errorf("StatusCode = %d, want %d", result.StatusCode, tt.expectedStatusCode)
			}
			if result.Response.Code != tt.expectedCode {
				t.Errorf("Response.Code = %d, want %d", result.Response.Code, tt.expectedCode)
			}
			if result.Response.Description != tt.expectedDesc {
				t.Errorf("Response.Description = %q, want %q", result.Response.Description, tt.expectedDesc)
			}
		})
	}
}

func TestClaudeErrorWrapper(t *testing.T) {
	tests := []struct {
		name               string
		err                error
		code               string
		statusCode         int
		expectedType       string
		containsMessage    bool // whether message should contain original error
		expectedStatusCode int
	}{
		{
			// L3-CONTRACT-TAXONOMY: ClaudeErrorWrapper now stamps the
			// Anthropic vendor type via types.WireErrorType(statusCode, ...)
			// instead of the retired new_api_error literal.
			name:               "basic error",
			err:                errors.New("something went wrong"),
			code:               "error_code",
			statusCode:         http.StatusBadRequest,
			expectedType:       "invalid_request_error",
			containsMessage:    true,
			expectedStatusCode: http.StatusBadRequest,
		},
		{
			name:               "network error with post",
			err:                errors.New("Post request failed to endpoint"),
			code:               "network_error",
			statusCode:         http.StatusBadGateway,
			expectedType:       "api_error",
			containsMessage:    false, // should be masked
			expectedStatusCode: http.StatusBadGateway,
		},
		{
			// L3-CONTRACT-TAXONOMY item 7: 503 now diverges the same way 529
			// does — types.WireErrorType(503, ClaudeError) answers
			// overloaded_error, not the plain 5xx api_error fallback, so the
			// Claude wire sees one consistent "temporarily unavailable"
			// vendor type regardless of which internal cause (all-keys-
			// cooling, or this generic dial failure) produced the 503.
			name:               "network error with dial",
			err:                errors.New("dial tcp connection refused"),
			code:               "connection_error",
			statusCode:         http.StatusServiceUnavailable,
			expectedType:       "overloaded_error",
			containsMessage:    false, // should be masked
			expectedStatusCode: http.StatusServiceUnavailable,
		},
		{
			name:               "network error with http",
			err:                errors.New("http timeout exceeded"),
			code:               "timeout",
			statusCode:         http.StatusGatewayTimeout,
			expectedType:       "api_error",
			containsMessage:    false, // should be masked
			expectedStatusCode: http.StatusGatewayTimeout,
		},
		{
			name:               "file base64 error not masked",
			err:                errors.New("get file base64 from url failed"),
			code:               "file_error",
			statusCode:         http.StatusBadRequest,
			expectedType:       "invalid_request_error",
			containsMessage:    true, // should NOT be masked
			expectedStatusCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClaudeErrorWrapper(tt.err, tt.code, tt.statusCode)

			if result == nil {
				t.Fatal("ClaudeErrorWrapper returned nil")
			}
			if result.Error.Type != tt.expectedType {
				t.Errorf("Error.Type = %q, want %q", result.Error.Type, tt.expectedType)
			}
			if result.StatusCode != tt.expectedStatusCode {
				t.Errorf("StatusCode = %d, want %d", result.StatusCode, tt.expectedStatusCode)
			}
			if result.LocalError {
				t.Error("LocalError should be false for ClaudeErrorWrapper")
			}

			// Check if message contains original error or is masked
			if tt.containsMessage {
				if result.Error.Message != tt.err.Error() {
					t.Errorf("Message = %q, want %q", result.Error.Message, tt.err.Error())
				}
			} else {
				// Should be masked to Chinese message
				if result.Error.Message == tt.err.Error() {
					t.Errorf("Message should be masked but got original: %q", result.Error.Message)
				}
			}
		})
	}
}

func TestClaudeErrorWrapperLocal(t *testing.T) {
	err := errors.New("test error")
	result := ClaudeErrorWrapperLocal(err, "test_code", http.StatusBadRequest)

	if result == nil {
		t.Fatal("ClaudeErrorWrapperLocal returned nil")
	}
	if !result.LocalError {
		t.Error("LocalError should be true for ClaudeErrorWrapperLocal")
	}
	if result.Error.Type != "invalid_request_error" {
		t.Errorf("Error.Type = %q, want invalid_request_error", result.Error.Type)
	}
}

func TestTaskErrorWrapper(t *testing.T) {
	tests := []struct {
		name               string
		err                error
		code               string
		statusCode         int
		containsOriginal   bool
		expectedStatusCode int
	}{
		{
			name:               "basic error",
			err:                errors.New("task failed"),
			code:               "task_error",
			statusCode:         http.StatusInternalServerError,
			containsOriginal:   true,
			expectedStatusCode: http.StatusInternalServerError,
		},
		{
			name:               "network error masked",
			err:                errors.New("POST https://api.example.com failed"),
			code:               "network_error",
			statusCode:         http.StatusBadGateway,
			containsOriginal:   false, // should be masked
			expectedStatusCode: http.StatusBadGateway,
		},
		{
			name:               "dial error masked",
			err:                errors.New("dial tcp 10.0.0.1:443 connection refused"),
			code:               "connection_error",
			statusCode:         http.StatusServiceUnavailable,
			containsOriginal:   false, // should be masked
			expectedStatusCode: http.StatusServiceUnavailable,
		},
		{
			name:               "http error with URL masked",
			err:                errors.New("http error: POST https://api.example.com/v1 canceled"),
			code:               "timeout",
			statusCode:         http.StatusGatewayTimeout,
			containsOriginal:   false, // URL will be masked by MaskSensitiveInfo
			expectedStatusCode: http.StatusGatewayTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TaskErrorWrapper(tt.err, tt.code, tt.statusCode)

			if result == nil {
				t.Fatal("TaskErrorWrapper returned nil")
			}
			if result.Code != tt.code {
				t.Errorf("Code = %q, want %q", result.Code, tt.code)
			}
			if result.StatusCode != tt.expectedStatusCode {
				t.Errorf("StatusCode = %d, want %d", result.StatusCode, tt.expectedStatusCode)
			}
			if result.Error == nil {
				t.Error("Error should not be nil")
			}
			if result.LocalError {
				t.Error("LocalError should be false for TaskErrorWrapper")
			}

			// Check message masking
			if tt.containsOriginal {
				if result.Message != tt.err.Error() {
					t.Errorf("Message = %q, want %q", result.Message, tt.err.Error())
				}
			} else {
				// Should be masked (not equal to original)
				if result.Message == tt.err.Error() {
					t.Errorf("Message should be masked but got original: %q", result.Message)
				}
			}
		})
	}
}

func TestTaskErrorWrapperLocal(t *testing.T) {
	err := errors.New("local task error")
	result := TaskErrorWrapperLocal(err, "local_error", http.StatusInternalServerError)

	if result == nil {
		t.Fatal("TaskErrorWrapperLocal returned nil")
	}
	if !result.LocalError {
		t.Error("LocalError should be true for TaskErrorWrapperLocal")
	}
	if result.Code != "local_error" {
		t.Errorf("Code = %q, want local_error", result.Code)
	}
}

func TestResetStatusCode(t *testing.T) {
	tests := []struct {
		name           string
		initialStatus  int
		mappingStr     string
		expectedStatus int
	}{
		{
			name:           "empty mapping",
			initialStatus:  400,
			mappingStr:     "",
			expectedStatus: 400,
		},
		{
			name:           "empty object mapping",
			initialStatus:  400,
			mappingStr:     "{}",
			expectedStatus: 400,
		},
		{
			name:           "status code mapped",
			initialStatus:  400,
			mappingStr:     `{"400": "429"}`,
			expectedStatus: 429,
		},
		{
			name:           "status code not in mapping",
			initialStatus:  500,
			mappingStr:     `{"400": "429"}`,
			expectedStatus: 500,
		},
		{
			name:           "200 not mapped",
			initialStatus:  200,
			mappingStr:     `{"200": "201"}`,
			expectedStatus: 200, // 200 should not be changed
		},
		{
			name:           "invalid json",
			initialStatus:  400,
			mappingStr:     `{"invalid`,
			expectedStatus: 400,
		},
		{
			name:           "multiple mappings",
			initialStatus:  502,
			mappingStr:     `{"400": "429", "502": "503", "500": "503"}`,
			expectedStatus: 503,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock NewAPIError
			apiErr := &types.NewAPIError{
				StatusCode: tt.initialStatus,
			}

			ResetStatusCode(apiErr, tt.mappingStr)

			if apiErr.StatusCode != tt.expectedStatus {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.expectedStatus)
			}
		})
	}
}

// Verify error wrapper interfaces
func TestErrorWrapperReturnsNonNil(t *testing.T) {
	// These should never return nil
	if MidjourneyErrorWrapper(0, "") == nil {
		t.Error("MidjourneyErrorWrapper returned nil")
	}
	if MidjourneyErrorWithStatusCodeWrapper(0, "", 0) == nil {
		t.Error("MidjourneyErrorWithStatusCodeWrapper returned nil")
	}
	if ClaudeErrorWrapper(errors.New(""), "", 0) == nil {
		t.Error("ClaudeErrorWrapper returned nil")
	}
	if ClaudeErrorWrapperLocal(errors.New(""), "", 0) == nil {
		t.Error("ClaudeErrorWrapperLocal returned nil")
	}
	if TaskErrorWrapper(errors.New(""), "", 0) == nil {
		t.Error("TaskErrorWrapper returned nil")
	}
	if TaskErrorWrapperLocal(errors.New(""), "", 0) == nil {
		t.Error("TaskErrorWrapperLocal returned nil")
	}
}
