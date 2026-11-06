package catalog_sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var catalogTestDBCounter atomic.Int64

func withCatalogSQLite(t *testing.T) {
	t.Helper()
	prevDB, prevLog := repo.DB, repo.LOG_DB
	prevSQLite, prevPG := common.UsingSQLite, common.UsingPostgreSQL
	name := fmt.Sprintf("file:catalogsync%d?mode=memory&cache=shared", catalogTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(name), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&entity.CatalogSyncRun{},
		&entity.CatalogStalePending{},
		&entity.CatalogPriceProposal{},
		&repo.Channel{},
		&repo.Ability{},
		&repo.Option{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo.DB = db
	repo.LOG_DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	repo.InitCol()
	t.Cleanup(func() {
		repo.DB, repo.LOG_DB = prevDB, prevLog
		common.UsingSQLite, common.UsingPostgreSQL = prevSQLite, prevPG
		if sqlDB, e := db.DB(); e == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
}

func TestApplyGuardsWithoutDB(t *testing.T) {
	if err := ConfirmStaleRemovals(ConfirmStaleRequest{}); !errors.Is(err, ErrEmptySelection) {
		t.Fatalf("empty confirm: %v", err)
	}
	if err := ConfirmStaleRemovals(ConfirmStaleRequest{ChannelId: 1, Models: []string{"m"}}); !errors.Is(err, ErrSourceVersionMismatch) {
		t.Fatalf("missing version: %v", err)
	}
	if err := ApplyPriceProposals(ApplyPriceRequest{}); !errors.Is(err, ErrEmptySelection) {
		t.Fatalf("empty apply: %v", err)
	}
	if err := ApplyPriceProposals(ApplyPriceRequest{IDs: []int64{1}}); !errors.Is(err, ErrSourceVersionMismatch) {
		t.Fatalf("apply version: %v", err)
	}
	if err := DismissPriceProposals(nil, ""); !errors.Is(err, ErrEmptySelection) {
		t.Fatalf("empty dismiss: %v", err)
	}
}

func TestProviderForChannelSupported(t *testing.T) {
	base := "https://example.test"
	cases := []struct {
		typ   int
		label string
	}{
		{constant.ChannelTypeOpenAI, "openai_compatible"},
		{constant.ChannelTypeOpenRouter, "openrouter"},
		{constant.ChannelTypeDeepSeek, "deepseek"},
		{constant.ChannelTypeZhipu_v4, "glm"},
		{constant.ChannelTypeVolcEngine, "volcengine"},
	}
	for _, tc := range cases {
		ch := &repo.Channel{Type: tc.typ, BaseURL: &base, Key: "k"}
		p, err := ProviderForChannel(ch)
		if err != nil {
			t.Fatalf("type %d: %v", tc.typ, err)
		}
		if p.Name() != tc.label {
			t.Fatalf("type %d name=%q want %q", tc.typ, p.Name(), tc.label)
		}
		// Empty BaseURL hits the constant default branch.
		if _, err := ProviderForChannel(&repo.Channel{Type: tc.typ, Key: "k"}); err != nil {
			t.Fatalf("default base type %d: %v", tc.typ, err)
		}
	}
	if _, err := ProviderForChannel(&repo.Channel{Type: 999}); !errors.Is(err, ErrUnsupportedChannelType) {
		t.Fatalf("unsupported: %v", err)
	}
}

func TestParseChannelAllowlist(t *testing.T) {
	if parseChannelAllowlist("") != nil {
		t.Fatal("empty should be nil")
	}
	m := parseChannelAllowlist("1, 2, bad, 0, -3,3")
	if !m[1] || !m[2] || !m[3] || m[0] {
		t.Fatalf("%v", m)
	}
}

func TestOfficialRatioHintsFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"model_ratio": map[string]any{
					"keep": 1.5,
					"skip": 2.0,
					"bad":  "x",
				},
			},
		})
	}))
	defer srv.Close()
	prev := officialRatioURL
	officialRatioURL = srv.URL
	t.Cleanup(func() { officialRatioURL = prev })

	hints, err := OfficialRatioHints(context.Background(), nil, []UpstreamModel{{ID: "keep"}, {ID: "bad"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hints) != 1 || hints[0].ModelName != "keep" || hints[0].Value != 1.5 {
		t.Fatalf("%#v", hints)
	}
}

func TestAsFloatVariants(t *testing.T) {
	cases := []struct {
		in   any
		want float64
		ok   bool
	}{
		{float64(1.2), 1.2, true},
		{float32(2), 2, true},
		{3, 3, true},
		{int64(4), 4, true},
		{json.Number("5.5"), 5.5, true},
		{"6.25", 6.25, true},
		{true, 0, false},
	}
	for _, tc := range cases {
		got, ok := asFloat(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("%v → %v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNearlyEqual(t *testing.T) {
	if !nearlyEqual(1.0, 1.0+1e-10) {
		t.Fatal("eps")
	}
	if nearlyEqual(1.0, 1.1) {
		t.Fatal("far")
	}
}

func TestOpenAICompatibleHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()
	p := &OpenAICompatibleProvider{Label: "t", BaseURL: srv.URL, HTTPClient: srv.Client()}
	if _, err := p.ListModels(context.Background()); err == nil {
		t.Fatal("want status error")
	}
}

func TestEngineRunWithSQLite(t *testing.T) {
	withCatalogSQLite(t)
	base := "https://upstream.test"
	ch := &repo.Channel{
		Id:                 9,
		Type:               constant.ChannelTypeOpenAI,
		Status:             common.ChannelStatusEnabled,
		Models:             "old-only,keep",
		BaseURL:            &base,
		Key:                "sk",
		LastSyncFetchCount: 2,
	}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}

	engine := &Engine{
		Now:             func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) },
		ForceBreakerOff: true,
		AllowAutoAdd:    true,
		ListChannels:    func() ([]*repo.Channel, error) { return []*repo.Channel{ch}, nil },
		ProviderFor: func(*repo.Channel) (Provider, error) {
			return stubProvider{models: []UpstreamModel{{ID: "keep"}, {ID: "new"}}}, nil
		},
		PriceHintsFor: func(context.Context, *repo.Channel, []UpstreamModel) ([]PriceHint, error) {
			return []PriceHint{{ModelName: "keep", Field: "model_ratio", Value: 0.5, Source: "test"}}, nil
		},
		UpdateChannel: func(c *repo.Channel) error {
			return repo.DB.Model(c).Updates(map[string]any{
				"models":                c.Models,
				"last_sync_fetch_count": c.LastSyncFetchCount,
			}).Error
		},
	}
	res, err := engine.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Channels) != 1 {
		t.Fatalf("%#v", res)
	}
	cr := res.Channels[0]
	if len(cr.Added) != 1 || cr.Added[0] != "new" {
		t.Fatalf("added=%v", cr.Added)
	}
	if len(cr.Stale) != 1 || cr.Stale[0] != "old-only" {
		t.Fatalf("stale=%v", cr.Stale)
	}
	if cr.ProposedPrices != 1 {
		t.Fatalf("proposals=%d", cr.ProposedPrices)
	}

	stale, err := ListOpenStale(9)
	if err != nil || len(stale) != 1 || stale[0].ModelName != "old-only" {
		t.Fatalf("stale rows=%v err=%v", stale, err)
	}
	props, err := ListOpenPriceProposals()
	if err != nil || len(props) != 1 {
		t.Fatalf("props=%v err=%v", props, err)
	}
	runs, err := ListRecentRuns(10)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}

	if err := ConfirmStaleRemovals(ConfirmStaleRequest{
		ChannelId: 9, Models: []string{"old-only"}, SourceVersion: stale[0].SourceVersion,
	}); err != nil {
		t.Fatal(err)
	}
	stale, _ = ListOpenStale(9)
	if len(stale) != 0 {
		t.Fatalf("stale after confirm: %v", stale)
	}

	if err := DismissPriceProposals([]int64{props[0].Id}, props[0].SourceVersion); err != nil {
		t.Fatal(err)
	}
	props, _ = ListOpenPriceProposals()
	if len(props) != 0 {
		t.Fatalf("props after dismiss: %v", props)
	}
}

