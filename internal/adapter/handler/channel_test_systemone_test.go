package handler

// channel_test_systemone_test.go — a channel of either System One type must be
// probed with POST /v1/systemone and the System One ping body, never a chat
// body: a chat probe against a System One server fails, and the auto-probe
// would then disable a healthy channel.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

type systemOneProbeSeen struct {
	mu     sync.Mutex
	paths  []string
	bodies [][]byte
}

func (s *systemOneProbeSeen) record(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, r.URL.Path)
	s.bodies = append(s.bodies, body)
}

// fakeSystemOneUpstream answers every request with status/body and records
// what it was asked.
func fakeSystemOneUpstream(t *testing.T, status int, body string) (*httptest.Server, *systemOneProbeSeen) {
	t.Helper()
	seen := &systemOneProbeSeen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

const systemOnePingOK = `{"model":"jev-1.13.0","answers":{"ok":{"type":"noul","noul":0.9}},"usage":{"input_tokens":12,"output_tokens":0}}`

func systemOneProbeChannel(t *testing.T, channelType int, models, baseURL string) *repo.Channel {
	t.Helper()
	u := baseURL
	autoBan := 1
	ch := &repo.Channel{
		Id: 9900 + channelType, Type: channelType, Status: common.ChannelStatusEnabled,
		Name: "so-probe", Key: "sk-so-probe", Models: models, Group: "default",
		BaseURL: &u, AutoBan: &autoBan,
	}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return ch
}

func TestProbeChannel_SystemOneUsesSystemOnePing(t *testing.T) {
	cases := []struct {
		name         string
		channelType  int
		models       string
		endpointType string // what an operator may pass from the console
		wantModel    string
	}{
		{"typesafe first model", constant.ChannelTypeTypeSafe, "jev-preview,jev-latest", "", "jev-preview"},
		{"compatible first model", constant.ChannelTypeSystemOneCompatible, "laya-english", "", "laya-english"},
		{"typesafe falls back to jev-latest", constant.ChannelTypeTypeSafe, "", "", "jev-latest"},
		{"operator-chosen chat endpoint is ignored", constant.ChannelTypeTypeSafe, "jev-latest", "openai", "jev-latest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupContextTierChannelTestDB(t)
			allowLoopbackEgress(t)
			app.InitHttpClient()
			seedModelRatio(t, tc.wantModel)
			upstream, seen := fakeSystemOneUpstream(t, http.StatusOK, systemOnePingOK)
			ch := systemOneProbeChannel(t, tc.channelType, tc.models, upstream.URL)

			result := probeChannel(ch, "", tc.endpointType, channelProbeOptions{})
			if result.localErr != nil || result.newAPIError != nil {
				t.Fatalf("healthy System One upstream failed the probe: localErr=%v newAPIError=%v", result.localErr, result.newAPIError)
			}

			if len(seen.paths) != 1 || seen.paths[0] != "/v1/systemone" {
				t.Fatalf("upstream paths = %v, want exactly [/v1/systemone]", seen.paths)
			}
			var got map[string]any
			if err := json.Unmarshal(seen.bodies[0], &got); err != nil {
				t.Fatalf("probe body is not JSON: %v: %s", err, seen.bodies[0])
			}
			wantQuestions := map[string]any{"ok": map[string]any{"type": "noul", "instructions": "Is this a test message?"}}
			questions, _ := json.Marshal(got["questions"])
			wantQ, _ := json.Marshal(wantQuestions)
			if got["state"] != "ping" || got["model"] != tc.wantModel || string(questions) != string(wantQ) {
				t.Errorf("probe body = %s, want state=ping model=%s questions=%s", seen.bodies[0], tc.wantModel, wantQ)
			}
			if _, chat := got["messages"]; chat {
				t.Errorf("probe body carries chat messages: %s", seen.bodies[0])
			}
		})
	}
}

func TestProbeChannel_SystemOneUpstream401FailsWithClearMessage(t *testing.T) {
	setupContextTierChannelTestDB(t)
	allowLoopbackEgress(t)
	app.InitHttpClient()
	seedModelRatio(t, "jev-latest")
	upstream, seen := fakeSystemOneUpstream(t, http.StatusUnauthorized, `{"detail":"invalid or missing bearer token"}`)
	ch := systemOneProbeChannel(t, constant.ChannelTypeTypeSafe, "jev-latest", upstream.URL)

	result := probeChannel(ch, "", "", channelProbeOptions{})
	if result.localErr == nil || result.newAPIError == nil {
		t.Fatalf("a 401 upstream must fail the probe: localErr=%v newAPIError=%v", result.localErr, result.newAPIError)
	}
	for _, want := range []string{"401", "invalid or missing bearer token"} {
		if !strings.Contains(result.localErr.Error(), want) {
			t.Errorf("probe error %q does not mention %q", result.localErr, want)
		}
	}
	if len(seen.paths) != 1 || seen.paths[0] != "/v1/systemone" {
		t.Errorf("upstream paths = %v, want exactly [/v1/systemone]", seen.paths)
	}
}

