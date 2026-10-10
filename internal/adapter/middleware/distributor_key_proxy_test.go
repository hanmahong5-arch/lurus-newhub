package middleware

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func keyProxyChannel() *repo.Channel {
	ch := &repo.Channel{Id: 7701, Name: "mk", Type: constant.ChannelTypeOpenAI, Key: "k0\nk1", Status: common.ChannelStatusEnabled}
	ch.SetSetting(dto.ChannelSettings{Proxy: "http://channel-proxy.example.com:8080"})
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = 2
	ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeWeighted
	ch.ChannelInfo.MultiKeyWeight = map[int]int{0: 0, 1: 50} // only key 1 is selectable
	ch.ChannelInfo.MultiKeyProxy = map[int]string{1: "http://key-proxy.example.com:3128"}
	return ch
}

func TestSetupContext_KeyProxyOverridesChannelProxy(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
	if err := SetupContextForSelectedChannel(c, keyProxyChannel(), "model-a"); err != nil {
		t.Fatal(err)
	}
	setting, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	if !ok || setting.Proxy != "http://key-proxy.example.com:3128" {
		t.Fatalf("relay must egress through the selected key's proxy, got %q", setting.Proxy)
	}
	if got := common.GetContextKeyString(c, constant.ContextKeyChannelKey); got != "k1" {
		t.Fatalf("selected key = %q, want k1", got)
	}
}

func TestSetupContext_NoKeyProxyKeepsChannelProxy(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
	ch := keyProxyChannel()
	ch.ChannelInfo.MultiKeyProxy = nil
	if err := SetupContextForSelectedChannel(c, ch, "model-a"); err != nil {
		t.Fatal(err)
	}
	setting, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	if setting.Proxy != "http://channel-proxy.example.com:8080" {
		t.Fatalf("channel proxy must survive, got %q", setting.Proxy)
	}
}

func TestSetupContext_StickySessionKeepsKey(t *testing.T) {
	ch := keyProxyChannel()
	ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeRandom
	ch.ChannelInfo.MultiKeyWeight = nil
	ch.ChannelInfo.MultiKeyProxy = nil
	var first string
	for i := 0; i < 20; i++ {
		c, _ := newTestContext(http.MethodPost, "/v1/chat/completions", `{}`, "application/json")
		common.SetContextKey(c, constant.ContextKeySessionAffinity, "sticky-session-x")
		if err := SetupContextForSelectedChannel(c, ch, "model-a"); err != nil {
			t.Fatal(err)
		}
		k := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
		if i == 0 {
			first = k
		} else if k != first {
			t.Fatalf("request %d used %q, session is bound to %q", i, k, first)
		}
	}
}
