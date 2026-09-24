package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/pkg/errors"

	"github.com/gin-gonic/gin"
)

const KeyRequestBody = "key_request_body"

var ErrRequestBodyTooLarge = errors.New("request body too large")

func IsRequestBodyTooLargeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRequestBodyTooLarge) {
		return true
	}
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

func GetRequestBody(c *gin.Context) ([]byte, error) {
	cached, exists := c.Get(KeyRequestBody)
	if exists && cached != nil {
		if b, ok := cached.([]byte); ok {
			return b, nil
		}
	}
	maxMB := constant.MaxRequestBodyMB
	if maxMB < 0 {
		// no limit
		body, err := io.ReadAll(c.Request.Body)
		_ = c.Request.Body.Close()
		if err != nil {
			return nil, err
		}
		c.Set(KeyRequestBody, body)
		return body, nil
	}
	maxBytes := int64(maxMB) << 20

	limited := io.LimitReader(c.Request.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		_ = c.Request.Body.Close()
		if IsRequestBodyTooLargeError(err) {
			return nil, errors.Wrap(ErrRequestBodyTooLarge, fmt.Sprintf("request body exceeds %d MB", maxMB))
		}
		return nil, err
	}
	_ = c.Request.Body.Close()
	if int64(len(body)) > maxBytes {
		return nil, errors.Wrap(ErrRequestBodyTooLarge, fmt.Sprintf("request body exceeds %d MB", maxMB))
	}
	c.Set(KeyRequestBody, body)
	return body, nil
}

// ErrUnsupportedContentType is the sentinel behind the refusal
// UnmarshalBodyReusable now returns for a Content-Type it cannot parse. It is
// exported so a caller can branch on the CAUSE (errors.Is) instead of
// matching the wire text, which is customer-facing prose and may be reworded.
var ErrUnsupportedContentType = errors.New("unsupported content type")

// wireBodyError carries a customer-safe message on the wire while keeping the
// decoder's original error reachable through errors.Is/errors.As for
// server-side callers. Error() deliberately returns ONLY the safe message:
// every caller in this tree puts err.Error() straight into an HTTP response,
// which is how "json: cannot unmarshal number -5 into Go struct field
// GeneralOpenAIRequest.max_tokens of type uint" reached a customer.
type wireBodyError struct {
	message string
	cause   error
}

func (e *wireBodyError) Error() string { return e.message }
func (e *wireBodyError) Unwrap() error { return e.cause }

func UnmarshalBodyReusable(c *gin.Context, v any) error {
	requestBody, err := GetRequestBody(c)
	if err != nil {
		return err
	}
	//if DebugEnabled {
	//	println("UnmarshalBodyReusable request body:", string(requestBody))
	//}
	// Reset the request body on EVERY exit from here on, not only on success:
	// several callers deliberately ignore the error and keep going
	// (middleware/distributor.go's images/edits and audio branches), and after
	// this function started answering unsupported content types with an error
	// instead of silently doing nothing, the old success-only reset would have
	// left those requests with a drained body.
	defer func() { c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody)) }()
	contentType := c.Request.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		err = Unmarshal(requestBody, v)
	case strings.Contains(contentType, gin.MIMEPOSTForm):
		err = parseFormData(requestBody, v)
	case strings.Contains(contentType, gin.MIMEMultipartPOSTForm):
		err = parseMultipartFormData(c, requestBody, v)
	case strings.TrimSpace(contentType) == "" || len(requestBody) == 0:
		// Two deliberate carve-outs from the refusal below, because neither is
		// an unsupported type: a request that sends NO Content-Type at all
		// (every GET on a relay route reaches this parser that way), and a
		// request with nothing to parse. Both keep the historical no-op.
	default:
		// Previously "skip for now": the body was left unparsed and the caller
		// was told, further downstream, that the model name was missing —
		// which pointed at the wrong thing. A caller whose Content-Type this
		// parser cannot read gets that reason instead.
		err = unsupportedContentTypeError(contentType)
	}
	if err != nil {
		// The detail stays here, on the server side: the wire only gets the
		// sanitised sentence.
		SysLog("request body decode failed: content_type=" + contentType + ": " + err.Error())
		return sanitizeBodyDecodeError(err)
	}
	return nil
}

