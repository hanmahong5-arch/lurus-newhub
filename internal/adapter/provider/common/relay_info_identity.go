package common

import (
	"encoding/json"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// relay_info_identity.go — request-identity derivation helpers, moved out of
// relay_info.go unchanged (source-size ratchet).

// deriveSessionId reads X-Session-Id and bounds what it may contain: bytes
// must be printable ASCII (0x20-0x7E) and the value at most 200 bytes.
// Anything outside that comes back "" — the field is descriptive metadata on
// the log row, not a trust boundary, but it must not carry control
// characters or an unbounded blob into JSON/log rendering.
func deriveSessionId(c *gin.Context) string {
	raw := strings.TrimSpace(c.GetHeader("X-Session-Id"))
	if raw == "" || len(raw) > 200 {
		return ""
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 || raw[i] > 0x7E {
			return ""
		}
	}
	return raw
}

// deriveEndUserHash extracts the caller's own end-user identifier from the
// request body — OpenAI's `user` field or Anthropic-wire `metadata.user_id`
// — and returns a tenant-scoped, non-reversible hash of it. The raw value is
// never returned or stored anywhere; "" when the request carries no such
// field.
func deriveEndUserHash(c *gin.Context, tenantID string, request dto.Request) string {
	var raw string
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		raw = r.User
	case *dto.OpenAIResponsesRequest:
		raw = r.User
	case *dto.ClaudeRequest:
		if len(r.Metadata) > 0 {
			var meta dto.ClaudeMetadata
			if err := json.Unmarshal(r.Metadata, &meta); err == nil {
				raw = meta.UserId
			}
		}
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	hash := common.GenerateHMAC("end_user|" + tenantID + "|" + raw)
	if len(hash) > 16 {
		hash = hash[:16]
	}
	return hash
}
