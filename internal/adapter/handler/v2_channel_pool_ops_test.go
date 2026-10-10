package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"
)

const (
	poolBase       = "/api/v2/test-tenant/channels"
	publicProxyURL = "http://8.8.4.4:3128"
	privateProxy   = "http://10.0.0.5:3128"
)

// poolOpsEnv turns the SSRF guard on, stubs the upstream probe and the jitter
// pause, and captures audit rows. Everything is restored on cleanup.
type poolOpsEnv struct {
	mu      sync.Mutex
	probes  []string // keys probed, in order
	jitters int
	audits  []string // "action|details"
	failKey string   // keys containing this substring fail the probe
}

type auditCapture struct{ env *poolOpsEnv }

func (a auditCapture) CreateAuditEvent(e *entity.AuditEvent) error {
	a.env.mu.Lock()
	defer a.env.mu.Unlock()
	a.env.audits = append(a.env.audits, e.Action+"|"+e.Details)
	return nil
}

func newPoolOpsEnv(t *testing.T) *poolOpsEnv {
	t.Helper()
	env := &poolOpsEnv{}
	fs := system_setting.GetFetchSetting()
	prevFS := *fs
	fs.EnableSSRFProtection = true
	fs.AllowPrivateIp = false
	prevProbe, prevJitter, prevNow, prevAsync := keyProbeFn, importJitter, restoreProofNow, governance.AsyncGo
	keyProbeFn = func(_ context.Context, _, _, key, _ string) (int64, string) {
		env.mu.Lock()
		env.probes = append(env.probes, key)
		env.mu.Unlock()
		if env.failKey != "" && strings.Contains(key, env.failKey) {
			return 5, "invalid api key"
		}
		return 5, ""
	}
	importJitter = func() time.Duration { env.jitters++; return 0 }
	governance.AsyncGo = func(f func()) { f() }
	governance.SetAuditWriter(auditCapture{env})
	t.Cleanup(func() {
		*fs = prevFS
		keyProbeFn, importJitter, restoreProofNow, governance.AsyncGo = prevProbe, prevJitter, prevNow, prevAsync
		governance.SetAuditWriter(nil)
	})
	return env
}