// sanitizeBodyDecodeError converts a body-decoding failure into something a
// customer can act on, and strips the Go vocabulary (struct names, package
// names, type names) the standard decoder puts in its errors.
//
// BLIND SPOT, stated so this is not read as a general-purpose scrubber: it
// classifies the two encoding/json error types it knows and falls back to a
// generic sentence for anything else that still LOOKS internal (the
// mentionsGoInternals check below is a substring test, not a proof). A custom
// UnmarshalJSON somewhere in the tree that returns its own error mentioning a
// type by a spelling that check does not list would still reach the wire.
func sanitizeBodyDecodeError(err error) error {
	if err == nil {
		return nil
	}
	var already *wireBodyError
	if errors.As(err, &already) {
		return err
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return &wireBodyError{message: describeJSONTypeError(typeErr), cause: err}
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		// Syntax errors ("unexpected end of JSON input", "invalid character
		// 'x' looking for beginning of value") describe the caller's OWN
		// bytes and name nothing internal, so they pass through unchanged.
		return &wireBodyError{message: syntaxErr.Error(), cause: err}
	}
	if mentionsGoInternals(err.Error()) {
		return &wireBodyError{message: "invalid request body", cause: err}
	}
	return err
}

// mentionsGoInternals reports whether a message contains the vocabulary the
// standard library uses for Go identifiers. Substring matching, so it is a
// net rather than a proof — see sanitizeBodyDecodeError's blind spot.
func mentionsGoInternals(msg string) bool {
	for _, marker := range []string{"Go struct field", "Go value of type", "json: ", " of type ", "reflect."} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// describeJSONTypeError names the offending field the way the CALLER wrote it
// (encoding/json's Field is the JSON path, not the Go field name) and the
// shape that was expected in JSON terms. It never renders e.Type's name —
// that is the Go type ("uint") the customer has no business seeing.
func describeJSONTypeError(e *json.UnmarshalTypeError) string {
	where := "in request body"
	if e.Field != "" {
		where = "for field " + strconv.Quote(e.Field)
	}
	got := e.Value
	if got == "" {
		got = "an unexpected value"
	}
	if want := jsonShapeName(e.Type); want != "" {
		return "invalid value " + where + ": expected " + want + ", got " + got
	}
	return "invalid value " + where + ": got " + got
}

// jsonShapeName maps a Go kind onto the JSON shape a caller would recognise.
// Returns "" for a kind with no natural JSON spelling, in which case the
// caller omits the "expected ..." clause rather than inventing one.
func jsonShapeName(t reflect.Type) string {
	if t == nil {
		return ""
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "an integer"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a non-negative integer"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.Map, reflect.Struct:
		return "an object"
	default:
		return ""
	}
}

// unsupportedContentTypeError echoes the caller's own media type back, after
// cutting parameters, non-printable bytes and length — the header is
// attacker-controlled and ends up in a JSON response and in the server log.
func unsupportedContentTypeError(contentType string) error {
	return &wireBodyError{
		message: "unsupported content type " + strconv.Quote(safeMediaType(contentType)) +
			"; expected application/json, application/x-www-form-urlencoded or multipart/form-data",
		cause: ErrUnsupportedContentType,
	}
}

// safeMediaType reduces a Content-Type header to its media type and keeps only
// printable ASCII, capped at 64 bytes.
func safeMediaType(contentType string) string {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	contentType = strings.TrimSpace(contentType)
	var b strings.Builder
	for i := 0; i < len(contentType) && b.Len() < 64; i++ {
		if ch := contentType[i]; ch >= 0x20 && ch < 0x7f {
			b.WriteByte(ch)
		}
	}
	return b.String()
}

func SetContextKey(c *gin.Context, key constant.ContextKey, value any) {
	c.Set(string(key), value)
}

func GetContextKey(c *gin.Context, key constant.ContextKey) (any, bool) {
	return c.Get(string(key))
}

func GetContextKeyString(c *gin.Context, key constant.ContextKey) string {
	return c.GetString(string(key))
}

func GetContextKeyInt(c *gin.Context, key constant.ContextKey) int {
	return c.GetInt(string(key))
}

func GetContextKeyBool(c *gin.Context, key constant.ContextKey) bool {
	return c.GetBool(string(key))
}

func GetContextKeyStringSlice(c *gin.Context, key constant.ContextKey) []string {
	return c.GetStringSlice(string(key))
}

func GetContextKeyStringMap(c *gin.Context, key constant.ContextKey) map[string]any {
	return c.GetStringMap(string(key))
}

func GetContextKeyTime(c *gin.Context, key constant.ContextKey) time.Time {
	return c.GetTime(string(key))
}

func GetContextKeyType[T any](c *gin.Context, key constant.ContextKey) (T, bool) {
	if value, ok := c.Get(string(key)); ok {
		if v, ok := value.(T); ok {
			return v, true
		}
	}
	var t T
	return t, false
}

func ApiError(c *gin.Context, err error) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": err.Error(),
	})
}

