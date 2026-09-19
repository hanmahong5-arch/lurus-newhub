package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// ValidateOutboundURL is the single SSRF gate that operator-supplied outbound
// targets (channel base_url, the upstream test/fetch endpoints) funnel
// through at write/config time, AND — since cycle-9 L4 — the two per-request
// task-media content routes funnel a vendor-supplied URL through at read
// time: video_proxy.go's VideoProxy (GET /v1/videos/:task_id/content) and
// task_media_guard.go's streamMediaContent (GET
// /v1/tasks/:platform/:task_id/artifacts/:key/content). It applies the
// system fetch_setting with two egress-specific adjustments:
//
//   - The port allow-list is NOT enforced (nil ports → all ports allowed): LLM
//     providers legitimately listen on arbitrary ports, and a port is not a
//     trust boundary — an internal service is blocked by the IP/host rule
//     regardless of its port. Enforcing the fetch default {80,443,8080,8443}
//     would break legitimate custom-port channels for no SSRF gain.
//
//   - apply_ip_filter_for_domain is FORCED ON in the default blacklist posture,
//     so an internal DNS name (*.svc, *.cluster.local) that resolves to a
//     private address is rejected like a private-IP literal. It is NOT forced
//     when the operator runs IP-whitelist mode (ip_filter_mode), because there
//     forcing it would resolve every external domain and reject it unless its
//     (CDN, rotating) IP is whitelisted — a regression; a whitelist operator
//     has already taken explicit control of egress IPs.
//
// Validation fails CLOSED on DNS-resolution error: a host that cannot be
// resolved at check time cannot be vetted, and treating that as a pass
// would let a name that is NXDOMAIN now but internal later (attacker-controlled
// authoritative DNS, or delayed record creation) slip through. For the
// write/config-time callers (channel base_url, upstream test/fetch) this is
// not latency-critical, so a transient blip merely asks the operator to
// retry. The two task-media content routes are a different cost: each is a
// per-request, on-the-hot-path call, so each request pays one DNS lookup
// for the resolved vendor URL and gets a 502 if that lookup fails —
// including for a channel configured with its own outbound proxy (see
// video_proxy.go's egress-check comment for that specific case). (TTL-based
// rebinding on the relay hot path is a separate, transport-layer concern
// tracked in the hardening plan.)
//
// Operators that intentionally route to in-cluster inference set
// allow_private_ip=true or allow-list the host/IP. Empty input is a no-op;
// callers decide whether an empty base_url is legal.
func ValidateOutboundURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	// One snapshot, taken under the configuration read lock, for the whole
	// decision: reading the live struct field by field could mix a filter mode
	// from before an option-sync tick with a list from after it, and such a
	// mixture can admit a host that both of the published configurations
	// reject — ssrf_guard_race_test.go exhibits a pair where either mixture
	// does.
	fs := system_setting.GetFetchSettingSnapshot()
	applyIPFilterForDomain := fs.ApplyIPFilterForDomain || !fs.IpFilterMode
	return common.ValidateURLWithFetchSetting(
		rawURL,
		fs.EnableSSRFProtection,
		fs.AllowPrivateIp,
		fs.DomainFilterMode,
		fs.IpFilterMode,
		fs.DomainList,
		fs.IpList,
		nil, // ports are not a trust boundary for channel egress (see doc above)
		applyIPFilterForDomain,
	)
}

// ValidateOutboundProxy validates a channel proxy target. A proxy may use a
// non-HTTP scheme (socks5://, socks5h://) or the bare host:port form some
// dialers accept; the SSRF validator only understands http/https, so we
// validate the proxy's host:port under an http:// envelope — the SSRF decision
// is about the destination address, not the wire protocol.
func ValidateOutboundProxy(rawProxy string) error {
	rawProxy = strings.TrimSpace(rawProxy)
	if rawProxy == "" {
		return nil
	}
	host := proxyHost(rawProxy)
	if host == "" {
		return fmt.Errorf("invalid proxy URL")
	}
	return ValidateOutboundURL("http://" + host)
}

// proxyHost extracts host:port from a proxy value in scheme://host:port,
// //host:port, or bare host:port form, returning "" when no host can be found.
func proxyHost(rawProxy string) string {
	if u, err := url.Parse(rawProxy); err == nil && u.Host != "" {
		return u.Host
	}
	// Bare "host:port" fails url.Parse (leading segment cannot contain a colon).
	// Re-parse under an authority prefix so a scheme-less proxy is still vetted
	// rather than rejected (and, more importantly, not silently trusted).
	if u, err := url.Parse("//" + rawProxy); err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}