func poolSeedChannel(t *testing.T, ctx *V2TestContext, keys []string, mutate func(*repo.Channel)) *repo.Channel {
	t.Helper()
	base := "https://8.8.8.8"
	ch := &repo.Channel{
		Name: "pool", TenantId: ctx.TenantID, Key: strings.Join(keys, "\n"), Status: common.ChannelStatusEnabled,
		Type: 1, Models: "model-a", Group: "default", CreatedTime: common.GetTimestamp(), BaseURL: &base,
	}
	ch.ChannelInfo.IsMultiKey = true
	ch.ChannelInfo.MultiKeySize = len(keys)
	ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeWeighted
	if mutate != nil {
		mutate(ch)
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	return ch
}

func poolAdmin(ctx *V2TestContext, method, path string, body interface{}) (int, map[string]interface{}) {
	w := V2RequestAsUser(ctx, ctx.RootUser, method, path, body, []string{"root"})
	var m map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func poolData(m map[string]interface{}) map[string]interface{} {
	d, _ := m["data"].(map[string]interface{})
	return d
}

func poolResults(t *testing.T, m map[string]interface{}) []map[string]interface{} {
	t.Helper()
	raw, _ := poolData(m)["results"].([]interface{})
	out := make([]map[string]interface{}, len(raw))
	for i, r := range raw {
		out[i], _ = r.(map[string]interface{})
	}
	return out
}

func poolChannelCount(t *testing.T, ctx *V2TestContext) int64 {
	t.Helper()
	var n int64
	ctx.DB.Model(&repo.Channel{}).Count(&n)
	return n
}

func TestPoolOps_NonStaffForbidden(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	env := newPoolOpsEnv(t)
	ch := poolSeedChannel(t, ctx, []string{"sk-a", "sk-b"}, nil)
	cases := []struct {
		method, path string
		body         interface{}
	}{
		{http.MethodPost, poolBase + "/import", map[string]interface{}{"keys": "sk-x", "type": 1}},
		{http.MethodGet, fmt.Sprintf("%s/%d/health", poolBase, ch.Id), nil},
		{http.MethodPost, fmt.Sprintf("%s/%d/keys/0/test", poolBase, ch.Id), nil},
		{http.MethodPost, fmt.Sprintf("%s/%d/keys/0/restore", poolBase, ch.Id), map[string]string{"proof": "x"}},
		{http.MethodPut, fmt.Sprintf("%s/%d/keys/0/settings", poolBase, ch.Id), map[string]int{"weight": 10}},
	}
	for _, tc := range cases {
		w := V2RequestAsUser(ctx, ctx.NormalUser, tc.method, tc.path, tc.body, nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s as non-staff = %d, want 403", tc.method, tc.path, w.Code)
		}
	}
	if len(env.probes) != 0 {
		t.Error("forbidden callers must not trigger probes")
	}
}

func TestImport_DryRunDedupeInvalidAndSSRF(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	env := newPoolOpsEnv(t)
	poolSeedChannel(t, ctx, []string{"sk-existing-1", "sk-existing-2"}, nil)
	before := poolChannelCount(t, ctx)

	code, resp := poolAdmin(ctx, http.MethodPost, poolBase+"/import", map[string]interface{}{
		"type": 1, "models": "model-a", "base_url": "https://8.8.8.8",
		"keys": "sk-existing-2\nsk-new-1\nsk-new-1\n\n",
		"items": []map[string]interface{}{
			{"key": "sk-priv-proxy", "proxy": privateProxy},
			{"key": "sk-bad-weight", "weight": 101},
			{"key": "sk-ok-proxy", "proxy": publicProxyURL, "weight": 0, "plan_kind": "monthly", "expires_at": "2030-01-02T03:04:05Z"},
			{"key": "sk-bad-expiry", "expires_at": "not-a-date"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, resp)
	}
	if poolData(resp)["dry_run"] != true {
		t.Fatal("dry_run must default to true")
	}
	rs := poolResults(t, resp)
	want := []struct{ status, code string }{
		{"duplicate", "duplicate_existing_key"}, // existing channel key
		{"ready", ""},                           // first sk-new-1
		{"duplicate", "duplicate_in_batch"},     // repeated sk-new-1
		{"invalid", "proxy_rejected"},           // private proxy: SSRF
		{"invalid", "invalid_weight"},
		{"ready", ""}, // explicit weight 0 is valid
		{"invalid", "invalid_expires_at"},
	}
	if len(rs) != len(want) {
		t.Fatalf("got %d results, want %d: %v", len(rs), len(want), rs)
	}
	for i, w := range want {
		got, _ := rs[i]["status"].(string)
		ec, _ := rs[i]["error_code"].(string)
		if got != w.status || ec != w.code {
			t.Errorf("row %d = %s/%s, want %s/%s", i, got, ec, w.status, w.code)
		}
	}
	fp, _ := rs[1]["fingerprint"].(string)
	if len(fp) != 16 || fp != keyFingerprint("sk-new-1") {
		t.Errorf("fingerprint %q is not the 16-hex SHA-256 prefix", fp)
	}
	if rs[2]["fingerprint"] != fp {
		t.Error("batch duplicate must carry the same fingerprint")
	}
	if cid, _ := rs[0]["existing_channel_id"].(float64); cid == 0 {
		t.Error("existing duplicate must point at the owning channel")
	}
	if poolChannelCount(t, ctx) != before {
		t.Error("dry run must not write channels")
	}
	if len(env.probes) != 0 {
		t.Error("dry run must not probe")
	}
	for _, row := range rs {
		if _, leaked := row["key"]; leaked {
			t.Error("results must not carry key plaintext")
		}
	}
}

func TestImport_ConfirmProbesSerialWithJitterAndSkipsFailures(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	env := newPoolOpsEnv(t)
	env.failKey = "bad"

	code, resp := poolAdmin(ctx, http.MethodPost, poolBase+"/import", map[string]interface{}{
		"dry_run": false, "probe": true, "type": 1, "models": "model-a", "base_url": "https://8.8.8.8",
		"keys": "sk-good-1\nsk-bad-2\nsk-good-3",
	})
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, resp)
	}
	rs := poolResults(t, resp)
	if rs[0]["status"] != "ready" || rs[0]["imported"] != true ||
		rs[1]["status"] != "probe_failed" || rs[1]["imported"] != false ||
		rs[2]["status"] != "ready" || rs[2]["imported"] != true {
		t.Fatalf("unexpected rows: %v", rs)
	}
	if got := strings.Join(env.probes, ","); got != "sk-good-1,sk-bad-2,sk-good-3" {
		t.Errorf("probes must run serially in input order, got %s", got)
	}
	if env.jitters != 2 {
		t.Errorf("jitter pauses = %d, want 2 (between 3 probes)", env.jitters)
	}
	if n := poolChannelCount(t, ctx); n != 2 {
		t.Errorf("channels created = %d, want 2 (failed probe not imported)", n)
	}
	// Audit rows exist and never contain key plaintext.
	env.mu.Lock()
	defer env.mu.Unlock()
	found := false
	for _, a := range env.audits {
		if strings.HasPrefix(a, governance.ActionChannelKeysImported+"|") {
			found = true
		}
		if strings.Contains(a, "sk-good") || strings.Contains(a, "sk-bad") {
			t.Errorf("audit leaks key plaintext: %s", a)
		}
	}
	if !found {
		t.Error("import must be audited")
	}
}

func TestImport_AppendToMultiKeyChannelAppliesPerKeySettings(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	newPoolOpsEnv(t)
	ch := poolSeedChannel(t, ctx, []string{"sk-a", "sk-b"}, nil)

	code, resp := poolAdmin(ctx, http.MethodPost, poolBase+"/import", map[string]interface{}{
		"dry_run": false, "channel_id": ch.Id,
		"items": []map[string]interface{}{
			{"key": "sk-c", "proxy": publicProxyURL, "weight": 80, "name": "acct-c", "plan_kind": "monthly"},
			{"key": "sk-a"}, // duplicate of an existing key in the target
		},
	})
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, resp)
	}
	rs := poolResults(t, resp)
	if rs[0]["imported"] != true || rs[1]["status"] != "duplicate" {
		t.Fatalf("rows: %v", rs)
	}
	got, _ := repo.GetChannelById(ch.Id, true)
	if got.Key != "sk-a\nsk-b\nsk-c" || got.ChannelInfo.MultiKeySize != 3 {
		t.Fatalf("key list = %q size %d", got.Key, got.ChannelInfo.MultiKeySize)
	}
	if got.ChannelInfo.MultiKeyProxy[2] != publicProxyURL || got.ChannelInfo.MultiKeyWeight[2] != 80 ||
		got.ChannelInfo.MultiKeyMeta[2].Name != "acct-c" || got.ChannelInfo.MultiKeyMeta[2].PlanKind != "monthly" {
		t.Fatalf("per-key settings not stored at the new index: %+v", got.ChannelInfo)
	}
}

