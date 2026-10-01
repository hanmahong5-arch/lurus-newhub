package systemone

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// Both the main interface and the optional System One one must be satisfied,
// or the relay helper's type assertion silently never matches.
var (
	_ provider.Adaptor          = (*Adaptor)(nil)
	_ provider.SystemOneAdaptor = (*Adaptor)(nil)
)

func infoFor(channelType int, base, key string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: channelType, ChannelBaseUrl: base, ApiKey: key,
	}}
}

func TestAdaptor_Identity(t *testing.T) {
	a := &Adaptor{}
	if got := a.GetChannelName(); got != "systemone" {
		t.Errorf("channel name = %q", got)
	}
	want := []string{"jev-latest", "jev-preview", "jev-1.13.0", "laya-auto", "laya-english", "laya-multilingual", "laya-typed-decisions"}
	if got := a.GetModelList(); !reflect.DeepEqual(got, want) {
		t.Errorf("model list = %v, want %v", got, want)
	}
}

func TestAdaptor_GetRequestURL(t *testing.T) {
	a := &Adaptor{}
	cases := []struct {
		name    string
		info    *relaycommon.RelayInfo
		want    string
		wantErr bool
	}{
		{"hosted default base", infoFor(constant.ChannelTypeTypeSafe, "", "k"), "https://api.typesafe.ai/v1/systemone", false},
		{"trailing slash trimmed", infoFor(constant.ChannelTypeSystemOneCompatible, "http://laya.internal:8000/", ""), "http://laya.internal:8000/v1/systemone", false},
		{"several trailing slashes", infoFor(constant.ChannelTypeSystemOneCompatible, "http://laya.internal:8000///", ""), "http://laya.internal:8000/v1/systemone", false},
		{"self-hosted without a base is an error", infoFor(constant.ChannelTypeSystemOneCompatible, "", ""), "", true},
		{"nil info", nil, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.GetRequestURL(tc.info)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("url = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAdaptor_SetupRequestHeader(t *testing.T) {
	a := &Adaptor{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", nil)
	// Caller-side SDK headers must not leak upstream.
	c.Request.Header.Set("X-TypeSafe-SDK", "typesafe-sdk/1")
	c.Request.Header.Set("Authorization", "Bearer hub-key")

	t.Run("key present", func(t *testing.T) {
		h := http.Header{}
		if err := a.SetupRequestHeader(c, &h, infoFor(constant.ChannelTypeTypeSafe, "", "up-key")); err != nil {
			t.Fatal(err)
		}
		if got := h.Get("Authorization"); got != "Bearer up-key" {
			t.Errorf("Authorization = %q", got)
		}
		if got := h.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if h.Get("X-TypeSafe-SDK") != "" {
			t.Errorf("caller SDK header leaked upstream: %v", h)
		}
	})
	t.Run("self-hosted without a key never sends an empty Bearer", func(t *testing.T) {
		h := http.Header{}
		if err := a.SetupRequestHeader(c, &h, infoFor(constant.ChannelTypeSystemOneCompatible, "http://x", "")); err != nil {
			t.Fatal(err)
		}
		if _, ok := h["Authorization"]; ok {
			t.Errorf("Authorization header present: %q", h.Get("Authorization"))
		}
		if got := h.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
	})
}