// A chat-completions request to a System One channel is the failure mode this
// file guards against, so assert on every path any request used, over every
// way the probe can be entered.
func TestProbeChannel_SystemOneNeverRequestsChatPath(t *testing.T) {
	for _, endpointType := range []string{"", "openai", "openai-response", "embeddings", "systemone"} {
		setupContextTierChannelTestDB(t)
		allowLoopbackEgress(t)
		app.InitHttpClient()
		seedModelRatio(t, "laya-auto")
		upstream, seen := fakeSystemOneUpstream(t, http.StatusOK, systemOnePingOK)
		ch := systemOneProbeChannel(t, constant.ChannelTypeSystemOneCompatible, "laya-auto", upstream.URL)

		probeChannel(ch, "", endpointType, channelProbeOptions{})
		for _, p := range seen.paths {
			if p != "/v1/systemone" {
				t.Errorf("endpointType %q: upstream saw path %q, want only /v1/systemone", endpointType, p)
			}
		}
		if len(seen.paths) == 0 {
			t.Errorf("endpointType %q: upstream saw no request", endpointType)
		}
	}
}

// The console's one-click test (TestChannelV2) is a second probe path with its
// own request builder. It must also speak System One, send the mapped model,
// and omit the Authorization header when the channel has no key.
func TestV2ChannelTest_SystemOne(t *testing.T) {
	cases := []struct {
		name       string
		chType     int
		key        string
		mapping    string
		status     int
		reply      string
		wantOK     bool
		wantAuth   string
		wantModel  string
		wantErrHas string
	}{
		{"typesafe ok", constant.ChannelTypeTypeSafe, "sk-ts", "", 200, systemOnePingOK, true, "Bearer sk-ts", "jev-latest", ""},
		{"compatible keyless sends no bearer, mapped model", constant.ChannelTypeSystemOneCompatible, "", `{"jev-latest":"english"}`, 200, systemOnePingOK, true, "", "english", ""},
		{"upstream 401 fails clearly", constant.ChannelTypeTypeSafe, "sk-bad", "", 401, `{"detail":"invalid or missing bearer token"}`, false, "Bearer sk-bad", "jev-latest", "401"},
		{"200 without answers fails", constant.ChannelTypeTypeSafe, "sk-ts", "", 200, `{"model":"x"}`, false, "Bearer sk-ts", "jev-latest", "answers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allowLoopbackEgress(t)
			var mu sync.Mutex
			var paths []string
			var auth string
			var sent map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				paths = append(paths, r.URL.Path)
				auth = r.Header.Get("Authorization")
				_ = json.Unmarshal(b, &sent)
				mu.Unlock()
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.reply)
			}))
			defer upstream.Close()
			prev := channelTestHTTPClient
			channelTestHTTPClient = upstream.Client()
			defer func() { channelTestHTTPClient = prev }()

			ctx := SetupV2TestRouter(t)
			defer ctx.Cleanup()
			ch := seedChannelWithBase(t, ctx, "so-v2", upstream.URL)
			updates := map[string]any{"type": tc.chType, "key": tc.key, "models": "jev-latest"}
			if tc.mapping != "" {
				updates["model_mapping"] = tc.mapping
			}
			if err := ctx.DB.Model(ch).Updates(updates).Error; err != nil {
				t.Fatalf("retype channel: %v", err)
			}

			w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, fmt.Sprintf("/api/v2/test-tenant/channels/%d/test", ch.Id), nil, []string{"admin"})
			AssertV2Status(t, w, http.StatusOK)
			resp := ParseV2Response(t, w)

			if got, _ := resp["success"].(bool); got != tc.wantOK {
				t.Fatalf("success = %v, want %v; body=%s", resp["success"], tc.wantOK, w.Body.String())
			}
			if tc.wantErrHas != "" && !strings.Contains(fmt.Sprint(resp["error"]), tc.wantErrHas) {
				t.Errorf("error %q does not mention %q", resp["error"], tc.wantErrHas)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(paths) != 1 || paths[0] != "/v1/systemone" {
				t.Fatalf("upstream paths = %v, want exactly [/v1/systemone]", paths)
			}
			if auth != tc.wantAuth {
				t.Errorf("Authorization = %q, want %q", auth, tc.wantAuth)
			}
			if sent["model"] != tc.wantModel || sent["state"] != "ping" {
				t.Errorf("sent body = %v, want state=ping model=%s", sent, tc.wantModel)
			}
		})
	}
}
