package app

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// requestCausedMarkers are message fragments (lower case) that identify a 429
// the caller provoked with an oversized request rather than a supply limit.
var requestCausedMarkers = []string{
	"request too large",
	"must be reduced",
	"reduce the length",
	"maximum context",
}

var (
	limitRe     = regexp.MustCompile(`(?i)limit\s*:?\s*(\d+)`)
	requestedRe = regexp.MustCompile(`(?i)requested\s*:?\s*(\d+)`)
)

// IsRequestCaused429 reports whether apiErr is a 429 caused by the request
// itself: the message names a size problem, or the error type is "tokens" and
// the requested amount exceeds the limit. Such an error is neither a reason to
// cool a channel nor to retry on another one; it goes back to the caller.
func IsRequestCaused429(apiErr *types.NewAPIError) bool {
	if apiErr == nil || apiErr.StatusCode != http.StatusTooManyRequests {
		return false
	}
	oaiErr := apiErr.ToOpenAIError()
	text := strings.ToLower(oaiErr.Message + " " + apiErr.UpstreamBodyHint)
	for _, m := range requestCausedMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	if strings.EqualFold(oaiErr.Type, "tokens") {
		lm, rm := limitRe.FindStringSubmatch(text), requestedRe.FindStringSubmatch(text)
		if lm != nil && rm != nil {
			limit, e1 := strconv.ParseInt(lm[1], 10, 64)
			req, e2 := strconv.ParseInt(rm[1], 10, 64)
			return e1 == nil && e2 == nil && req > limit
		}
	}
	return false
}
