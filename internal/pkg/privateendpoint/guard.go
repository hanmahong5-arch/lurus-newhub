// Package privateendpoint enforces the one property that makes the
// "private inference" channel type worth its name: a channel of that type may
// only ever dispatch to an address inside the customer's own network.
//
// This is the exact mirror image of an SSRF guard, with the verdict inverted.
// An SSRF guard REJECTS private/loopback targets because a public service must
// not be tricked into reaching intranet resources. Here the threat model is
// reversed: the customer's prompt data must not leave their network, so a
// private-inference channel REJECTS every PUBLICLY routable target and admits
// only intranet ones. Same address classification, opposite allow-list — do
// not "unify" the two by reusing one predicate; they will drift into each
// other's failure mode.
//
// The guard runs in two places (see callers):
//
//  1. Config time — when a channel is created/updated, so a misconfiguration
//     is refused with an actionable message instead of silently shipping
//     prompts to a SaaS provider.
//  2. Dispatch time — fail-closed on every relayed request, because config
//     validation can be bypassed (seeded/edited rows in the DB write straight
//     past the HTTP handler). Only the private-endpoint channel type pays this
//     cost; every other channel type is untouched.
package privateendpoint

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
)

// ExtraHostsEnv names the escape hatch for deployments whose intranet
// inference host resolves through a corporate DNS name that is not covered by
// the intranet-suffix table below (e.g. `inference.corp.example.com` served
// only inside a VPN). Comma-separated exact hostnames.
//
// Deliberately opt-in and explicit rather than a pattern: a wildcard here
// would quietly re-open the egress hole this package exists to close, and the
// operator who adds an entry is stating "I know this name is internal".
const ExtraHostsEnv = "PRIVATE_ENDPOINT_EXTRA_HOSTS"

// carrierGradeNAT is RFC 6598 (100.64.0.0/10) — shared address space used by
// Tailscale and other overlay VPNs to number intranet peers. `netip.Addr.IsPrivate`
// does NOT include it, but reaching a Tailscale peer keeps traffic inside the
// customer's own tailnet, so for this guard's purpose it counts as intranet.
var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

// intranetSuffixes are DNS name shapes that cannot resolve on the public
// internet, so a host ending in one is intranet by construction.
var intranetSuffixes = []string{
	".local",             // mDNS / Bonjour
	".localhost",         //
	".internal",          // common convention; also GCP internal DNS
	".intranet",          //
	".corp",              //
	".lan",               //
	".home.arpa",         // RFC 8375
	".svc",               // Kubernetes short service form
	".svc.cluster.local", // Kubernetes FQDN
	".cluster.local",     //
}

// Verdict explains a classification so callers can log or surface WHY an
// address counted as intranet — evidence, not just a boolean.
type Verdict struct {
	// Intranet is true when the host is provably inside a private network.
	Intranet bool
	// Host is the hostname or IP literal that was classified.
	Host string
	// Reason names the rule that decided it (e.g. "RFC1918 private IPv4",
	// "kubernetes .svc suffix", "public IPv4 — egress would leave the network").
	Reason string
}

// ClassifyBaseURL parses a channel base URL and classifies its host.
//
// It performs NO DNS resolution: a name-based verdict must not depend on what
// a resolver happens to answer at validation time (an attacker-controlled or
// merely flaky DNS answer would otherwise flip the verdict). Names are judged
// structurally — by intranet suffix, single-label shape, or the explicit
// operator allow-list.
func ClassifyBaseURL(rawURL string) (Verdict, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return Verdict{}, fmt.Errorf("base URL is empty: a private-inference channel must name the intranet endpoint explicitly (there is no public default to fall back to)")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return Verdict{}, fmt.Errorf("base URL %q is not a valid URL: %w", trimmed, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Verdict{}, fmt.Errorf("base URL %q must use http or https (got scheme %q)", trimmed, parsed.Scheme)
	}
	host := parsed.Hostname()
	if host == "" {
		return Verdict{}, fmt.Errorf("base URL %q has no host", trimmed)
	}

	return classifyHost(host), nil
}

