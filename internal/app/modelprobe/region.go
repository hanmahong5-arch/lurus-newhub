package modelprobe

import "strings"

// regionRefusalMarkers are the fragments an upstream puts in a geo-restriction
// refusal (OpenRouter's message text and the routing step recorded in its error
// metadata). Matching on these, not on the 403 status alone, is what keeps a
// bad-key 403 from being read as a per-model block.
var regionRefusalMarkers = []string{
	"not available in your region",
	"gate endpoints with geo restrictions",
}

// IsRegionRefusal reports whether a failed probe says the model is refused
// from our egress region. status is 0 when only stored error text is known.
func IsRegionRefusal(status int, text string) bool {
	if status != 0 && status != 403 {
		return false
	}
	lower := strings.ToLower(text)
	for _, m := range regionRefusalMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}
