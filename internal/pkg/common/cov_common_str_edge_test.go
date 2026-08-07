package common

import "testing"

// TestMapToJsonStr_MarshalErrorReturnsEmptyString covers the error branch:
// a map value that json.Marshal cannot encode (a bare channel) must yield ""
// rather than propagating the error or panicking — callers treat "" as the
// canonical "nothing to store" sentinel.
func TestMapToJsonStr_MarshalErrorReturnsEmptyString(t *testing.T) {
	m := map[string]interface{}{"bad": make(chan int)}
	if got := MapToJsonStr(m); got != "" {
		t.Errorf("MapToJsonStr with unmarshalable value = %q, want empty string", got)
	}
}

// TestMapToJsonStr_HappyPath is the control case the error-branch test above
// is diffed against: a normal map must round-trip to a real JSON object, not
// also come back "" (which would make the error-branch assertion meaningless).
func TestMapToJsonStr_HappyPath(t *testing.T) {
	m := map[string]interface{}{"tenant": "acme", "quota": 100}
	got := MapToJsonStr(m)
	if got == "" {
		t.Fatal("MapToJsonStr on a valid map must not return empty string")
	}
	back, err := StrToMap(got)
	if err != nil {
		t.Fatalf("round-trip StrToMap failed: %v", err)
	}
	if back["tenant"] != "acme" {
		t.Errorf("round-trip tenant = %v, want acme", back["tenant"])
	}
}

// TestMaskHostForPlainDomain_EdgeMatrix pins down the subdomain-depth-aware
// masking documented in the function's own comment, including the co.uk /
// com.cn two-part-TLD carve-out and the single-label (no dot) passthrough.
func TestMaskHostForPlainDomain_EdgeMatrix(t *testing.T) {
	cases := []struct {
		name   string
		domain string
		want   string
	}{
		{"single label passthrough", "localhost", "localhost"},
		{"empty string passthrough", "", ""},
		{"bare TLD-like second-level", "openai.com", "***.com"},
		{"one subdomain", "api.openai.com", "***.***.com"},
		{"two subdomains", "eu.api.openai.com", "***.***.***.com"},
		{"country-code second-level tld", "example.co.uk", "***.co.uk"},
		{"country-code tld with one subdomain", "sub.example.co.uk", "***.***.co.uk"},
		{"cn country tld", "example.com.cn", "***.com.cn"},
		{"trailing dot produces empty last label", "openai.com.", "***.***."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := maskHostForPlainDomain(tc.domain); got != tc.want {
				t.Errorf("maskHostForPlainDomain(%q) = %q, want %q", tc.domain, got, tc.want)
			}
		})
	}
}

// TestMaskSensitiveInfo_DomainMatrixIntegration exercises MaskSensitiveInfo
// end-to-end (regex match -> maskHostForPlainDomain) for domain-only (no
// scheme) inputs, which is the code path maskHostForPlainDomain exists for.
func TestMaskSensitiveInfo_DomainMatrixIntegration(t *testing.T) {
	cases := map[string]string{
		"openai.com":      "***.com",
		"www.openai.com":  "***.***.com",
		"contact us at support.example.co.uk please": "contact us at ***.***.co.uk please",
	}
	for input, want := range cases {
		if got := MaskSensitiveInfo(input); got != want {
			t.Errorf("MaskSensitiveInfo(%q) = %q, want %q", input, got, want)
		}
	}
}
