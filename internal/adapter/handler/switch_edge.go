package handler

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

// Switch Edge Provision (ADR-027 / contracts「Switch Edge Provision」).
// Bare JSON shapes match 2c-gui-switch/internal/edge — no {success,data} envelope.
//
// Partner cloud is not wired yet. Default POST /instances returns
// provision.partner_error so Switch falls back to affiliate self-buy.
// Set EDGE_PROVISION_MODE=mock and EDGE_MOCK_HOST=<ip> for desktop/Hub contract tests.

type switchEdgeCatalogItem struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	NameEN    string   `json:"nameEn,omitempty"`
	Regions   []string `json:"regions"`
	MinSKU    string   `json:"minSku"`
	PriceHint string   `json:"priceHint"`
	PublicIP  bool     `json:"publicIP"`
	ICPNote   string   `json:"icpNote,omitempty"`
	Ports     []int    `json:"ports"`
	BuyURL    string   `json:"buyURL"`
	OneClick  bool     `json:"oneClick"`
	Checklist []string `json:"checklist,omitempty"`
}

type switchEdgeCatalog struct {
	Items  []switchEdgeCatalogItem `json:"items"`
	Ref    string                  `json:"ref,omitempty"`
	Source string                  `json:"source"`
}

type switchEdgeInstance struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	Host            string `json:"host,omitempty"`
	SSHPort         int    `json:"sshPort,omitempty"`
	User            string `json:"user,omitempty"`
	BootstrapSecret string `json:"bootstrapSecret,omitempty"`
	ErrorCode       string `json:"errorCode,omitempty"`
	ErrorMessage    string `json:"errorMessage,omitempty"`
	Region          string `json:"region,omitempty"`
	SKU             string `json:"sku,omitempty"`
}

type switchEdgeCreateBody struct {
	Region      string `json:"region"`
	SKU         string `json:"sku"`
	PurposeHint string `json:"purposeHint"`
}

type edgeStoredInstance struct {
	inst            switchEdgeInstance
	ownerKey        string
	bootstrapOnce   string
	bootstrapIssued bool
}

var (
	edgeInstMu sync.Mutex
	edgeInst   = map[string]*edgeStoredInstance{}
	edgeIdem   = map[string]string{} // idempotency-key → instance id
)

func edgeOwnerKey(c *gin.Context) string {
	// RequireOIDCToken sets oidc_user_id (= JWT sub or identity:<id>).
	if v, ok := c.Get("oidc_user_id"); ok {
		if s, _ := v.(string); s != "" {
			return "oidc:" + s
		}
	}
	if v, ok := c.Get("idp_subject"); ok {
		if s, _ := v.(string); s != "" {
			return "sub:" + s
		}
	}
	if v, ok := c.Get("user_id"); ok {
		switch t := v.(type) {
		case int:
			return "uid:" + itoaEdge(t)
		case int64:
			return "uid:" + itoaEdge(int(t))
		}
	}
	return "anon"
}

func itoaEdge(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func edgeNewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "edge-" + hex.EncodeToString(b[:])
}

func edgeMockEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("EDGE_PROVISION_MODE")), "mock")
}

func edgeMockHost() string {
	h := strings.TrimSpace(os.Getenv("EDGE_MOCK_HOST"))
	if h == "" {
		h = "203.0.113.50"
	}
	return h
}

// GetSwitchEdgeCatalog handles GET /api/v2/switch/edge/catalog.
func GetSwitchEdgeCatalog(c *gin.Context) {
	ref := strings.TrimSpace(c.Query("ref"))
	if ref == "" {
		if v, ok := c.Get("oidc_user_id"); ok {
			ref, _ = v.(string)
		}
	}
	if ref == "" {
		if v, ok := c.Get("idp_subject"); ok {
			ref, _ = v.(string)
		}
	}
	ports := []int{22, 80, 443, 7000}
	items := []switchEdgeCatalogItem{
		{
			ID: "cn-light-generic", Name: "国内轻量云(自助)", NameEN: "CN lightweight VPS (BYO)",
			Regions: []string{"cn-east", "cn-south"}, MinSKU: "1C1G",
			PriceHint: "约 ¥20–40/月", PublicIP: true,
			ICPNote: "用域名对外且落中国大陆机房需备案", Ports: ports,
			BuyURL: edgeBuyURL("https://www.aliyun.com/product/swas", ref), OneClick: false,
			Checklist: []string{
				"选「有公网 IP」的轻量/云服务器,系统选 Ubuntu 22.04+",
				"安全组放行 TCP 22、80、443、7000",
				"记下公网 IP 与 root(或可 sudo)登录方式",
				"回到 Switch 填入地址并一键部署中继",
			},
		},
		{
			ID: "global-budget-vps", Name: "海外预算 VPS(自助)", NameEN: "Budget VPS (global)",
			Regions: []string{"sg", "jp", "us"}, MinSKU: "1C1G",
			PriceHint: "~$3–6/mo", PublicIP: true,
			ICPNote: "海外机房一般无需 ICP", Ports: ports,
			BuyURL: edgeBuyURL("https://www.vultr.com/products/cloud-compute/", ref), OneClick: false,
			Checklist: []string{
				"Create a VM with a public IPv4, Ubuntu 22.04+",
				"Open TCP 22, 80, 443, 7000 in the firewall",
				"Copy the public IP and root/SSH key",
				"Paste into Switch and deploy the relay",
			},
		},
		{
			ID: "lurus-oneclick", Name: "Lurus 一键开通", NameEN: "Lurus one-click edge",
			Regions: []string{"auto"}, MinSKU: "edge-s",
			PriceHint: "按账号余额计费", PublicIP: true, Ports: ports,
			OneClick: true,
			Checklist: []string{
				"需登录 Lurus 账号且余额充足",
				"开通后自动回填公网地址与一次性登录凭据",
				"不用的实例请在本页释放,避免持续扣费",
			},
		},
	}
	c.JSON(http.StatusOK, switchEdgeCatalog{Items: items, Ref: ref, Source: "hub"})
}