func TestEngineCircuitBreaker(t *testing.T) {
	withCatalogSQLite(t)
	base := "https://x"
	ch := &repo.Channel{Id: 3, Type: 1, Status: 1, Models: "a", BaseURL: &base, LastSyncFetchCount: 40}
	engine := &Engine{
		Now:          time.Now,
		ListChannels: func() ([]*repo.Channel, error) { return []*repo.Channel{ch}, nil },
		ProviderFor: func(*repo.Channel) (Provider, error) {
			return stubProvider{models: []UpstreamModel{{ID: "a"}}}, nil
		},
	}
	res, err := engine.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Channels[0].CircuitBreaker || !res.Channels[0].Skipped {
		t.Fatalf("%#v", res.Channels[0])
	}
}

func TestAutoCatalogSyncDisabledPaths(t *testing.T) {
	prevMaster := common.IsMasterNode
	t.Cleanup(func() { common.IsMasterNode = prevMaster })
	common.IsMasterNode = false
	AutoCatalogSyncWithContext(context.Background(), 60)

	common.IsMasterNode = true
	AutoCatalogSyncWithContext(context.Background(), 0)
}

func TestAutoCatalogSyncStopsOnCancel(t *testing.T) {
	prevMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = prevMaster })
	prevLeader := common.IsLeader()
	common.SetLeader(false)
	t.Cleanup(func() { common.SetLeader(prevLeader) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		AutoCatalogSyncWithContext(ctx, 1)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AutoCatalogSyncWithContext did not stop")
	}
}

