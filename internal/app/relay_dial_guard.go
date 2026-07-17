package app

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// relay_dial_guard.go — transport-layer SSRF defense for the relay egress client
// (hardening plan R1). Channel base_url / proxy are tenant-admin-controllable and
// are SSRF-validated at write time; the relay hot path does NOT re-resolve per
// request, so a TTL-based DNS-rebinding target that passed write-time validation,
// or any internal address reached via a direct (NO_PROXY) dial, would otherwise
// still connect. This guard re-checks the *resolved* destination at connection
// time and refuses dials to internal addresses.
//
// It enforces exactly the operator's existing SSRF private-IP policy
// (system_setting.FetchSetting): a no-op when EnableSSRFProtection is off or
// AllowPrivateIp is on, so it never blocks a target that write-time validation
// would have accepted — i.e. it adds no new failure mode relative to the
// currently-effective policy, only extends its enforcement to dial time.
//
// Scope & residuals (honest):
//   - Applies to the DEFAULT transport (channels with no explicit proxy). When a
//     channel sets its own proxy, or the egress env proxy applies, the dial
//     targets the proxy and that proxy is the egress control point — the proxy
//     address itself is exempted here (it is legitimately private in-cluster).
//   - Domain/IP allow/deny *list* modes are NOT applied at dial time (they are a
//     write-time concern and a whitelist would wrongly block external LLM
//     domains); only the private/internal-IP rule is enforced.
//   - Non-pinning: the resolved IPs are validated, then the original address is
//     dialed (preserving DNS failover). This closes static-internal and
//     already-in-effect rebinding, leaving only the narrow window of a rebind
//     landing between this resolve and the OS's dial resolve. Write-time
//     validation remains the primary gate.

// relayEgressLookupIPAddr is the resolver used by the dial guard; a package var
// so tests can inject deterministic results without real DNS.
var relayEgressLookupIPAddr = net.DefaultResolver.LookupIPAddr

// egressProxyHosts returns the set of host names of the configured egress
// proxies (HTTP(S)_PROXY / ALL_PROXY). Dials whose target host is one of these
// are exempted from validation: the transport is connecting to the proxy, not to
// the relay destination, and the in-cluster egress proxy is legitimately private.
func egressProxyHosts() map[string]struct{} {
	hosts := make(map[string]struct{})
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		// Proxy env may be "host:port" or a full URL; url.Parse of a bare
		// host:port yields no Hostname, so fall back to that form.
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			hosts[u.Hostname()] = struct{}{}
			continue
		}
		if h, _, err := net.SplitHostPort(raw); err == nil {
			hosts[h] = struct{}{}
		}
	}
	return hosts
}

// workerEgressHost returns the host of the operator-configured egress worker, or
// "" when no worker is set. Parsed per call because WorkerUrl is runtime mutable.
func workerEgressHost() string {
	raw := strings.TrimSpace(system_setting.WorkerUrl)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	if h, _, err := net.SplitHostPort(raw); err == nil {
		return h
	}
	return raw
}

// checkRelayEgress enforces the private-IP SSRF policy for a single dial target
// addr ("host:port"). It returns nil (allow) when protection is disabled, private
// access is permitted, the target is the egress proxy, or every resolved address
// is external; it returns an error (block) when the target resolves to an
// internal address. A resolver error is fail-open: the subsequent dial will fail
// on its own if the host is truly unresolvable, and write-time validation is the
// primary gate — refusing here would turn transient DNS hiccups into hard relay
// failures on the hot path.
func checkRelayEgress(ctx context.Context, addr string, proxyHosts map[string]struct{}) error {
	fs := system_setting.GetFetchSetting()
	if !fs.EnableSSRFProtection || fs.AllowPrivateIp {
		return nil
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if _, ok := proxyHosts[host]; ok {
		return nil
	}
	// The operator-configured worker (system_setting.WorkerUrl) is a trusted
	// egress relay, same trust level as the env proxy. Read per-dial because it
	// is runtime-configurable and empty by default. Exempting it keeps a legit
	// internal worker reachable while user-controlled sinks stay guarded.
	if wh := workerEgressHost(); wh != "" && wh == host {
		return nil
	}

	if ip := net.ParseIP(host); ip != nil {
		if common.IsPrivateOrInternalIP(ip) {
			return fmt.Errorf("relay egress to internal address %s blocked by SSRF guard", host)
		}
		return nil
	}

	ips, err := relayEgressLookupIPAddr(ctx, host)
	if err != nil {
		// Deliberate fail-open: a resolver error means the dial will fail on its
		// own, and write-time validation is the primary gate; refusing here would
		// turn transient DNS hiccups into hard relay failures on the hot path.
		return nil //nolint:nilerr // fail-open on resolver error (see comment above)
	}
	for _, ipa := range ips {
		if common.IsPrivateOrInternalIP(ipa.IP) {
			return fmt.Errorf("relay egress: %s resolves to internal address %s, blocked by SSRF guard", host, ipa.IP)
		}
	}
	return nil
}

// newRelayGuardedDialContext wraps base with the SSRF egress check. The proxy
// host set is captured once at construction (proxy env is process-level config,
// not per-request), keeping the hot-path dial allocation-free on the allow path.
func newRelayGuardedDialContext(base *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	proxyHosts := egressProxyHosts()
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if err := checkRelayEgress(ctx, addr, proxyHosts); err != nil {
			return nil, err
		}
		return base.DialContext(ctx, network, addr)
	}
}
