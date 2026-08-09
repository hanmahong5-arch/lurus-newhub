package mokaai

// Business-acceptance tests for the remaining MokaAI adaptor surfaces not
// covered by the existing cov_prov-longtail suite: DoRequest's transport
// delegation, and the read-failure branch of mokaEmbeddingHandler (an
// upstream connection reset mid-transfer must degrade to a typed error, not
// panic or silently fabricate empty usage).

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// ---------------------------------------------------------------------------
// DoRequest: delegates to provider.DoApiRequest against the channel base
// URL, running GetRequestURL + SetupRequestHeader in the process.
// ---------------------------------------------------------------------------

func TestMoka_DoRequest_DelegatesToApiRequest(t *testing.T) {
	app.InitHttpClient()
	a := &Adaptor{}

	fs := system_setting.GetFetchSetting()
	prevAllow := fs.AllowPrivateIp
	fs.AllowPrivateIp = true
	defer func() { system_setting.GetFetchSetting().AllowPrivateIp = prevAllow }()

	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeBaidu,
			ChannelBaseUrl:    srv.URL,
			ApiKey:            "moka-key",
			UpstreamModelName: "m3e-base",
		},
		RelayMode: relayconstant.RelayModeEmbeddings,
	}
	result, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, ok := result.(*http.Response)
	if !ok {
		t.Fatalf("result type = %T, want *http.Response", result)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if gotAuth != "Bearer moka-key" {
		t.Errorf("upstream saw Authorization = %q, want Bearer moka-key (SetupRequestHeader must run)", gotAuth)
	}
	if gotPath != "/embeddings" {
		t.Errorf("upstream saw path = %q, want /embeddings (m3e model routes to embeddings endpoint)", gotPath)
	}
}

// ---------------------------------------------------------------------------
// mokaEmbeddingHandler: a body read failure (e.g. connection reset
// mid-transfer) must surface as a typed error, not panic and not fabricate
// usage.
// ---------------------------------------------------------------------------

type provLT2MokaErrReader struct{}

func (provLT2MokaErrReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (provLT2MokaErrReader) Close() error              { return nil }

func TestMoka_EmbeddingHandler_BodyReadFailure_Errors(t *testing.T) {
	c, _ := newProvLongtailMokaCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: provLT2MokaErrReader{}, Header: make(http.Header)}
	usage, apiErr := mokaEmbeddingHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when the response body read fails")
	}
	if usage != nil {
		t.Errorf("usage should be nil on read failure, got %+v", usage)
	}
}
