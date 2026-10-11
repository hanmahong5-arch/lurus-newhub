package entity

// KeyMeta is the operator-supplied description of one upstream key (account).
// It lives in ChannelInfo.MultiKeyMeta keyed by key index; a single-key
// channel uses index 0. Nothing here is secret.
type KeyMeta struct {
	Name      string   `json:"name,omitempty"`
	PlanKind  string   `json:"plan_kind,omitempty"`
	ExpiresAt int64    `json:"expires_at,omitempty"` // Unix seconds, 0 = never
	Tags      []string `json:"tags,omitempty"`
}

// DefaultKeyWeight is the weight of a key with no explicit entry in
// ChannelInfo.MultiKeyWeight. An explicit 0 is kept and means "never pick".
const DefaultKeyWeight = 50

// MaxKeyWeight is the upper bound accepted for a per-key weight.
const MaxKeyWeight = 100

// KeyWeight returns the effective weight of key idx (default when unset).
func (c *ChannelInfo) KeyWeight(idx int) int {
	if w, ok := c.MultiKeyWeight[idx]; ok {
		return w
	}
	return DefaultKeyWeight
}

// KeyProxy returns the per-key proxy override ("" = use the channel proxy).
func (c *ChannelInfo) KeyProxy(idx int) string {
	return c.MultiKeyProxy[idx]
}
