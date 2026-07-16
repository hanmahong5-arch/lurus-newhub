package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// ValidateOutboundURL is the single SSRF gate that every operator-supplied
// outbound target (channel base_url, the upstream test/fetch endpoints) funnels
// through, so a newly added sink cannot silently bypass the system fetch policy.
//
// It applies the system fetch_setting but forces apply_ip_filter_for_domain, so
// an internal DNS name (e.g. *.svc, *.cluster.local) that resolves to a private
// address is rejected the same as a private-IP literal — the default
// domain-only pass would let such names reach in-cluster services. Operators
// that intentionally route to in-cluster inference set allow_private_ip=true or
// allow-list the host/IP.
//
// A DNS-resolution failure is treated as a pass: the relay client shares this
// pod's resolver, so a host the validator cannot resolve is equally unreachable
// by the actual request — failing closed here would only reject legitimate
// external channels on a transient lookup blip. (TTL-based DNS rebinding is a
// separate, transport-layer concern tracked in the hardening plan.)
//
// Empty input is a no-op; callers decide whether an empty base_url is legal.
func ValidateOutboundURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	fs := system_setting.GetFetchSetting()
	err := common.ValidateURLWithFetchSetting(
		rawURL,
		fs.EnableSSRFProtection,
		fs.AllowPrivateIp,
		fs.DomainFilterMode,
		fs.IpFilterMode,
		fs.DomainList,
		fs.IpList,
		// Deliberately do NOT apply the fetch policy's port allow-list to channel
		// egress: LLM providers legitimately listen on arbitrary ports (Ollama
		// 11434, vLLM 8000, self-hosted gateways, …), and a port is not a trust
		// boundary — an internal service is already blocked by the IP/host rule
		// regardless of its port. Enforcing the fetch default {80,443,8080,8443}
		// here would break legitimate custom-port channels without adding
		// meaningful SSRF protection.
		nil,
		true, // force IP filtering for domains: block internal DNS → private IP
	)
	if err != nil && strings.Contains(err.Error(), "DNS resolution failed") {
		return nil
	}
	return err
}

// ValidateOutboundProxy validates a channel proxy target. A proxy may use a
// non-HTTP scheme (socks5://, socks5h://); the SSRF validator only understands
// http/https, so we validate the proxy's host:port under an http:// envelope —
// the SSRF decision is about the destination address, not the wire protocol.
func ValidateOutboundProxy(rawProxy string) error {
	if strings.TrimSpace(rawProxy) == "" {
		return nil
	}
	u, err := url.Parse(rawProxy)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid proxy URL")
	}
	return ValidateOutboundURL("http://" + u.Host)
}
