package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupDeskDeviceDB(t *testing.T) func() {
	t.Helper()
	seq := taskGenericDBCounter.Add(1)
	dsn := fmt.Sprintf("file:deskdevice%d?mode=memory&cache=shared", seq)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.User{}, &repo.Token{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	prevDB := repo.DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	repo.DB = db
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	return func() {
		repo.DB = prevDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	}
}

func TestDeskDevice_StartApprovePollMintsSK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupDeskDeviceDB(t)
	defer cleanup()

	user := &repo.User{
		Username: "desk-mint-user",
		TenantId: "default",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
	}
	if err := user.Insert(); err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	r.POST("/code", StartDeskDeviceCode)
	r.POST("/approve", func(c *gin.Context) {
		c.Set("id", user.Id)
		ApproveDeskDeviceCode(c)
	})
	r.POST("/token", PollDeskDeviceToken)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/code", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	var start struct {
		Success bool `json:"success"`
		Data    struct {
			DeviceCode string `json:"device_code"`
			UserCode   string `json:"user_code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil || !start.Success || start.Data.DeviceCode == "" {
		t.Fatalf("start body: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	pendingBody, _ := json.Marshal(map[string]string{"device_code": start.Data.DeviceCode})
	req := httptest.NewRequest(http.MethodPost, "/token", bytes.NewReader(pendingBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("poll before approve: want 202, got %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	approveBody, _ := json.Marshal(map[string]string{"user_code": start.Data.UserCode})
	req = httptest.NewRequest(http.MethodPost, "/approve", bytes.NewReader(approveBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/token", bytes.NewReader(pendingBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("poll after approve: %d %s", w.Code, w.Body.String())
	}
	var minted struct {
		Success bool `json:"success"`
		Data    struct {
			Token string `json:"token"`
			Name  string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &minted); err != nil || !minted.Success {
		t.Fatalf("mint body: %s", w.Body.String())
	}
	if minted.Data.Name != deskTokenName || len(minted.Data.Token) < 10 || minted.Data.Token[:3] != "sk-" {
		t.Fatalf("minted token shape: %+v", minted.Data)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/token", bytes.NewReader(pendingBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("second poll: want 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestDeskDevice_ForeignApproveConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cleanup := setupDeskDeviceDB(t)
	defer cleanup()

	u1 := &repo.User{Username: "desk-a", TenantId: "default", Status: common.UserStatusEnabled, Role: common.RoleCommonUser}
	u2 := &repo.User{Username: "desk-b", TenantId: "default", Status: common.UserStatusEnabled, Role: common.RoleCommonUser}
	if err := u1.Insert(); err != nil {
		t.Fatal(err)
	}
	if err := u2.Insert(); err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	r.POST("/code", StartDeskDeviceCode)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/code", nil))
	var start struct {
		Data struct {
			UserCode string `json:"user_code"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &start)

	approve := func(uid int) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		body, _ := json.Marshal(map[string]string{"user_code": start.Data.UserCode})
		req := httptest.NewRequest(http.MethodPost, "/approve", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		c, _ := gin.CreateTestContext(rr)
		c.Request = req
		c.Set("id", uid)
		ApproveDeskDeviceCode(c)
		return rr
	}
	if got := approve(u1.Id); got.Code != http.StatusOK {
		t.Fatalf("first approve: %d %s", got.Code, got.Body.String())
	}
	if got := approve(u2.Id); got.Code != http.StatusConflict {
		t.Fatalf("foreign approve: want 409, got %d %s", got.Code, got.Body.String())
	}
}
