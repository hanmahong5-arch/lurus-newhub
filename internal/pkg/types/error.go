package types

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

const (
	// walletTopupPath is the relative path appended to IdentityPublicURL for wallet top-up.
	walletTopupPath = "/wallet/topup"
	// pricingPath is the relative path appended to IdentityPublicURL for plan upgrade.
	pricingPath = "/pricing"
)

type OpenAIError struct {
	Message  string          `json:"message"`
	Type     string          `json:"type"`
	Param    string          `json:"param"`
	Code     any             `json:"code"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type ClaudeError struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

type ErrorType string

const (
	ErrorTypeNewAPIError     ErrorType = "new_api_error"
	ErrorTypeOpenAIError     ErrorType = "openai_error"
	ErrorTypeClaudeError     ErrorType = "claude_error"
	ErrorTypeMidjourneyError ErrorType = "midjourney_error"
	ErrorTypeGeminiError     ErrorType = "gemini_error"
	ErrorTypeRerankError     ErrorType = "rerank_error"
	ErrorTypeUpstreamError   ErrorType = "upstream_error"
)

type ErrorCode string

const (
	ErrorCodeInvalidRequest         ErrorCode = "invalid_request"
	ErrorCodeSensitiveWordsDetected ErrorCode = "sensitive_words_detected"

	// new api error
	ErrorCodeCountTokenFailed   ErrorCode = "count_token_failed"
	ErrorCodeModelPriceError    ErrorCode = "model_price_error"
	ErrorCodeInvalidApiType     ErrorCode = "invalid_api_type"
	ErrorCodeJsonMarshalFailed  ErrorCode = "json_marshal_failed"
	ErrorCodeDoRequestFailed    ErrorCode = "do_request_failed"
	ErrorCodeGetChannelFailed   ErrorCode = "get_channel_failed"
	ErrorCodeGenRelayInfoFailed ErrorCode = "gen_relay_info_failed"

	// channel error
	ErrorCodeChannelNoAvailableKey        ErrorCode = "channel:no_available_key"
	ErrorCodeChannelAllKeysCooling        ErrorCode = "channel:all_keys_cooling"
	ErrorCodeChannelParamOverrideInvalid  ErrorCode = "channel:param_override_invalid"
	ErrorCodeChannelHeaderOverrideInvalid ErrorCode = "channel:header_override_invalid"
	ErrorCodeChannelModelMappedError      ErrorCode = "channel:model_mapped_error"
	ErrorCodeChannelAwsClientError        ErrorCode = "channel:aws_client_error"
	ErrorCodeChannelInvalidKey            ErrorCode = "channel:invalid_key"
	ErrorCodeChannelResponseTimeExceeded  ErrorCode = "channel:response_time_exceeded"

	// client request error
	ErrorCodeReadRequestBodyFailed ErrorCode = "read_request_body_failed"
	ErrorCodeConvertRequestFailed  ErrorCode = "convert_request_failed"
	ErrorCodeAccessDenied          ErrorCode = "access_denied"

	// request error
	ErrorCodeBadRequestBody ErrorCode = "bad_request_body"

	// response error
	ErrorCodeReadResponseBodyFailed ErrorCode = "read_response_body_failed"
	ErrorCodeBadResponseStatusCode  ErrorCode = "bad_response_status_code"
	ErrorCodeBadResponse            ErrorCode = "bad_response"
	ErrorCodeBadResponseBody        ErrorCode = "bad_response_body"
	ErrorCodeEmptyResponse          ErrorCode = "empty_response"
	// ErrorCodeUpstreamStreamIncomplete: the upstream stream stopped before
	// signalling completion (no terminator and no finish reason). Surfaced
	// in-band on an already-started stream; never billed.
	ErrorCodeUpstreamStreamIncomplete ErrorCode = "upstream_stream_incomplete"
	ErrorCodeAwsInvokeError           ErrorCode = "aws_invoke_error"
	ErrorCodeModelNotFound            ErrorCode = "model_not_found"
	ErrorCodePromptBlocked            ErrorCode = "prompt_blocked"

	// sql error
	ErrorCodeQueryDataError  ErrorCode = "query_data_error"
	ErrorCodeUpdateDataError ErrorCode = "update_data_error"

	// quota error
	ErrorCodeInsufficientUserQuota      ErrorCode = "insufficient_user_quota"
	ErrorCodePreConsumeTokenQuotaFailed ErrorCode = "pre_consume_token_quota_failed"
	ErrorCodeTenantQuotaExceeded        ErrorCode = "tenant_quota_exceeded"
	// ErrorCodeTokenQuotaExhausted marks a per-TOKEN spending-cap rejection —
	// distinct from ErrorCodeInsufficientUserQuota (per-USER wallet balance).
	// The remedy differs: a token-cap 402 is fixed by editing the token's own
	// remain_quota/unlimited_quota (token_service.go's guidance), not by a
	// wallet top-up. See middleware.TokenAuth and PreConsumeQuota's
	// ErrTokenQuotaInsufficient branch.
	ErrorCodeTokenQuotaExhausted ErrorCode = "token_quota_exhausted"
)

type NewAPIError struct {
	Err            error
	RelayError     any
	skipRetry      bool
	recordErrorLog *bool
	errorType      ErrorType
	errorCode      ErrorCode
	StatusCode     int
	Metadata       json.RawMessage

	// UpstreamHeader is the HTTP response header from the upstream provider, if any.
	// Populated by RelayErrorHandler so that downstream code (e.g. the OpenRouter
	// pool cooldown writer) can read X-RateLimit-Reset etc. without re-fetching.
	UpstreamHeader http.Header
	// UpstreamBodyHint holds the (truncated) raw response body for the same purpose.
	// Limited to ~16KB; longer responses are dropped to avoid bloating in-memory error chains.
	UpstreamBodyHint string

	// RetryAfterUnix carries the earliest recovery timestamp (Unix seconds) when
	// errorCode == ErrorCodeChannelAllKeysCooling. The relay's final-error path
	// converts this into the HTTP Retry-After header on the user-facing 503.
	RetryAfterUnix int64
}

// Unwrap enables errors.Is / errors.As to work with NewAPIError by exposing the underlying error.
func (e *NewAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *NewAPIError) GetErrorCode() ErrorCode {
	if e == nil {
		return ""
	}
	return e.errorCode
}

func (e *NewAPIError) GetErrorType() ErrorType {
	if e == nil {
		return ""
	}
	return e.errorType
}

func (e *NewAPIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		// fallback message when underlying error is missing
		return string(e.errorCode)
	}
	return e.Err.Error()
}

func (e *NewAPIError) MaskSensitiveError() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return string(e.errorCode)
	}
	errStr := e.Err.Error()
	if e.errorCode == ErrorCodeCountTokenFailed {
		return errStr
	}
	return common.MaskSensitiveInfo(errStr)
}

func (e *NewAPIError) SetMessage(message string) {
	e.Err = errors.New(message)
}

func (e *NewAPIError) ToOpenAIError() OpenAIError {
	var result OpenAIError
	switch e.errorType {
	case ErrorTypeOpenAIError:
		if openAIError, ok := e.RelayError.(OpenAIError); ok {
			result = openAIError
		}
	case ErrorTypeClaudeError:
		if claudeError, ok := e.RelayError.(ClaudeError); ok {
			result = OpenAIError{
				Message: e.Error(),
				Type:    claudeError.Type,
				Param:   "",
				Code:    e.errorCode,
			}
		}
	default:
		result = OpenAIError{
			Message: e.Error(),
			Type:    string(e.errorType),
			Param:   "",
			Code:    e.errorCode,
		}
	}
	// Forward structured Metadata (e.g. {"topup_url":...} on a 402) to the
	// client envelope. The switch above only carries it for the upstream
	// OpenAIError case; new_api_error / claude_error errors set it via
	// ErrOptionWithTopupURL/UpgradeURL and would otherwise lose it here.
	// Metadata is not run through MaskSensitiveInfo, so the URL survives.
	if len(e.Metadata) > 0 {
		result.Metadata = e.Metadata
	}
	if e.errorCode != ErrorCodeCountTokenFailed {
		result.Message = common.MaskSensitiveInfo(result.Message)
	}
	if result.Message == "" {
		result.Message = string(e.errorType)
	}
	return result
}

func (e *NewAPIError) ToClaudeError() ClaudeError {
	var result ClaudeError
	switch e.errorType {
	case ErrorTypeOpenAIError:
		if openAIError, ok := e.RelayError.(OpenAIError); ok {
			result = ClaudeError{
				Message: e.Error(),
				Type:    fmt.Sprintf("%v", openAIError.Code),
			}
		}
	case ErrorTypeClaudeError:
		if claudeError, ok := e.RelayError.(ClaudeError); ok {
			result = claudeError
		}
	default:
		result = ClaudeError{
			Message: e.Error(),
			Type:    string(e.errorType),
		}
	}
	if e.errorCode != ErrorCodeCountTokenFailed {
		result.Message = common.MaskSensitiveInfo(result.Message)
	}
	if result.Message == "" {
		result.Message = string(e.errorType)
	}
	return result
}

type NewAPIErrorOptions func(*NewAPIError)

func NewError(err error, errorCode ErrorCode, ops ...NewAPIErrorOptions) *NewAPIError {
	var newErr *NewAPIError
	// 保留深层传递的 new err
	if errors.As(err, &newErr) {
		for _, op := range ops {
			op(newErr)
		}
		return newErr
	}
	e := &NewAPIError{
		Err:        err,
		RelayError: nil,
		errorType:  ErrorTypeNewAPIError,
		StatusCode: http.StatusInternalServerError,
		errorCode:  errorCode,
	}
	for _, op := range ops {
		op(e)
	}
	return e
}

func NewOpenAIError(err error, errorCode ErrorCode, statusCode int, ops ...NewAPIErrorOptions) *NewAPIError {
	var newErr *NewAPIError
	// 保留深层传递的 new err
	if errors.As(err, &newErr) {
		if newErr.RelayError == nil {
			openaiError := OpenAIError{
				Message: newErr.Error(),
				Type:    string(errorCode),
				Code:    errorCode,
			}
			newErr.RelayError = openaiError
		}
		for _, op := range ops {
			op(newErr)
		}
		return newErr
	}
	openaiError := OpenAIError{
		Message: err.Error(),
		Type:    string(errorCode),
		Code:    errorCode,
	}
	return WithOpenAIError(openaiError, statusCode, ops...)
}

func InitOpenAIError(errorCode ErrorCode, statusCode int, ops ...NewAPIErrorOptions) *NewAPIError {
	openaiError := OpenAIError{
		Type: string(errorCode),
		Code: errorCode,
	}
	return WithOpenAIError(openaiError, statusCode, ops...)
}

func NewErrorWithStatusCode(err error, errorCode ErrorCode, statusCode int, ops ...NewAPIErrorOptions) *NewAPIError {
	e := &NewAPIError{
		Err: err,
		RelayError: OpenAIError{
			Message: err.Error(),
			Type:    string(errorCode),
		},
		errorType:  ErrorTypeNewAPIError,
		StatusCode: statusCode,
		errorCode:  errorCode,
	}
	for _, op := range ops {
		op(e)
	}

	return e
}

func WithOpenAIError(openAIError OpenAIError, statusCode int, ops ...NewAPIErrorOptions) *NewAPIError {
	code, ok := openAIError.Code.(string)
	if !ok {
		if openAIError.Code != nil {
			code = fmt.Sprintf("%v", openAIError.Code)
		} else {
			code = "unknown_error"
		}
	}
	if openAIError.Type == "" {
		openAIError.Type = "upstream_error"
	}
	e := &NewAPIError{
		RelayError: openAIError,
		errorType:  ErrorTypeOpenAIError,
		StatusCode: statusCode,
		Err:        errors.New(openAIError.Message),
		errorCode:  ErrorCode(code),
	}
	// OpenRouter
	if len(openAIError.Metadata) > 0 {
		openAIError.Message = fmt.Sprintf("%s (%s)", openAIError.Message, openAIError.Metadata)
		e.Metadata = openAIError.Metadata
		e.RelayError = openAIError
		e.Err = errors.New(openAIError.Message)
	}
	for _, op := range ops {
		op(e)
	}
	return e
}

func WithClaudeError(claudeError ClaudeError, statusCode int, ops ...NewAPIErrorOptions) *NewAPIError {
	if claudeError.Type == "" {
		claudeError.Type = "upstream_error"
	}
	e := &NewAPIError{
		RelayError: claudeError,
		errorType:  ErrorTypeClaudeError,
		StatusCode: statusCode,
		Err:        errors.New(claudeError.Message),
		errorCode:  ErrorCode(claudeError.Type),
	}
	for _, op := range ops {
		op(e)
	}
	return e
}

func IsChannelError(err *NewAPIError) bool {
	if err == nil {
		return false
	}
	return strings.HasPrefix(string(err.errorCode), "channel:")
}

func IsSkipRetryError(err *NewAPIError) bool {
	if err == nil {
		return false
	}

	return err.skipRetry
}

// IsUpstreamFailure reports whether err is attributable to the upstream
// provider (or network) rather than the caller. Used by the per-channel
// circuit breaker so that user-side errors (4xx, client cancellation) do not
// trip a healthy channel and starve other tenants of capacity.
//
// True:  channel:* errors, upstream timeouts (408/504/524), 5xx, unclassified.
// False: nil, ctx.Canceled, 4xx (other than the upstream-timeout statuses).
//
// Default for unclassified status codes is true (fail-safe — better to cool
// a possibly-fine channel than to miss a real outage).
func IsUpstreamFailure(err *NewAPIError) bool {
	if err == nil {
		return false
	}
	if IsChannelError(err) {
		return true
	}
	if err.Err != nil && errors.Is(err.Err, context.Canceled) {
		return false
	}
	switch err.StatusCode {
	case http.StatusRequestTimeout, // 408 — upstream took too long
		http.StatusGatewayTimeout, // 504
		524:                       // CF origin timeout
		return true
	}
	if err.StatusCode/100 == 5 {
		return true
	}
	if err.StatusCode/100 == 4 {
		return false
	}
	return true
}

// RelayErrorType classifies a terminal relay error into one bounded,
// low-cardinality bucket for the relay_errors_total metric (O1): the missing
// "WHY is a provider failing" signal. The classification is two-tier on purpose:
//
//  1. errorCode first — to peel off errors that are NOT upstream-provider faults
//     even though they often carry a synthetic 500 status: caller quota/credit
//     exhaustion ("insufficient_quota") and newhub-internal / request-prep /
//     routing / persistence failures ("internal"). Bucketing these by status
//     would pollute upstream_5xx and make the signal lie.
//  2. status code second — for everything that did reach the upstream exchange,
//     isolating the two highest-signal sub-classes (rate-limit, timeout) before
//     falling back to the 4xx/5xx status class.
//
// Returns one of: upstream_5xx, upstream_4xx, upstream_timeout,
// upstream_rate_limit, insufficient_quota, internal.
func RelayErrorType(err *NewAPIError) string {
	if err == nil {
		return "internal"
	}
	switch err.errorCode {
	// Caller quota/credit exhaustion — actionable, not an upstream fault.
	case ErrorCodeInsufficientUserQuota, ErrorCodePreConsumeTokenQuotaFailed, ErrorCodeTenantQuotaExceeded,
		ErrorCodeTokenQuotaExhausted:
		return "insufficient_quota"
	// newhub-internal / request-prep / routing / persistence failures: synthetic
	// 500s (or client-prep 4xx) that must NOT count as upstream provider faults.
	case ErrorCodeInvalidRequest, ErrorCodeSensitiveWordsDetected,
		ErrorCodeCountTokenFailed, ErrorCodeModelPriceError, ErrorCodeInvalidApiType,
		ErrorCodeJsonMarshalFailed, ErrorCodeGenRelayInfoFailed, ErrorCodeGetChannelFailed,
		ErrorCodeReadRequestBodyFailed, ErrorCodeConvertRequestFailed, ErrorCodeAccessDenied,
		ErrorCodeBadRequestBody, ErrorCodeQueryDataError, ErrorCodeUpdateDataError:
		return "internal"
	}
	// From here the error is attributable to the upstream exchange (response
	// status, transport failure, or channel capacity).
	switch err.StatusCode {
	case http.StatusTooManyRequests: // 429
		return "upstream_rate_limit"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout, 524: // 408 / 504 / CF 524
		return "upstream_timeout"
	}
	if err.StatusCode/100 == 4 {
		return "upstream_4xx"
	}
	// 5xx and unclassified (status 0 / channel-capacity) → fail-safe upstream_5xx,
	// mirroring IsUpstreamFailure's default-true posture.
	return "upstream_5xx"
}

func ErrOptionWithSkipRetry() NewAPIErrorOptions {
	return func(e *NewAPIError) {
		e.skipRetry = true
	}
}

func ErrOptionWithNoRecordErrorLog() NewAPIErrorOptions {
	return func(e *NewAPIError) {
		e.recordErrorLog = common.GetPointer(false)
	}
}

func ErrOptionWithHideErrMsg(replaceStr string) NewAPIErrorOptions {
	return func(e *NewAPIError) {
		if common.DebugEnabled {
			common.SysLogf("ErrOptionWithHideErrMsg: %s, origin error: %s", replaceStr, e.Err)
		}
		e.Err = errors.New(replaceStr)
	}
}

func IsRecordErrorLog(e *NewAPIError) bool {
	if e == nil {
		return false
	}
	if e.recordErrorLog == nil {
		// default to true if not set
		return true
	}
	return *e.recordErrorLog
}

// ErrOptionWithTopupURL attaches a {"topup_url": ...} JSON object to the error
// Metadata so the client can navigate directly to the wallet top-up page.
// Used for per-user balance exhaustion (HTTP 402).
// Note: types imports common — no import cycle.
func ErrOptionWithTopupURL() NewAPIErrorOptions {
	return func(e *NewAPIError) {
		raw, _ := json.Marshal(map[string]string{
			"topup_url": common.IdentityPublicURL + walletTopupPath,
		})
		e.Metadata = json.RawMessage(raw)
	}
}

// ErrOptionWithTokenQuotaHint attaches structured metadata for a per-TOKEN
// spending-cap 402 (HTTP 402, ErrorCodeTokenQuotaExhausted): the token's own
// remain_quota, NOT a wallet top-up link. Unlike ErrOptionWithTopupURL, a
// wallet top-up cannot fix this state — the remedy is editing the token's
// remain_quota or switching it to unlimited (see token_service.go's
// "请先修改令牌剩余额度，或者设置为无限额度" guidance) — so this option
// deliberately does NOT set a management URL (none exists yet) and does NOT
// set Retry-After (no data source for "how long until it recovers").
//
// The field is named token_remain_quota_units (not token_remain_quota) and
// carries the RAW internal quota integer (common.QuotaPerUnit units == 1
// baseline USD), deliberately NOT the same unit as the human-readable
// error.message text: PreConsumeQuota's ErrTokenQuotaInsufficient path
// builds that message via logger.FormatQuota, which renders in whatever
// currency operation_setting.GetQuotaDisplayType() is configured for (USD,
// CNY, or a custom currency/rate) — a moving target this hint must not try
// to match. Naming the raw-unit field explicitly (rather than reusing the
// ambiguous "token_remain_quota" name) is the fix for a prior defect where
// the same JSON response carried this integer under that name right next to
// a currency-formatted number in .message, differing by ~5x10^5 with no unit
// on either — see r2_token_quota_code_test.go / l3_token_quota_402_test.go.
// remainQuota is NOT floored at zero: a negative value (e.g. from a
// concurrent settlement draining the token between the read and this call)
// is passed through as-is — see the negative-value case in
// r2_token_quota_code_test.go.
func ErrOptionWithTokenQuotaHint(remainQuota int) NewAPIErrorOptions {
	return func(e *NewAPIError) {
		raw, _ := json.Marshal(map[string]any{
			"reason":                   "token_quota_exhausted",
			"token_remain_quota_units": remainQuota,
		})
		e.Metadata = json.RawMessage(raw)
	}
}

// ErrOptionWithTokenDisabledHint is the sibling of ErrOptionWithTokenQuotaHint
// for the state repo.ValidateUserToken's Status==TokenStatusExhausted branch
// can also produce: the token's Status flag is still "exhausted" but its
// RemainQuota has since been raised above zero (or switched to unlimited)
// without Status being flipped back to Enabled — handler/token.go's
// app.ApplyTokenUpdate copies RemainQuota but never touches Status; the
// enable transition only runs when the update request explicitly sets
// status=Enabled (token.go:230/CanEnableToken). Calling this "token quota
// exhausted" (ErrOptionWithTokenQuotaHint's reason) would contradict the very
// remainQuota this metadata reports, so it uses a distinct reason string —
// the remedy is re-enabling the token in the console, not editing its quota.
func ErrOptionWithTokenDisabledHint(remainQuota int) NewAPIErrorOptions {
	return func(e *NewAPIError) {
		raw, _ := json.Marshal(map[string]any{
			"reason":                   "token_disabled",
			"token_remain_quota_units": remainQuota,
		})
		e.Metadata = json.RawMessage(raw)
	}
}

// ErrOptionWithUpgradeURL attaches a {"upgrade_url": ...} JSON object to the error
// Metadata so the client can navigate to the plan pricing page.
// Used for tenant monthly-cap exhaustion (HTTP 402).
func ErrOptionWithUpgradeURL() NewAPIErrorOptions {
	return func(e *NewAPIError) {
		raw, _ := json.Marshal(map[string]string{
			"upgrade_url": common.IdentityPublicURL + pricingPath,
		})
		e.Metadata = json.RawMessage(raw)
	}
}