func TestKeySettings_ProxySSRFWeightBounds(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	newPoolOpsEnv(t)
	ch := poolSeedChannel(t, ctx, []string{"sk-a", "sk-b"}, nil)
	url := fmt.Sprintf("%s/%d/keys/1/settings", poolBase, ch.Id)

	if code, _ := poolAdmin(ctx, http.MethodPut, url, map[string]interface{}{"proxy": privateProxy}); code != http.StatusBadRequest {
		t.Errorf("private proxy = %d, want 400", code)
	}
	if code, _ := poolAdmin(ctx, http.MethodPut, url, map[string]interface{}{"proxy": "socks5://192.168.1.1:1080"}); code != http.StatusBadRequest {
		t.Errorf("private socks proxy = %d, want 400", code)
	}
	if code, _ := poolAdmin(ctx, http.MethodPut, url, map[string]interface{}{"weight": 101}); code != http.StatusBadRequest {
		t.Errorf("weight 101 = %d, want 400", code)
	}
	if code, _ := poolAdmin(ctx, http.MethodPut, url, map[string]interface{}{"weight": -1}); code != http.StatusBadRequest {
		t.Errorf("weight -1 = %d, want 400", code)
	}
	if got, _ := repo.GetChannelById(ch.Id, true); len(got.ChannelInfo.MultiKeyProxy) != 0 || len(got.ChannelInfo.MultiKeyWeight) != 0 {
		t.Fatal("rejected requests must not change anything")
	}
	if code, _ := poolAdmin(ctx, http.MethodPut, url, map[string]interface{}{"proxy": publicProxyURL, "weight": 0}); code != http.StatusOK {
		t.Fatalf("valid settings = %d", code)
	}
	got, _ := repo.GetChannelById(ch.Id, true)
	if got.ChannelInfo.MultiKeyProxy[1] != publicProxyURL {
		t.Error("proxy not stored")
	}
	if w, ok := got.ChannelInfo.MultiKeyWeight[1]; !ok || w != 0 {
		t.Error("explicit weight 0 must be stored (means never pick)")
	}
	if code, _ := poolAdmin(ctx, http.MethodPut, fmt.Sprintf("%s/%d/keys/9/settings", poolBase, ch.Id), map[string]int{"weight": 1}); code != http.StatusBadRequest {
		t.Error("out-of-range key index must be 400")
	}
}