func edgeBuyURL(base, ref string) string {
	if ref == "" {
		return base
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "ref=" + ref
}

// CreateSwitchEdgeInstance handles POST /api/v2/switch/edge/instances.
func CreateSwitchEdgeInstance(c *gin.Context) {
	var body switchEdgeCreateBody
	_ = c.ShouldBindJSON(&body)
	idem := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	owner := edgeOwnerKey(c)

	edgeInstMu.Lock()
	defer edgeInstMu.Unlock()

	if idem != "" {
		if id, ok := edgeIdem[owner+"|"+idem]; ok {
			if st, ok := edgeInst[id]; ok && st.ownerKey == owner {
				c.JSON(http.StatusOK, edgePublicView(st))
				return
			}
		}
	}

	if !edgeMockEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"errorCode":    "provision.partner_error",
			"errorMessage": "一键开通尚未对接合作云,请改用自助购买",
		})
		return
	}

	id := edgeNewID()
	secret := "mock-" + edgeNewID()
	st := &edgeStoredInstance{
		ownerKey:      owner,
		bootstrapOnce: secret,
		inst: switchEdgeInstance{
			ID:     id,
			Status: "ready",
			Host:   edgeMockHost(),
			SSHPort: 22,
			User:   "root",
			Region: body.Region,
			SKU:    body.SKU,
		},
	}
	if st.inst.Region == "" {
		st.inst.Region = "auto"
	}
	if st.inst.SKU == "" {
		st.inst.SKU = "edge-s"
	}
	edgeInst[id] = st
	if idem != "" {
		edgeIdem[owner+"|"+idem] = id
	}
	c.JSON(http.StatusOK, edgePublicView(st))
}

// GetSwitchEdgeInstance handles GET /api/v2/switch/edge/instances/:id.
func GetSwitchEdgeInstance(c *gin.Context) {
	id := c.Param("id")
	owner := edgeOwnerKey(c)
	edgeInstMu.Lock()
	defer edgeInstMu.Unlock()
	st, ok := edgeInst[id]
	if !ok || st.ownerKey != owner {
		c.JSON(http.StatusNotFound, gin.H{
			"errorCode":    "provision.partner_error",
			"errorMessage": "实例不存在或无权访问",
		})
		return
	}
	c.JSON(http.StatusOK, edgePublicView(st))
}

// DeleteSwitchEdgeInstance handles DELETE /api/v2/switch/edge/instances/:id.
func DeleteSwitchEdgeInstance(c *gin.Context) {
	id := c.Param("id")
	owner := edgeOwnerKey(c)
	edgeInstMu.Lock()
	defer edgeInstMu.Unlock()
	st, ok := edgeInst[id]
	if !ok || st.ownerKey != owner {
		c.Status(http.StatusNoContent)
		return
	}
	st.inst.Status = "released"
	st.inst.Host = ""
	st.bootstrapOnce = ""
	st.bootstrapIssued = true
	delete(edgeInst, id)
	c.Status(http.StatusNoContent)
}

func edgePublicView(st *edgeStoredInstance) switchEdgeInstance {
	out := st.inst
	if out.Status == "ready" && !st.bootstrapIssued && st.bootstrapOnce != "" {
		out.BootstrapSecret = st.bootstrapOnce
		st.bootstrapIssued = true
	}
	return out
}

// ResetSwitchEdgeStoreForTest clears in-memory instances (tests only).
func ResetSwitchEdgeStoreForTest() {
	edgeInstMu.Lock()
	defer edgeInstMu.Unlock()
	edgeInst = map[string]*edgeStoredInstance{}
	edgeIdem = map[string]string{}
}
