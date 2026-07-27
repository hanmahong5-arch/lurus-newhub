package privateendpoint

import (
	"strings"
	"testing"
)

func TestClassifyBaseURL_IntranetTargetsAccepted(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"loopback v4", "http://127.0.0.1:11434"},
		{"loopback name", "http://localhost:8080/v1"},
		{"loopback v6", "http://[::1]:11434"},
		{"rfc1918 10/8", "http://10.0.3.14:8000/v1"},
		{"rfc1918 172.16/12", "https://172.20.5.9/v1"},
		{"rfc1918 192.168/16", "http://192.168.1.50:11434"},
		{"ipv6 unique local", "http://[fd00::5]:8000"},
		{"tailnet cgnat", "http://100.101.102.103:11434"},
		{"link local", "http://169.254.10.9:8000"},
		{"k8s service fqdn", "http://vllm.lurus-inference.svc.cluster.local:8000/v1"},
		{"k8s short form", "http://vllm.lurus-inference.svc:8000"},
		{"mdns", "http://gpubox.local:11434"},
		{"single label host", "http://gpubox:11434/v1"},
		{"corp suffix", "https://inference.corp/v1"},
		{"home.arpa", "http://ai.home.arpa:8080"},
		{"trailing dot tolerated", "http://gpubox.local.:11434"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict, err := ClassifyBaseURL(tc.url)
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.url, err)
			}
			if !verdict.Intranet {
				t.Fatalf("%s must classify as intranet, got reason: %s", tc.url, verdict.Reason)
			}
			if verdict.Reason == "" {
				t.Fatal("an accepted verdict must still explain WHY (it is the demo's evidence line)")
			}
			if err := ValidateBaseURL(tc.url); err != nil {
				t.Fatalf("ValidateBaseURL disagreed with ClassifyBaseURL: %v", err)
			}
		})
	}
}

func TestClassifyBaseURL_PublicTargetsRejected(t *testing.T) {
	// Every one of these is a real SaaS LLM endpoint or a public IP. If any
	// starts passing, the "data never leaves the network" claim is broken.
	cases := []string{
		"https://api.openai.com/v1",
		"https://api.anthropic.com",
		"https://generativelanguage.googleapis.com",
		"https://api.deepseek.com/v1",
		"http://8.8.8.8:11434",
		"https://1.1.1.1/v1",
		"http://[2606:4700::1111]:8000",
		// A public name that merely CONTAINS an intranet-looking label must not
		// pass — suffix matching, not substring matching.
		"https://local.evil.example.com/v1",
		"https://svc.attacker.net/v1",
		// Unspecified address is not a reachable endpoint.
		"http://0.0.0.0:11434",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			verdict, err := ClassifyBaseURL(raw)
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if verdict.Intranet {
				t.Fatalf("%s must be REJECTED as public egress, got accepted with reason: %s", raw, verdict.Reason)
			}
			if err := ValidateBaseURL(raw); err == nil {
				t.Fatalf("ValidateBaseURL must reject %s", raw)
			}
		})
	}
}

func TestClassifyBaseURL_MalformedInputs(t *testing.T) {
	for _, raw := range []string{"", "   ", "not a url at all with spaces", "ftp://10.0.0.1/v1", "http://"} {
		if err := ValidateBaseURL(raw); err == nil {
			t.Fatalf("malformed base URL %q must be rejected", raw)
		}
	}
}

func TestEmptyBaseURLErrorIsActionable(t *testing.T) {
	// The empty case is the most likely operator mistake (channel created with
	// no base_url), so its message must say what to do, not just "invalid".
	err := ValidateBaseURL("")
	if err == nil {
		t.Fatal("empty base URL must error")
	}
	if !strings.Contains(err.Error(), "must name the intranet endpoint") {
		t.Fatalf("error must be actionable, got: %v", err)
	}
}

func TestExtraHostsEscapeHatch(t *testing.T) {
	const corporate = "https://inference.datacenter.example.com/v1"

	// Default posture: a publicly resolvable corporate name is refused.
	if err := ValidateBaseURL(corporate); err == nil {
		t.Fatal("a public-looking corporate name must be refused by default")
	}

	t.Setenv(ExtraHostsEnv, "inference.datacenter.example.com")
	verdict, err := ClassifyBaseURL(corporate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !verdict.Intranet {
		t.Fatalf("allow-listed host must pass, reason: %s", verdict.Reason)
	}
	if !strings.Contains(verdict.Reason, ExtraHostsEnv) {
		t.Fatalf("verdict must attribute the pass to the allow-list so an auditor can see it, got: %s", verdict.Reason)
	}

	// The allow-list is exact: a sibling host is still refused.
	if err := ValidateBaseURL("https://other.datacenter.example.com/v1"); err == nil {
		t.Fatal("allow-list must be exact-match, not domain-wide")
	}
}

func TestExtraHostsToleratesURLAndHostPortForms(t *testing.T) {
	// Operators paste what they have; accept the obvious shapes rather than
	// failing closed on a formatting nit that has no security meaning.
	for _, form := range []string{
		"https://inference.datacenter.example.com/v1",
		"inference.datacenter.example.com:8000",
		" inference.datacenter.example.com ",
	} {
		t.Run(form, func(t *testing.T) {
			t.Setenv(ExtraHostsEnv, form)
			if err := ValidateBaseURL("https://inference.datacenter.example.com/v1"); err != nil {
				t.Fatalf("allow-list entry %q should have matched: %v", form, err)
			}
		})
	}
}