func TestHealth_ReasonsPerKey(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	newPoolOpsEnv(t)
	future := time.Now().Add(30 * time.Minute).Unix()
	past := time.Now().Add(-time.Hour).Unix()
	ch := poolSeedChannel(t, ctx, []string{"sk-0", "sk-1", "sk-2", "sk-3", "sk-4", "sk-5"}, func(c *repo.Channel) {
		i := &c.ChannelInfo
		i.MultiKeyStatusList = map[int]int{
			1: common.ChannelStatusAutoDisabled, // cooling
			2: common.ChannelStatusManuallyDisabled,
			3: common.ChannelStatusAutoDisabled, // auth
			4: common.ChannelStatusAutoDisabled, // window
		}
		i.MultiKeyCooldownUntil = map[int]int64{1: future}
		i.MultiKeyDisabledReason = map[int]string{3: "HTTP 401 unauthorized", 4: "quota_window: 5h exhausted"}
		i.MultiKeyMeta = map[int]entity.KeyMeta{5: {ExpiresAt: past}}
	})
	code, resp := poolAdmin(ctx, http.MethodGet, fmt.Sprintf("%s/%d/health", poolBase, ch.Id), nil)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	d := poolData(resp)
	keys, _ := d["keys"].([]interface{})
	wantReason := map[int]string{1: "cooling_429", 2: "disabled_manual", 3: "auth_failed", 4: "plan_window_exhausted", 5: "expired"}
	for i, raw := range keys {
		k := raw.(map[string]interface{})
		rs, _ := k["reasons"].([]interface{})
		if i == 0 {
			if k["routable"] != true || len(rs) != 0 {
				t.Errorf("key 0 must be routable, got %v", k)
			}
			continue
		}
		if k["routable"] != false || len(rs) == 0 || rs[0] != wantReason[i] {
			t.Errorf("key %d = routable %v reasons %v, want %s", i, k["routable"], rs, wantReason[i])
		}
	}
	if k1 := keys[1].(map[string]interface{}); k1["cooldown_until"].(float64) != float64(future) {
		t.Errorf("cooldown_until = %v", k1["cooldown_until"])
	}
	if d["routable"] != true {
		t.Error("channel with one routable key is routable")
	}
	if _, has := keys[0].(map[string]interface{})["window"]; !has {
		t.Error("window field must be present (null when no snapshot source)")
	}
}

func TestHealth_ChannelAggregateWhenNothingRoutable(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	newPoolOpsEnv(t)
	ch := poolSeedChannel(t, ctx, []string{"sk-0", "sk-1"}, func(c *repo.Channel) {
		c.ChannelInfo.MultiKeyStatusList = map[int]int{0: common.ChannelStatusManuallyDisabled, 1: common.ChannelStatusAutoDisabled}
		c.ChannelInfo.MultiKeyDisabledReason = map[int]string{1: "insufficient balance"}
	})
	_, resp := poolAdmin(ctx, http.MethodGet, fmt.Sprintf("%s/%d/health", poolBase, ch.Id), nil)
	d := poolData(resp)
	rs, _ := d["reasons"].([]interface{})
	if d["routable"] != false || len(rs) != 2 {
		t.Fatalf("aggregate = %v", d)
	}
	joined := fmt.Sprint(rs)
	if !strings.Contains(joined, "disabled_manual") || !strings.Contains(joined, "balance_low") {
		t.Errorf("reasons = %s", joined)
	}
}

