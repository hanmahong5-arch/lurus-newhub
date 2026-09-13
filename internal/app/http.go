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

// UpstreamHeadersNotForwarded lists response header names IOCopyBytesGracefully
// must not copy from the upstream vendor onto the client-facing response.
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
// package) is the cross-package lock that keeps the two lists in sync
// instead of a comment. http.Header canonicalizes header names (net/http,
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
			// avoid setting Content-Length, and skip the vendor's own
			// request-id headers so they do not clobber the gateway's (see
			// UpstreamHeadersNotForwarded).
			if k == "Content-Length" || UpstreamHeadersNotForwarded[k] {
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
