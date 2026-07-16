package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// ValidateOutboundURL is the single SSRF gate that operator-supplied outbound
// targets (channel base_url, the upstream test/fetch endpoints) funnel through.
// It applies the system fetch_setting with two egress-specific adjustments:
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
// resolved at write/sink time cannot be vetted, and treating that as a pass
// would let a name that is NXDOMAIN now but internal later (attacker-controlled
// authoritative DNS, or delayed record creation) slip through. Write/sink time
// is not latency-critical, so a transient blip merely asks the operator to
// retry. (TTL-based rebinding on the relay hot path is a separate,
// transport-layer concern tracked in the hardening plan.)
//
// Operators that intentionally route to in-cluster inference set
// allow_private_ip=true or allow-list the host/IP. Empty input is a no-op;
// callers decide whether an empty base_url is legal.
func ValidateOutboundURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	fs := system_setting.GetFetchSetting()
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
