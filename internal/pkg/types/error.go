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

	// gateway rejection codes — the machine-readable half of a middleware-
	// stage 4xx/5xx (L3-CONTRACT-TAXONOMY). Every abortWithOpenAiMessage call
	// site now names one of these (or reuses ErrorCodeInvalidRequest) instead
	// of leaving Code empty; see abort_code_structural_test.go for the sweep.
	ErrorCodeModelBlocked             ErrorCode = "model_blocked"
	ErrorCodeTokenDisabled            ErrorCode = "token_disabled"
	ErrorCodeUserBanned               ErrorCode = "user_banned"
	ErrorCodeTenantSuspended          ErrorCode = "tenant_suspended"
	ErrorCodeIpNotAllowed             ErrorCode = "ip_not_allowed"
	ErrorCodeGroupNotAllowed          ErrorCode = "group_not_allowed"
	ErrorCodeSessionRequired          ErrorCode = "session_required"
	ErrorCodeChannelSpecifyForbidden  ErrorCode = "channel_specify_forbidden"
	ErrorCodeScopeNotGranted          ErrorCode = "scope_not_granted"
	ErrorCodeRequestRateLimitExceeded ErrorCode = "request_rate_limit_exceeded"
	ErrorCodeGatewayInternal          ErrorCode = "gateway_internal"

	// Emitted by the pool/entitlement/cost-spike/rate-limit gates below
	// PoolBalanceCheck in the relay chain; promoted from string literals so
	// the openapi_contract_lock_test bidirectional enum check (relay.json
	// GatewayError.code.enum <-> ErrorCode constants) covers them too.
	ErrorCodePoolExhausted             ErrorCode = "pool_exhausted"
	ErrorCodePoolNotConfigured         ErrorCode = "pool_not_configured"
	ErrorCodeQuotaExceeded             ErrorCode = "quota_exceeded"
	ErrorCodeCostSpikeLimitExceeded    ErrorCode = "cost_spike_limit_exceeded"
	ErrorCodeBusinessRateLimitExceeded ErrorCode = "business_rate_limit_exceeded"
	ErrorCodeConcurrencyLimitExceeded  ErrorCode = "concurrency_limit_exceeded"
	ErrorCodeAPINotImplemented         ErrorCode = "api_not_implemented"
	// ErrorCodeTaskPlatformUnknown is returned by the generic async-task
	// surface (/v1/tasks/:platform) when :platform names no compiled
	// provider/task/* adaptor (cycle-8 L8).
	ErrorCodeTaskPlatformUnknown ErrorCode = "task_platform_unknown"
	// ErrorCodeResponsesCompactUnsupported is returned by POST
	// /v1/responses/compact (cycle-8 L6, wire-formats-03) when the selected
	// channel's type is not in common.SupportsResponsesCompact's allow-list.
	// Checked after channel selection but before any upstream call.
	ErrorCodeResponsesCompactUnsupported ErrorCode = "responses_compact_unsupported"
	// ErrorCodeResponseNotFound is returned by GET/DELETE
	// /v1/responses/:response_id (cycle-8 L7, tasks-plugins-12) for three
	// byte-identical cases: no response_registry row for the id, a row that
	// belongs to a different user or tenant, and a row whose channel is
	// missing or disabled. The handler never distinguishes these with a 403
	// — see handler/relay_responses_registry.go.
	ErrorCodeResponseNotFound ErrorCode = "response_not_found"
	// ErrorCodeArtifactNotFound is returned by GET
	// /v1/tasks/:platform/:task_id/artifacts/:key/content (cycle-8 L9,
	// tasks-plugins-02/17/19) when :key is not one the sibling listing route
	// would currently produce for this task. Deliberately distinct from the
	// task-ownership 404 (task_generic.go's respondTaskNotFound, which stays
	// codeless — see that function's doc comment), which this handler also
	// uses unmodified for an absent/foreign task_id.
	ErrorCodeArtifactNotFound ErrorCode = "artifact_not_found"
	// ErrorCodeArtifactRequestRejected is returned by the artifact-content
	// proxy (task_media_guard.go's streamMediaContent) when the request is
	// refused by this gateway's own policy before any upstream fetch is
	// attempted: an unfetchable scheme, a self-referential/loop URL, a
	// fetch_setting egress-policy rejection, or a known Content-Length that
	// exceeds the proxy's size cap.
	ErrorCodeArtifactRequestRejected ErrorCode = "artifact_request_rejected"
	// ErrorCodeArtifactUpstreamError is returned by the artifact-content
	// proxy when the request itself was allowed but the upstream fetch
	// failed or returned a non-200 status.
	ErrorCodeArtifactUpstreamError ErrorCode = "artifact_upstream_error"
)

