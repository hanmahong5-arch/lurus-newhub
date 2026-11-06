package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

func edgeTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("oidc_user_id", "user-edge-test")
		c.Set("idp_subject", "user-edge-test")
		c.Set("user_id", 42)
		c.Next()
	})
	r.GET("/api/v2/switch/edge/catalog", GetSwitchEdgeCatalog)
	r.POST("/api/v2/switch/edge/instances", CreateSwitchEdgeInstance)
	r.GET("/api/v2/switch/edge/instances/:id", GetSwitchEdgeInstance)
	r.DELETE("/api/v2/switch/edge/instances/:id", DeleteSwitchEdgeInstance)
	return r
}

func TestGetSwitchEdgeCatalog_Shape(t *testing.T) {
	ResetSwitchEdgeStoreForTest()
	w := httptest.NewRecorder()
	edgeTestRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v2/switch/edge/catalog", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var cat switchEdgeCatalog
	if err := json.Unmarshal(w.Body.Bytes(), &cat); err != nil {
		t.Fatal(err)
	}
	if cat.Source != "hub" || len(cat.Items) < 2 {
		t.Fatalf("%+v", cat)
	}
	var oneClick, self bool
	for _, it := range cat.Items {
		if it.OneClick {
			oneClick = true
		} else {
			self = true
			if !it.PublicIP || len(it.Ports) == 0 {
				t.Fatalf("%+v", it)
			}
		}
	}
	if !oneClick || !self {
		t.Fatal("catalog needs both tracks")
	}
}

func TestCreateSwitchEdgeInstance_PartnerNotConfigured(t *testing.T) {
	ResetSwitchEdgeStoreForTest()
	_ = os.Unsetenv("EDGE_PROVISION_MODE")
	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"region":"auto","sku":"edge-s","purposeHint":"local_gateway"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/switch/edge/instances", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-1")
	edgeTestRouter().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var er struct {
		Code string `json:"errorCode"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &er)
	if er.Code != "provision.partner_error" {
		t.Fatalf("%s", w.Body.String())
	}
}

func TestCreateSwitchEdgeInstance_MockReadyOnceSecret(t *testing.T) {
	ResetSwitchEdgeStoreForTest()
	t.Setenv("EDGE_PROVISION_MODE", "mock")
	t.Setenv("EDGE_MOCK_HOST", "198.51.100.9")

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"region":"auto","sku":"edge-s"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/switch/edge/instances", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-mock")
	edgeTestRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var inst switchEdgeInstance
	if err := json.Unmarshal(w.Body.Bytes(), &inst); err != nil {
		t.Fatal(err)
	}
	if inst.Status != "ready" || inst.Host != "198.51.100.9" || inst.BootstrapSecret == "" {
		t.Fatalf("%+v", inst)
	}

	w2 := httptest.NewRecorder()
	edgeTestRouter().ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v2/switch/edge/instances/"+inst.ID, nil))
	var again switchEdgeInstance
	_ = json.Unmarshal(w2.Body.Bytes(), &again)
	if again.BootstrapSecret != "" {
		t.Fatalf("bootstrap must be one-shot, got %q", again.BootstrapSecret)
	}

	// idempotent replay
	w3 := httptest.NewRecorder()
	body3 := bytes.NewBufferString(`{"region":"auto","sku":"edge-s"}`)
	req3 := httptest.NewRequest(http.MethodPost, "/api/v2/switch/edge/instances", body3)
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Idempotency-Key", "idem-mock")
	edgeTestRouter().ServeHTTP(w3, req3)
	var replay switchEdgeInstance
	_ = json.Unmarshal(w3.Body.Bytes(), &replay)
	if replay.ID != inst.ID {
		t.Fatalf("idempotency broken: %s vs %s", replay.ID, inst.ID)
	}

	w4 := httptest.NewRecorder()
	del := httptest.NewRequest(http.MethodDelete, "/api/v2/switch/edge/instances/"+inst.ID, nil)
	edgeTestRouter().ServeHTTP(w4, del)
	if w4.Code != http.StatusNoContent {
		t.Fatalf("delete %d", w4.Code)
	}
}
