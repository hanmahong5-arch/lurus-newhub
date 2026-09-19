package middleware

// direct_request_test.go — pins IsDirectInClusterRequest's classification:
// loopback/private RemoteAddr with no forwarding header looks like a direct
// in-cluster caller (kubelet); a public RemoteAddr, or any of the three
// forwarding headers a reverse proxy adds, does not.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIsDirectInClusterRequest_Table(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func(remoteAddr string, headers map[string]string) *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		req.RemoteAddr = remoteAddr
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		c.Request = req
		return c
	}

	cases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       bool
	}{
		{"loopback, no headers", "127.0.0.1:12345", nil, true},
		{"private, no headers", "10.0.0.5:12345", nil, true},
		{"public, no headers", "203.0.113.9:12345", nil, false},
		{"private + X-Forwarded-For", "10.0.0.5:12345", map[string]string{"X-Forwarded-For": "203.0.113.9"}, false},
		{"private + X-Real-IP", "10.0.0.5:12345", map[string]string{"X-Real-IP": "203.0.113.9"}, false},
		{"private + Forwarded", "10.0.0.5:12345", map[string]string{"Forwarded": "for=203.0.113.9"}, false},
		{"private, no port (SplitHostPort fallback)", "10.0.0.5", nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCtx(tc.remoteAddr, tc.headers)
			if got := IsDirectInClusterRequest(c); got != tc.want {
				t.Errorf("IsDirectInClusterRequest(%s, headers=%v) = %v, want %v", tc.remoteAddr, tc.headers, got, tc.want)
			}
		})
	}
}
