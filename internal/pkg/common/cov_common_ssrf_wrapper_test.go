package common

import (
	"net"
	"testing"
)

// TestIsPrivateOrInternalIP_MatchesInternalIsPrivateIP asserts the exported
// dial-guard wrapper (used by the relay transport layer) stays byte-for-byte
// in sync with the internal isPrivateIP notion used by write-time channel
// validation — the whole point of the wrapper's existence per its docstring.
// A stub that always returned false would let every case below through.
func TestIsPrivateOrInternalIP_MatchesInternalIsPrivateIP(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{"loopback v4", "127.0.0.1", true},
		{"loopback v6", "::1", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},
		{"link-local v4", "169.254.1.1", true},
		{"rfc1918 10/8", "10.1.2.3", true},
		{"rfc1918 172.16/12", "172.16.5.5", true},
		{"rfc1918 192.168/16", "192.168.1.1", true},
		{"cgnat tailscale range", "100.64.0.5", true},
		{"multicast", "224.0.0.1", true},
		{"ipv6 unique-local fd", "fd00::1", true},
		{"ipv6 link-local fe80", "fe80::1", true},
		{"public v4 dns", "8.8.8.8", false},
		{"public v6", "2001:4860:4860::8888", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("test bug: %q did not parse as an IP", tc.ip)
			}
			got := IsPrivateOrInternalIP(ip)
			if got != tc.want {
				t.Errorf("IsPrivateOrInternalIP(%s) = %v, want %v", tc.ip, got, tc.want)
			}
			// The exported wrapper must be a pure pass-through: never diverge from
			// the package-internal check the write-time validator also uses.
			if internalWant := isPrivateIP(ip); got != internalWant {
				t.Errorf("IsPrivateOrInternalIP(%s) = %v diverges from isPrivateIP = %v", tc.ip, got, internalWant)
			}
		})
	}
}
