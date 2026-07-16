package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var switchUserInfoDBCounter atomic.Int64

type switchUserInfoTestCtx struct {
	router *gin.Engine
	db     *gorm.DB
	user   *repo.User
}

// setupSwitchUserInfoTest spins up an in-memory SQLite with the tables the
// handler touches and wires the route onto a no-middleware gin engine —
// same harness shape as setupHeartbeatTest.
func setupSwitchUserInfoTest(t *testing.T) *switchUserInfoTestCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:suitest%d?mode=memory&cache=shared", switchUserInfoDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	for _, tbl := range []interface{}{&repo.User{}, &repo.Token{}} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	t.Cleanup(func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	user := &repo.User{
		Username:    fmt.Sprintf("suiuser%d", switchUserInfoDBCounter.Load()),
		DisplayName: "SUI User",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "suiuser@test.local",
		Group:       "default",
		Quota:       500_000,
		UsedQuota:   120_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	router := gin.New()
	router.GET("/api/v2/switch/user/info", GetSwitchUserInfo)

	return &switchUserInfoTestCtx{router: router, db: db, user: user}
}

func (s *switchUserInfoTestCtx) seedToken(t *testing.T, override repo.Token) *repo.Token {
	t.Helper()
	tok := repo.Token{
		UserId:         s.user.Id,
		Key:            common.GetRandomString(32),
		Status:         common.TokenStatusEnabled,
		Name:           "sui-test-token",
		CreatedTime:    common.GetTimestamp(),
		AccessedTime:   common.GetTimestamp(),
		ExpiredTime:    -1,
		RemainQuota:    200_000,
		UnlimitedQuota: false,
		Group:          "default",
	}
	if override.Status != 0 {
		tok.Status = override.Status
	}
	if override.RemainQuota != 0 {
		tok.RemainQuota = override.RemainQuota
	}
	if override.UnlimitedQuota {
		tok.UnlimitedQuota = true
	}
	if err := s.db.Create(&tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return &tok
}

func (s *switchUserInfoTestCtx) get(t *testing.T, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/switch/user/info", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

func TestGetSwitchUserInfo_LimitedToken(t *testing.T) {
	ctx := setupSwitchUserInfoTest(t)
	tok := ctx.seedToken(t, repo.Token{})

	// Bearer + sk- prefixes must both be tolerated (client sends Bearer sk-<key>).
	w := ctx.get(t, "Bearer sk-"+tok.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Quota          int    `json:"quota"`
			UsedQuota      int    `json:"used_quota"`
			RemainingQuota int    `json:"remaining_quota"`
			Username       string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !envelope.Success {
		t.Fatal("success = false, want true")
	}
	if envelope.Data.Quota != 500_000 || envelope.Data.UsedQuota != 120_000 {
		t.Errorf("quota/used = %d/%d, want 500000/120000", envelope.Data.Quota, envelope.Data.UsedQuota)
	}
	// Limited token: remaining is the token allowance (200k < user 500k).
	if envelope.Data.RemainingQuota != 200_000 {
		t.Errorf("remaining_quota = %d, want 200000 (token-capped)", envelope.Data.RemainingQuota)
	}
	if envelope.Data.Username != ctx.user.Username {
		t.Errorf("username = %q, want %q", envelope.Data.Username, ctx.user.Username)
	}
}

func TestGetSwitchUserInfo_UnlimitedTokenUsesUserBalance(t *testing.T) {
	ctx := setupSwitchUserInfoTest(t)
	tok := ctx.seedToken(t, repo.Token{UnlimitedQuota: true, RemainQuota: 1})

	w := ctx.get(t, tok.Key) // raw key, no prefixes
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Data struct {
			RemainingQuota int `json:"remaining_quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.RemainingQuota != 500_000 {
		t.Errorf("remaining_quota = %d, want 500000 (user balance)", envelope.Data.RemainingQuota)
	}
}

func TestGetSwitchUserInfo_AuthFailures(t *testing.T) {
	ctx := setupSwitchUserInfoTest(t)
	disabled := ctx.seedToken(t, repo.Token{Status: common.TokenStatusDisabled})

	cases := []struct {
		name string
		auth string
	}{
		{"missing header", ""},
		{"unknown token", "sk-doesnotexist0000000000000000000000"},
		{"disabled token", disabled.Key},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := ctx.get(t, tc.auth)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
			}
		})
	}
}