// WireErrorType maps an HTTP status code to the vendor-taxonomy "type" string
// for the wire the caller is speaking. OpenAI and Anthropic share almost the
// same buckets; they diverge on 402 (insufficient_quota vs billing_error) and
// on the two capacity statuses — 503 and 529 — where the Anthropic wire uses
// overloaded_error (OpenAI's 503 stays the plain 5xx api_error bucket).
//
// Reachability — three callers, do not narrow this back to the first one:
//  1. gateway-originated rejections (errorType == ErrorTypeNewAPIError:
//     middleware aborts, handler rejections), in both converters' default
//     branch. No literal constructor in this codebase builds one of those
//     with StatusCode 529, so for THIS caller 529 arrives only when a
//     channel's admin-configured status_code_mapping (applied by
//     app/error.go ResetStatusCode) rewrites the status;
//  2. every UPSTREAM OpenAI-shaped error (ErrorTypeOpenAIError — which is
//     what app/error.go's RelayErrorHandler builds for every vendor 4xx/5xx)
//     rendered on the Anthropic wire, i.e. ToClaudeError's
//     ErrorTypeOpenAIError branch. A vendor 529 or 503 reaches the
//     overloaded_error branch straight from the upstream body there, with no
//     status_code_mapping involved anywhere;
//  3. the fallback in claudeTypeToOpenAIType, for an upstream Anthropic type
//     that belongs to neither vocabulary.
//
// Any status this table does not name falls back to invalid_request_error
// for 4xx and api_error for 5xx/unknown — never the bare "new_api_error"
// literal a caller would otherwise have to special-case.
func WireErrorType(statusCode int, wire ErrorType) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusPaymentRequired:
		if wire == ErrorTypeClaudeError {
			return "billing_error"
		}
		return "insufficient_quota"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable:
		if wire == ErrorTypeClaudeError {
			return "overloaded_error"
		}
		return "api_error"
	case 529:
		if wire == ErrorTypeClaudeError {
			return "overloaded_error"
		}
		return "api_error"
	}
	if statusCode/100 == 5 {
		return "api_error"
	}
	return "invalid_request_error"
}

// claudeTypeToOpenAIType translates an upstream Anthropic error "type" into
// the OpenAI-wire vocabulary this gateway emits (the values WireErrorType
// returns for ErrorTypeOpenAIError). It is the mirror of ToClaudeError's
// ErrorTypeOpenAIError branch: neither wire may carry the other's private
// type names.
//
// The two vocabularies differ in exactly two members, so this is a
// translation and NOT a flattening: every shared name is passed through,
// because it is strictly more specific than the HTTP status, and the status
// is worthless here — both WithClaudeError call sites
// (provider/claude/relay-claude.go:692 and :824, the in-band error frames
// parsed out of a Claude response body) hardcode StatusCode 500.
//
// CAUTION — this value is not only rendered: app.ShouldDisableChannel
// (internal/app/channel.go:85 and the Type switch at :111-125) reads
// ToOpenAIError().Type to decide whether to auto-ban the channel. Passing the
// shared names through keeps authentication_error / permission_error
// disabling the channel exactly as before. billing_error is a deliberate
// behaviour change: it now reads insufficient_quota, which that switch DOES
// match, so an Anthropic channel whose upstream account reports a billing
// failure in-band is auto-banned where before it was not — which is the
// stated intent of the insufficient_quota rule.
func claudeTypeToOpenAIType(claudeType string, statusCode int) string {
	switch claudeType {
	case "invalid_request_error", "authentication_error", "permission_error",
		"not_found_error", "request_too_large", "rate_limit_error", "api_error":
		// In both vocabularies (WireErrorType emits every one of these on the
		// OpenAI wire too) — keep the vendor's own classification.
		return claudeType
	case "overloaded_error":
		// Anthropic-only (503/529); OpenAI's bucket for those is api_error.
		return "api_error"
	case "billing_error":
		// Anthropic-only (402); OpenAI's bucket for that is insufficient_quota.
		return "insufficient_quota"
	}
	// Outside both vocabularies — WithClaudeError's "upstream_error" default
	// for a body with no type, or a type Anthropic adds after this was
	// written. Nothing to translate, so fall back to the status table rather
	// than put an unrecognised string on the wire.
	return WireErrorType(statusCode, ErrorTypeOpenAIError)
}

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

// SetMessage rewrites the message the client will read. ToOpenAIError and
// ToClaudeError below both derive the rendered Message from e.Err in EVERY
// branch, so a rewrite here reaches the OpenAI, Anthropic and Gemini wires
// alike (Gemini's converter delegates to ToOpenAIError) — including for the
// upstream error types (ErrorTypeOpenAIError / ErrorTypeClaudeError), whose
// stored RelayError keeps the vendor's original text and contributes only
// type/param/code to the envelope. Before that single source of truth, the
// two upstream branches returned the stored vendor struct verbatim, so the
// "(request id: ...)" suffix and the upstream-provider attribution
// handler/relay.go adds were silently dropped on exactly the most common
// failure a customer hits (a vendor 429 or 5xx).
//
// Two limits on "reaches every wire", both deliberate: if e.Err renders
// empty, each converter substitutes the ErrorType literal instead (see the
// tails of both functions), and the envelopes that do NOT go through these
// converters — middleware/utils.go's abortWithMidjourneyMessage and
// dto.TaskError — never read e.Err at all, so a rewrite is invisible there.
func (e *NewAPIError) SetMessage(message string) {
	e.Err = errors.New(message)
}

