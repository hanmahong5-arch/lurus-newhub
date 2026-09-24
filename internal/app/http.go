package app

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"

	"github.com/gin-gonic/gin"
)

func CloseResponseBodyGracefully(httpResponse *http.Response) {
	if httpResponse == nil || httpResponse.Body == nil {
		return
	}
	err := httpResponse.Body.Close()
	if err != nil {
		common.SysError("failed to close response body: " + err.Error())
	}
}

// UpstreamHeadersForwarded is the EXPLICIT allow-list of upstream response
// headers IOCopyBytesGracefully copies onto the client-facing response.
// Everything else the vendor sent is dropped.
//
// It used to be the other way round — copy everything except a five-name
// deny-list — and a live relay response therefore carried the upstream
// edge's Server, Eo-Cache-Status, Eo-Log-Uuid, X-Ds-Trace-Id and
// Strict-Transport-Security. That told the customer which vendor we routed
// to, handed them the vendor CDN's log id, and applied an upstream
// transport-security policy to our own domain. A deny-list cannot be
// complete: every vendor invents its own header names.
//
// Why each name stays (names are in canonical http.Header form, or they
// would never match a key in src.Header):
//
//	Content-Type        — without it the client cannot parse the body at
//	                      all, and it is not always JSON: the speech-to-text
//	                      handler (provider/openai/audio.go OpenaiSTTHandler)
//	                      passes the upstream body through verbatim for
//	                      whatever response_format the caller asked for, so
//	                      the upstream's own content type is the only
//	                      description of those bytes we have.
//	Content-Encoding    — if a body ever arrives still encoded, dropping the
//	                      label hands the client undecodable bytes. In this
//	                      codebase it should never be present: Accept-Encoding
//	                      is never forwarded upstream (provider/api_request.go
//	                      skips it even when a channel sets it as a header
//	                      override), so Go's transport negotiates and
//	                      transparently decompresses, deleting the header. It
//	                      is allow-listed for the case that stops being true,
//	                      not because it is observed.
//	Content-Disposition — a body that is a file, not a document, names itself
//	                      here and a client uses that to name the download.
//	                      Defensive rather than observed: every call site of
//	                      this function today hands over JSON or transcription
//	                      text, so nothing is known to send it; dropping it
//	                      would silently rename a future download.
//	Retry-After         — actionable on an upstream 429/503 that is passed
//	                      through with its status code. No collision with our
//	                      own limits: middleware/rate-limit.go writes its
//	                      Retry-After on a request it then aborts, which never
//	                      reaches a relay handler and so never reaches here.
//
// Deliberately NOT allow-listed, beyond the leak names above: Cache-Control,
// Vary, ETag and friends. Those are OUR domain's policy to set, for the same
// reason Strict-Transport-Security must not be inherited — an upstream must
// not decide how our responses are cached or pinned. If a route needs one,
// it should set it itself rather than inherit whatever the vendor sent.
var UpstreamHeadersForwarded = map[string]bool{
	"Content-Type":        true,
	"Content-Encoding":    true,
	"Content-Disposition": true,
	"Retry-After":         true,
}

// UpstreamHeadersNotForwarded lists response header names that must never
// reach the client-facing response from the upstream vendor.
//
// Since the filter above became an allow-list it is no longer what does the
// dropping — UpstreamHeadersForwarded does, and none of these names is in
// it. It remains as the negative half of the contract: a name here must
// never be added to the allow-list (asserted by
// TestUpstreamHeaderLists_DenyListStaysDisjointFromAllowList), and it is the
// subject the provider package's cross-package lock compares its own capture
// list against.
//
// X-Request-Id/X-Oneapi-Request-Id are the gateway's own minted id
// (middleware.RequestId sets them on c.Writer before this runs); some
// vendors send their own value under the identically-named "X-Request-Id"
// (the OpenAI-wire convention), which would otherwise silently overwrite it
// here. The remaining names are the ones
// provider.upstreamRequestIdHeaders already captures into
// other.upstream_request_id for admins — forwarding them raw would just
// duplicate that value under a second, undocumented channel with no tier
// gate. provider imports this package, so the reverse import would cycle —
// TestUpstreamRequestIdHeaders_AllSkippedFromClientResponse (provider
// package) asserts the capture list is a subset of this one — one
// direction; a name dropped from the capture list is not caught —
// rather than a comment. http.Header canonicalizes header names (net/http,
// textproto), so these must be written in canonical form to match
// src.Header's keys.
var UpstreamHeadersNotForwarded = map[string]bool{
	"X-Request-Id":        true,
	"X-Oneapi-Request-Id": true,
	"Request-Id":          true,
	"Openai-Request-Id":   true,
	"Cf-Ray":              true,
}

func IOCopyBytesGracefully(c *gin.Context, src *http.Response, data []byte) {
	if c.Writer == nil {
		return
	}

	body := io.NopCloser(bytes.NewBuffer(data))

	// We shouldn't set the header before we parse the response body, because the parse part may fail.
	// And then we will have to send an error response, but in this case, the header has already been set.
	// So the httpClient will be confused by the response.
	// For example, Postman will report error, and we cannot check the response at all.
	if src != nil {
		for k, v := range src.Header {
			// Allow-list, not deny-list: everything the vendor sent that is
			// not named in UpstreamHeadersForwarded is dropped. Content-Length
			// is excluded there too — it is recomputed from data just below,
			// because several callers hand us a re-serialised body whose
			// length no longer matches the upstream's.
			if !UpstreamHeadersForwarded[k] {
				continue
			}
			c.Writer.Header().Set(k, v[0])
		}
	}

	// set Content-Length header manually BEFORE calling WriteHeader
	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Write header with status code (this sends the headers)
	if src != nil {
		c.Writer.WriteHeader(src.StatusCode)
	} else {
		c.Writer.WriteHeader(http.StatusOK)
	}

	_, err := io.Copy(c.Writer, body)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("failed to copy response body: %s", err.Error()))
	}
	c.Writer.Flush()
}