func ApiErrorMsg(c *gin.Context, msg string) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"message": msg,
	})
}

func ApiSuccess(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
}

func ParseMultipartFormReusable(c *gin.Context) (*multipart.Form, error) {
	requestBody, err := GetRequestBody(c)
	if err != nil {
		return nil, err
	}

	contentType := c.Request.Header.Get("Content-Type")
	boundary, err := parseBoundary(contentType)
	if err != nil {
		return nil, err
	}

	reader := multipart.NewReader(bytes.NewReader(requestBody), boundary)
	form, err := reader.ReadForm(multipartMemoryLimit())
	if err != nil {
		return nil, err
	}

	// Reset request body
	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	return form, nil
}

func processFormMap(formMap map[string]any, v any) error {
	jsonData, err := Marshal(formMap)
	if err != nil {
		return err
	}

	err = Unmarshal(jsonData, v)
	if err != nil {
		return err
	}

	return nil
}

func parseFormData(data []byte, v any) error {
	values, err := url.ParseQuery(string(data))
	if err != nil {
		return err
	}
	formMap := make(map[string]any)
	for key, vals := range values {
		if len(vals) == 1 {
			formMap[key] = vals[0]
		} else {
			formMap[key] = vals
		}
	}

	return processFormMap(formMap, v)
}

func parseMultipartFormData(c *gin.Context, data []byte, v any) error {
	contentType := c.Request.Header.Get("Content-Type")
	boundary, err := parseBoundary(contentType)
	if err != nil {
		if errors.Is(err, errBoundaryNotFound) {
			return Unmarshal(data, v) // Fallback to JSON
		}
		return err
	}

	reader := multipart.NewReader(bytes.NewReader(data), boundary)
	form, err := reader.ReadForm(multipartMemoryLimit())
	if err != nil {
		return err
	}
	defer form.RemoveAll()
	formMap := make(map[string]any)
	for key, vals := range form.Value {
		if len(vals) == 1 {
			formMap[key] = vals[0]
		} else {
			formMap[key] = vals
		}
	}

	return processFormMap(formMap, v)
}

var errBoundaryNotFound = errors.New("multipart boundary not found")

// parseBoundary extracts the multipart boundary from the Content-Type header using mime.ParseMediaType
func parseBoundary(contentType string) (string, error) {
	if contentType == "" {
		return "", errBoundaryNotFound
	}
	// Boundary-UUID / boundary-------xxxxxx
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", err
	}
	boundary, ok := params["boundary"]
	if !ok || boundary == "" {
		return "", errBoundaryNotFound
	}
	return boundary, nil
}

// multipartMemoryLimit returns the configured multipart memory limit in bytes
func multipartMemoryLimit() int64 {
	limitMB := constant.MaxFileDownloadMB
	if limitMB <= 0 {
		limitMB = 32
	}
	return int64(limitMB) << 20
}