func TestEngineResolvesStaleWhenModelReturns(t *testing.T) {
	withCatalogSQLite(t)
	base := "https://u"
	ch := &repo.Channel{
		Id: 21, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Models: "a,b", BaseURL: &base, LastSyncFetchCount: 2,
	}
	engine := &Engine{
		Now:             func() time.Time { return time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC) },
		ForceBreakerOff: true,
		AllowAutoAdd:    true,
		ListChannels:    func() ([]*repo.Channel, error) { return []*repo.Channel{ch}, nil },
		UpdateChannel: func(c *repo.Channel) error {
			return repo.DB.Model(c).Update("models", c.Models).Error
		},
		PriceHintsFor: nil,
	}
	engine.ProviderFor = func(*repo.Channel) (Provider, error) {
		return stubProvider{models: []UpstreamModel{{ID: "a"}}}, nil
	}
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	stale, _ := ListOpenStale(21)
	if len(stale) != 1 || stale[0].ModelName != "b" {
		t.Fatalf("stale=%v", stale)
	}
	engine.ProviderFor = func(*repo.Channel) (Provider, error) {
		return stubProvider{models: []UpstreamModel{{ID: "a"}, {ID: "b"}}}, nil
	}
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	stale, _ = ListOpenStale(21)
	if len(stale) != 0 {
		t.Fatalf("expected stale cleared when model returns: %v", stale)
	}
}

func TestSupportedChannelTypes(t *testing.T) {
	m := SupportedChannelTypes()
	if !m[constant.ChannelTypeOpenAI] || !m[constant.ChannelTypeVolcEngine] {
		t.Fatalf("%v", m)
	}
}

func TestNewEngineDefaults(t *testing.T) {
	e := NewEngine()
	if e.ProviderFor == nil || e.PriceHintsFor == nil || !e.AllowAutoAdd {
		t.Fatalf("%#v", e)
	}
}

func TestApplyPriceProposalsSuccess(t *testing.T) {
	withCatalogSQLite(t)
	repo.InitOptionMap()
	ratio_setting.InitRatioSettings()
	t.Cleanup(func() { ratio_setting.InitRatioSettings() })
	prev := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(prev) })

	row := entity.CatalogPriceProposal{
		ModelName: "gpu-qwen", Field: "model_ratio", LocalValue: 1, UpstreamValue: 0.42,
		Source: "test", SourceVersion: "v-apply",
	}
	if err := repo.DB.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := ApplyPriceProposals(ApplyPriceRequest{SourceVersion: "v-apply", IDs: []int64{row.Id}}); err != nil {
		t.Fatal(err)
	}
	props, err := ListOpenPriceProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 0 {
		t.Fatalf("still open: %v", props)
	}
	got := ratio_setting.GetModelRatioCopy()["gpu-qwen"]
	if got != 0.42 {
		t.Fatalf("ratio=%v", got)
	}
}

func TestListEligibleChannelsFilters(t *testing.T) {
	withCatalogSQLite(t)
	t.Setenv("CATALOG_SYNC_CHANNEL_IDS", "")
	base := "https://e"
	ok := &repo.Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, BaseURL: &base, Models: "m", Name: "ok"}
	disabled := &repo.Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusManuallyDisabled, BaseURL: &base, Models: "m", Name: "off"}
	unsupported := &repo.Channel{Type: 999, Status: common.ChannelStatusEnabled, BaseURL: &base, Models: "m", Name: "bad"}
	for _, ch := range []*repo.Channel{ok, disabled, unsupported} {
		if err := repo.DB.Create(ch).Error; err != nil {
			t.Fatal(err)
		}
	}
	list, err := listEligibleChannels()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "ok" {
		t.Fatalf("%#v", list)
	}
	t.Setenv("CATALOG_SYNC_CHANNEL_IDS", "99")
	list, err = listEligibleChannels()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("allowlist should empty: %#v", list)
	}
}