func (e *NewAPIError) ToOpenAIError() OpenAIError {
	var result OpenAIError
	switch e.errorType {
	case ErrorTypeOpenAIError:
		if openAIError, ok := e.RelayError.(OpenAIError); ok {
			// Only Type/Param/Code/Metadata are taken from the vendor's
			// stored error; Message is re-derived from e.Err below.
			result = openAIError
		}
	case ErrorTypeClaudeError:
		if claudeError, ok := e.RelayError.(ClaudeError); ok {
			// Mirror of ToClaudeError's ErrorTypeOpenAIError branch below:
			// the vendor's type is translated into this wire's vocabulary
			// instead of being stamped in raw, so overloaded_error /
			// billing_error — which exist only on Anthropic's wire — cannot
			// land in an OpenAI envelope's type slot. Unlike that direction
			// nothing is lost here: the OpenAI envelope HAS a code slot, and
			// e.errorCode is the vendor type verbatim (WithClaudeError).
			result = OpenAIError{
				Type:  claudeTypeToOpenAIType(claudeError.Type, e.StatusCode),
				Param: "",
				Code:  e.errorCode,
			}
		}
	default:
		// ErrorTypeNewAPIError (gateway-originated middleware/handler
		// rejections) gets the vendor-taxonomy type keyed off the HTTP
		// status; the other default-falling types (midjourney/gemini/
		// rerank/upstream_error) keep stamping their own ErrorType literal
		// unchanged — those wires have their own type vocabularies already.
		errType := string(e.errorType)
		if e.errorType == ErrorTypeNewAPIError {
			errType = WireErrorType(e.StatusCode, ErrorTypeOpenAIError)
		}
		result = OpenAIError{
			Type:  errType,
			Param: "",
			Code:  e.errorCode,
		}
	}
	// One source of truth for the rendered message, in EVERY branch: e.Err,
	// which is what SetMessage writes (see its comment).
	result.Message = e.Error()
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
		// The Anthropic envelope has exactly two fields, type and message —
		// no slot for the vendor's "code". Stamping that code into .type put
		// a foreign value where an Anthropic SDK expects one of its own type
		// names, and, because an Anthropic-shaped upstream body carries no
		// "code" key at all (dto.GeneralErrorResponse leaves Code nil and the
		// "unknown_error" fallback in WithOpenAIError is written only to
		// e.errorCode), the COMMON case rendered the literal string "<nil>".
		// Use the same status-keyed Anthropic taxonomy the default branch
		// below uses; the vendor code stays reachable on the OpenAI wire and
		// through GetErrorCode for the error log.
		result = ClaudeError{Type: WireErrorType(e.StatusCode, ErrorTypeClaudeError)}
	case ErrorTypeClaudeError:
		if claudeError, ok := e.RelayError.(ClaudeError); ok {
			// Type only; Message is re-derived from e.Err below.
			result = ClaudeError{Type: claudeError.Type}
		}
	default:
		// Mirrors ToOpenAIError's default branch above: only
		// ErrorTypeNewAPIError gets the vendor (Anthropic) taxonomy mapping;
		// the other default-falling ErrorTypes keep their own literal.
		errType := string(e.errorType)
		if e.errorType == ErrorTypeNewAPIError {
			errType = WireErrorType(e.StatusCode, ErrorTypeClaudeError)
		}
		result = ClaudeError{
			Type: errType,
		}
	}
	// Same single source of truth as ToOpenAIError: the rendered message is
	// e.Err in every branch, so SetMessage reaches this wire too.
	result.Message = e.Error()
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
// upstream_rate_limit, upstream_insufficient_balance, insufficient_quota,
// internal.
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

	// OUR account is out of money at the provider. Split out from upstream_4xx,
	// which is the bucket meaning "the caller sent a bad request" — reading an
	// unpaid provider invoice as customer error is exactly the wrong conclusion
	// for whoever is on call, and this is the one failure class here that no
	// amount of retrying or failing over can fix.
	//
	// Not hypothetical: UAT's DeepSeek account balance is already negative as
	// of 2026-09-03, and production's single active route is the same provider.
	//
	// Both signals are safe only because the switch above has already returned:
	// every 402 newhub itself emits carries one of the caller-quota codes
	// (insufficient_user_quota / token_quota_exhausted / tenant_quota_exceeded),
	// so a 402 reaching this line came from upstream. Likewise the code strings
	// below are the provider's own — WithOpenAIError copies the upstream error
	// code verbatim into errorCode, so "insufficient_quota" here is OpenAI's
	// spelling and can never collide with ours ("insufficient_user_quota").
	if err.StatusCode == http.StatusPaymentRequired {
		return "upstream_insufficient_balance"
	}
	switch string(err.errorCode) {
	case "insufficient_quota", // OpenAI
		"billing_not_active", // several vendors
		"Arrearage":          // Alibaba / Tongyi
		return "upstream_insufficient_balance"
	}

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
