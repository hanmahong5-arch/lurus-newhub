package baidu

// Business-acceptance gap-fill: Adaptor.DoRequest (end-to-end REST call
// construction) and the remaining model->URL-suffix branches of
// Adaptor.GetRequestURL that the existing cov_ suite's table test doesn't
// exercise. Wrong suffix routing here silently sends billed traffic to the
// wrong upstream model endpoint.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

// prov_2nd_pass_baidu_allowPrivateIP flips the SSRF fetch-setting guard so
// httptest.Server (127.0.0.1) is reachable, mirroring the pattern already
// used by the volcengine/ali cov_ suites.
func prov_2nd_pass_baidu_allowPrivateIP(t *testing.T) {
	t.Helper()
	app.InitHttpClient()
	fs := system_setting.GetFetchSetting()
	prevAllow, prevPorts := fs.AllowPrivateIp, fs.AllowedPorts
	fs.AllowPrivateIp = true
	fs.AllowedPorts = nil
	t.Cleanup(func() {
		s := system_setting.GetFetchSetting()
		s.AllowPrivateIp = prevAllow
		s.AllowedPorts = prevPorts
	})
}

func TestProv2ndPass_Baidu_DoRequest_HitsUpstreamWithAccessTokenQueryParam(t *testing.T) {
	prov_2nd_pass_baidu_allowPrivateIP(t)

	apiKey := "prov-2nd-pass-baidu-key"
	baiduTokenStore.Store(apiKey, BaiduAccessToken{
		AccessToken: "fresh-token-xyz",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	})

	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":"as-1","result":"hi","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	a := &Adaptor{}
	c, _ := prov_cn_batch_baiduGinContext()
	info := &relaycommon.RelayInfo{
		RelayMode: 0,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    srv.URL,
			ApiKey:            apiKey,
			UpstreamModelName: "ERNIE-Bot",
		},
	}

	resp, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	httpResp, ok := resp.(*http.Response)
	if !ok || httpResp == nil {
		t.Fatalf("resp type = %T, want *http.Response", resp)
	}
	defer httpResp.Body.Close()

	if gotPath != "/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions" {
		t.Errorf("upstream path = %q, want the ERNIE-Bot chat/completions suffix", gotPath)
	}
	if gotQuery != "access_token=fresh-token-xyz" {
		t.Errorf("upstream query = %q, want access_token=fresh-token-xyz (billing/auth token forwarded)", gotQuery)
	}
	if gotAuth != "Bearer "+apiKey {
		t.Errorf("upstream Authorization = %q, want Bearer %s", gotAuth, apiKey)
	}
}

func TestProv2ndPass_Baidu_GetRequestURL_RemainingModelSuffixes(t *testing.T) {
	apiKey := "prov-2nd-pass-baidu-key-2"
	baiduTokenStore.Store(apiKey, BaiduAccessToken{
		AccessToken: "cached-tok",
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	})

	tests := []struct {
		model  string
		suffix string
	}{
		{"ERNIE-Bot-4", "chat/completions_pro"},
		{"ERNIE-Speed", "chat/ernie_speed"},
		{"ERNIE-4.0-8K", "chat/completions_pro"},
		{"ERNIE-3.5-8K", "chat/completions"},
		{"ERNIE-3.5-8K-0205", "chat/ernie-3.5-8k-0205"},
		{"ERNIE-3.5-8K-1222", "chat/ernie-3.5-8k-1222"},
		{"ERNIE-Bot-8K", "chat/ernie_bot_8k"},
		{"ERNIE-3.5-4K-0205", "chat/ernie-3.5-4k-0205"},
		{"ERNIE-Speed-8K", "chat/ernie_speed"},
		{"ERNIE-Speed-128K", "chat/ernie-speed-128k"},
		{"ERNIE-Lite-8K-0922", "chat/eb-instant"},
		{"ERNIE-Lite-8K-0308", "chat/ernie-lite-8k"},
		{"ERNIE-Tiny-8K", "chat/ernie-tiny-8k"},
		{"BLOOMZ-7B", "chat/bloomz_7b1"},
		{"bge-large-en", "embeddings/bge_large_en"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			a := &Adaptor{}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				ChannelBaseUrl:    "https://aip.baidubce.com",
				UpstreamModelName: tt.model,
				ApiKey:            apiKey,
			}}
			url, err := a.GetRequestURL(info)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/" + tt.suffix + "?access_token=cached-tok"
			if url != want {
				t.Errorf("url = %q, want %q", url, want)
			}
		})
	}
}

func TestProv2ndPass_Baidu_GetRequestURL_AccessTokenErrorPropagates(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelBaseUrl:    "https://aip.baidubce.com",
		UpstreamModelName: "ERNIE-Bot",
		ApiKey:            "not-cached-and-malformed", // no cache entry, no '|' separator
	}}
	url, err := a.GetRequestURL(info)
	if err == nil {
		t.Fatal("expected the access-token error to propagate out of GetRequestURL, got nil")
	}
	if url != "" {
		t.Errorf("url = %q, want empty string on token-acquisition failure", url)
	}
}