func TestRestoreProof_FlowAndStaleness(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	env := newPoolOpsEnv(t)
	now := time.Now()
	restoreProofNow = func() time.Time { return now }
	cool := now.Add(time.Hour).Unix()
	ch := poolSeedChannel(t, ctx, []string{"sk-0", "sk-1"}, func(c *repo.Channel) {
		c.ChannelInfo.MultiKeyStatusList = map[int]int{1: common.ChannelStatusAutoDisabled}
		c.ChannelInfo.MultiKeyDisabledReason = map[int]string{1: "429"}
		c.ChannelInfo.MultiKeyDisabledTime = map[int]int64{1: now.Unix() - 60}
		c.ChannelInfo.MultiKeyCooldownUntil = map[int]int64{1: cool}
	})
	testURL := fmt.Sprintf("%s/%d/keys/1/test", poolBase, ch.Id)
	restoreURL := fmt.Sprintf("%s/%d/keys/1/restore", poolBase, ch.Id)

	// A failing probe never yields a proof.
	env.failKey = "sk-1"
	_, resp := poolAdmin(ctx, http.MethodPost, testURL, nil)
	if _, has := poolData(resp)["restore_proof"]; has || poolData(resp)["ok"] != false {
		t.Fatalf("failed probe must not produce a proof: %v", resp)
	}
	env.failKey = ""

	// A healthy key needs no proof.
	_, resp = poolAdmin(ctx, http.MethodPost, fmt.Sprintf("%s/%d/keys/0/test", poolBase, ch.Id), nil)
	if _, has := poolData(resp)["restore_proof"]; has {
		t.Fatal("healthy key must not get a proof")
	}

	_, resp = poolAdmin(ctx, http.MethodPost, testURL, nil)
	proof, _ := poolData(resp)["restore_proof"].(string)
	if proof == "" {
		t.Fatalf("passing probe on a blocked key must return a proof: %v", resp)
	}

	// The state moves on after the probe (key re-disabled with a new reason):
	// the old conclusion must not restore it.
	ch2, _ := repo.GetChannelById(ch.Id, true)
	ch2.ChannelInfo.MultiKeyDisabledReason[1] = "401 unauthorized"
	if err := ch2.SaveChannelInfo(); err != nil {
		t.Fatal(err)
	}
	code, resp := poolAdmin(ctx, http.MethodPost, restoreURL, map[string]string{"proof": proof})
	if code != http.StatusConflict || resp["error_code"] != "proof_stale" {
		t.Fatalf("stale proof = %d %v, want 409 proof_stale", code, resp)
	}
	if got, _ := repo.GetChannelById(ch.Id, true); got.ChannelInfo.MultiKeyStatusList[1] != common.ChannelStatusAutoDisabled {
		t.Fatal("stale proof must not lift the block")
	}

	// A forged proof is refused too.
	if code, _ := poolAdmin(ctx, http.MethodPost, restoreURL, map[string]string{"proof": fmt.Sprintf("%d.%064d", now.Unix(), 0)}); code != http.StatusConflict {
		t.Errorf("forged proof = %d, want 409", code)
	}

	// Re-test against the new state, then restore succeeds exactly once.
	_, resp = poolAdmin(ctx, http.MethodPost, testURL, nil)
	proof, _ = poolData(resp)["restore_proof"].(string)
	if code, resp := poolAdmin(ctx, http.MethodPost, restoreURL, map[string]string{"proof": proof}); code != http.StatusOK {
		t.Fatalf("fresh proof = %d %v", code, resp)
	}
	got, _ := repo.GetChannelById(ch.Id, true)
	if _, blocked := got.ChannelInfo.MultiKeyStatusList[1]; blocked || got.ChannelInfo.MultiKeyCooldownUntil[1] != 0 || got.ChannelInfo.MultiKeyDisabledReason[1] != "" {
		t.Fatalf("restore left residue: %+v", got.ChannelInfo)
	}
	if code, _ := poolAdmin(ctx, http.MethodPost, restoreURL, map[string]string{"proof": proof}); code != http.StatusConflict {
		t.Error("a used proof must not work again")
	}
}

func TestRestoreProof_ExpiresAfterMaxAge(t *testing.T) {
	st := keyState{KeyStatus: common.ChannelStatusAutoDisabled, Reason: "x", CooldownUntil: 99}
	now := time.Now()
	p := makeRestoreProof(7, 1, st, now)
	if !verifyRestoreProof(p, 7, 1, st, now.Add(time.Minute)) {
		t.Fatal("fresh proof must verify")
	}
	if verifyRestoreProof(p, 7, 1, st, now.Add(restoreProofMaxAge+time.Minute)) {
		t.Fatal("expired proof must not verify")
	}
	if verifyRestoreProof(p, 8, 1, st, now) || verifyRestoreProof(p, 7, 2, st, now) {
		t.Fatal("proof is bound to channel and key index")
	}
}

// A non-root admin without the channel:sensitive_write grant may dry-run but
// not import or change a key proxy: both put credentials / egress in play.
func TestPoolOps_SensitiveWriteNeedsGrant(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	newPoolOpsEnv(t)
	ch := poolSeedChannel(t, ctx, []string{"sk-a", "sk-b"}, nil)
	post := func(body interface{}) int {
		return V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, poolBase+"/import", body, []string{"admin"}).Code
	}
	if code := post(map[string]interface{}{"type": 1, "models": "model-a", "keys": "sk-x"}); code != http.StatusOK {
		t.Errorf("dry run as plain admin = %d, want 200", code)
	}
	if code := post(map[string]interface{}{"dry_run": false, "type": 1, "models": "model-a", "keys": "sk-x"}); code != http.StatusForbidden {
		t.Errorf("real import without grant = %d, want 403", code)
	}
	if poolChannelCount(t, ctx) != 1 {
		t.Error("refused import must write nothing")
	}
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, fmt.Sprintf("%s/%d/keys/0/settings", poolBase, ch.Id),
		map[string]string{"proxy": publicProxyURL}, []string{"admin"})
	if w.Code != http.StatusForbidden {
		t.Errorf("proxy change without grant = %d, want 403", w.Code)
	}
}
