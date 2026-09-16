package handler

import (
	"strings"
	"testing"
)

// TestRedactVideoURLForLog pins what reaches an error log when VideoProxy
// refuses or fails on a vendor-supplied URL.
//
// Two credential shapes are in scope. A Gemini video URL carries the channel's
// API key as a query parameter (video_proxy_gemini.go's ensureAPIKey appends
// "?key=<apiKey>"), and an operator-configured artefact URL can carry HTTP
// credentials in the authority. Both must be gone; the host and path stay so
// the log line is still worth reading.
//
// This is the oracle for redactVideoURLForLog: with the body replaced by
// `return raw`, the query/fragment/userinfo cases fail.
func TestRedactVideoURLForLog(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "gemini api key in the query is dropped",
			raw:  "https://generativelanguage.googleapis.com/v1beta/files/abc:download?alt=media&key=AIzaLIVESECRET",
			want: "https://generativelanguage.googleapis.com/v1beta/files/abc:download",
		},
		{
			name: "fragment is dropped",
			raw:  "https://cdn.example.com/v/1.mp4#t=30",
			want: "https://cdn.example.com/v/1.mp4",
		},
		{
			name: "userinfo is dropped",
			raw:  "https://user:password@cdn.example.com/v/1.mp4",
			want: "https://cdn.example.com/v/1.mp4",
		},
		{
			name: "userinfo and query together are both dropped",
			raw:  "https://user:password@cdn.example.com/v/1.mp4?key=LIVESECRET",
			want: "https://cdn.example.com/v/1.mp4",
		},
		{
			name: "clean url is unchanged",
			raw:  "https://cdn.example.com/v/1.mp4",
			want: "https://cdn.example.com/v/1.mp4",
		},
		{
			name: "unparseable raw still loses its query shape",
			raw:  "://not a url?key=LIVESECRET",
			want: "://not a url",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactVideoURLForLog(tc.raw); got != tc.want {
				t.Errorf("redactVideoURLForLog(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestRedactVideoURLForLog_NoSecretSubstringSurvives is the property the table
// above encodes, asserted directly: whatever the input shape, the literal
// secret must not appear in the output.
func TestRedactVideoURLForLog_NoSecretSubstringSurvives(t *testing.T) {
	const secret = "LIVESECRET"
	for _, raw := range []string{
		"https://host/p?key=" + secret,
		"https://host/p#" + secret,
		"https://u:" + secret + "@host/p",
		"https://u:" + secret + "@host/p?key=" + secret,
	} {
		if got := redactVideoURLForLog(raw); strings.Contains(got, secret) {
			t.Errorf("redactVideoURLForLog(%q) = %q, still contains the secret", raw, got)
		}
	}
}
