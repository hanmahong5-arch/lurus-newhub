package taskopaque

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

const prefix = "tht_"

// Encode returns a Hub-issued opaque task handle for the tasks table
// primary key. Polling GET /v1/tasks/{opaque} accepts only these handles
// (TokenHub 0.10 shape); vendor task ids keep using /v1/tasks/:platform/:id.
func Encode(dbID int64) string {
	if dbID <= 0 {
		return ""
	}
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(dbID))
	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	_, _ = mac.Write(raw[:])
	sum := mac.Sum(nil)[:16]
	payload := make([]byte, 0, 8+16)
	payload = append(payload, raw[:]...)
	payload = append(payload, sum...)
	return prefix + base64.RawURLEncoding.EncodeToString(payload)
}

// Decode verifies and extracts the tasks.id. ok=false for wrong prefix,
// bad encoding, or a forged / truncated MAC.
func Decode(opaque string) (dbID int64, ok bool) {
	if !strings.HasPrefix(opaque, prefix) {
		return 0, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(opaque, prefix))
	if err != nil || len(raw) != 24 {
		return 0, false
	}
	mac := hmac.New(sha256.New, []byte(common.CryptoSecret))
	_, _ = mac.Write(raw[:8])
	sum := mac.Sum(nil)[:16]
	if !hmac.Equal(sum, raw[8:]) {
		return 0, false
	}
	u := binary.BigEndian.Uint64(raw[:8])
	if u == 0 || u > uint64(^uint64(0)>>1) {
		return 0, false
	}
	return int64(u), true
}