// classifyHost applies the address rules to a bare host (no scheme, no port).
func classifyHost(host string) Verdict {
	lower := strings.ToLower(strings.TrimSuffix(host, "."))

	// Explicit operator allow-list wins — it is the documented escape hatch
	// for corporate DNS names that look public but are not.
	for _, allowed := range extraHosts() {
		if lower == allowed {
			return Verdict{Intranet: true, Host: host, Reason: "explicitly allowed via " + ExtraHostsEnv}
		}
	}

	// IP literal: classify by address, which is exact.
	if addr, err := netip.ParseAddr(lower); err == nil {
		return classifyAddr(host, addr)
	}
	// Bracketed IPv6 that survived Hostname() with brackets stripped is handled
	// above; a remaining parse failure means this is a DNS name.

	if lower == "localhost" {
		return Verdict{Intranet: true, Host: host, Reason: "localhost"}
	}
	for _, suffix := range intranetSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return Verdict{Intranet: true, Host: host, Reason: "non-routable DNS suffix " + suffix}
		}
	}
	// A single-label name (no dot) cannot be resolved on the public internet;
	// it only means something to an intranet resolver or a hosts file.
	if !strings.Contains(lower, ".") {
		return Verdict{Intranet: true, Host: host, Reason: "single-label hostname (intranet-only by construction)"}
	}

	return Verdict{
		Intranet: false,
		Host:     host,
		Reason: fmt.Sprintf(
			"%q is a publicly resolvable DNS name — prompts would leave the customer network; "+
				"use an intranet address, or add the name to %s if it is internal-only",
			host, ExtraHostsEnv,
		),
	}
}

// classifyAddr classifies an IP literal.
func classifyAddr(host string, addr netip.Addr) Verdict {
	unmapped := addr.Unmap()
	switch {
	case unmapped.IsLoopback():
		return Verdict{Intranet: true, Host: host, Reason: "loopback address (never leaves the host)"}
	case unmapped.IsPrivate():
		// RFC1918 for IPv4, RFC4193 unique-local (fc00::/7) for IPv6.
		return Verdict{Intranet: true, Host: host, Reason: "private address space (RFC1918 / RFC4193)"}
	case carrierGradeNAT.Contains(unmapped):
		return Verdict{Intranet: true, Host: host, Reason: "RFC6598 shared address space (overlay VPN peer, e.g. tailnet)"}
	case unmapped.IsLinkLocalUnicast():
		return Verdict{Intranet: true, Host: host, Reason: "link-local address"}
	case unmapped.IsUnspecified():
		// 0.0.0.0 / :: as a *destination* is not a real endpoint. Refuse rather
		// than guess: silently treating it as loopback would let a typo look
		// like a working private channel.
		return Verdict{Intranet: false, Host: host, Reason: "unspecified address (0.0.0.0 / ::) is not a reachable endpoint — name the intranet host explicitly"}
	}
	return Verdict{
		Intranet: false,
		Host:     host,
		Reason:   fmt.Sprintf("%s is publicly routable — prompts would leave the customer network", host),
	}
}

// ValidateBaseURL is the fail-closed gate: nil error means the URL provably
// stays inside the customer's network.
func ValidateBaseURL(rawURL string) error {
	verdict, err := ClassifyBaseURL(rawURL)
	if err != nil {
		return err
	}
	if !verdict.Intranet {
		return fmt.Errorf("private-inference endpoint rejected: %s", verdict.Reason)
	}
	return nil
}

// extraHosts reads and normalizes the operator allow-list. Read per call
// (not cached) so an operator can fix a deployment by editing env + restarting
// without a rebuild, and so tests do not leak state between cases.
func extraHosts() []string {
	raw := os.Getenv(ExtraHostsEnv)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		host := strings.ToLower(strings.TrimSpace(p))
		// Tolerate an operator pasting a URL or a host:port instead of a bare host.
		if strings.Contains(host, "://") {
			if u, err := url.Parse(host); err == nil && u.Hostname() != "" {
				host = u.Hostname()
			}
		} else if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
			host = h
		}
		if host != "" {
			out = append(out, host)
		}
	}
	return out
}
