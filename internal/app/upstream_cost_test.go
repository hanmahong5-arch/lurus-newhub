package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

func TestRecordUpstreamCost_OnlyWhenUpstreamReportsIt(t *testing.T) {
	cases := []struct {
		name      string
		chType    int
		cost      any
		wantKnown bool
	}{
		{"openrouter reports cost", constant.ChannelTypeOpenRouter, 0.0123, true},
		{"openrouter reports zero (free model)", constant.ChannelTypeOpenRouter, 0.0, true},
		{"openrouter omits cost", constant.ChannelTypeOpenRouter, nil, false},
		{"other channel with a cost field", constant.ChannelTypeOpenAI, 0.5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: tc.chType}}
			RecordUpstreamCost(info, &dto.Usage{Cost: tc.cost})
			if known := info.UpstreamCostUSD != nil; known != tc.wantKnown {
				t.Fatalf("upstream cost known = %v, want %v", known, tc.wantKnown)
			}
		})
	}
}

// The usage mirror carries channel_id always and upstream_cost_cny only when
// known. Mutation: report upstream_cost_cny unconditionally → the unknown
// case sees a 0 and goes red.
func TestMirrorUsageEvent_CostFields(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()
	prevURL, prevKey := common.IdentityServiceURL, common.IdentityServiceInternalKey
	common.IdentityServiceURL, common.IdentityServiceInternalKey = srv.URL, "k"
	t.Cleanup(func() { common.IdentityServiceURL, common.IdentityServiceInternalKey = prevURL, prevKey })

	meta := func(info *relaycommon.RelayInfo) map[string]any {
		got = nil
		mirrorUsageEvent(context.Background(), info, 7, 1000, 0.5, "k1", 0)
		m, _ := got["metadata"].(map[string]any)
		if m == nil {
			t.Fatalf("no metadata posted: %v", got)
		}
		return m
	}

	known := &relaycommon.RelayInfo{OriginModelName: "m",
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 12, ChannelType: constant.ChannelTypeOpenRouter}}
	RecordUpstreamCost(known, &dto.Usage{Cost: 0.01})
	m := meta(known)
	if m["channel_id"].(float64) != 12 {
		t.Errorf("channel_id = %v, want 12", m["channel_id"])
	}
	want := 0.01 * operation_setting.USDExchangeRate
	if c, _ := m["upstream_cost_cny"].(float64); c < want-1e-9 || c > want+1e-9 {
		t.Errorf("upstream_cost_cny = %v, want %v", m["upstream_cost_cny"], want)
	}
	if m["amount_cny"].(float64) != 0.5 || m["model"] != "m" {
		t.Errorf("existing fields changed: %v", m)
	}

	unknown := &relaycommon.RelayInfo{OriginModelName: "m",
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 3, ChannelType: constant.ChannelTypeOpenAI}}
	m = meta(unknown)
	if _, present := m["upstream_cost_cny"]; present {
		t.Errorf("upstream_cost_cny present for an unknown cost: %v", m["upstream_cost_cny"])
	}
	if m["channel_id"].(float64) != 3 {
		t.Errorf("channel_id = %v, want 3", m["channel_id"])
	}
}
