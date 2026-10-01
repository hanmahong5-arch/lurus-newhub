package systemone

const (
	// ChannelName identifies the System One channels (hosted TypeSafe and
	// self-hosted compatible servers share one adaptor).
	ChannelName = "systemone"

	// requestPath is appended to the channel base URL; the official SDK
	// does the same, so the base must be the host root without /v1.
	requestPath = "/v1/systemone"

	// maxResponseBytes caps how much of a 200 body is read; a legitimate answer
	// is a few KiB, so anything near this is a misbehaving upstream.
	maxResponseBytes = 4 << 20
)

// ModelList is the public model names. Upstream names come from the channel's
// model_mapping, so no checkpoint name is hard-coded here.
var ModelList = []string{
	// TypeSafe (hosted)
	"jev-latest",
	"jev-preview",
	"jev-1.13.0",
	// System One compatible (self-hosted)
	"laya-auto",
	"laya-english",
	"laya-multilingual",
	"laya-typed-decisions",
}
