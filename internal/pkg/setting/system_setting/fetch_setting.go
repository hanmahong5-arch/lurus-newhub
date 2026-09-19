package system_setting

import "github.com/LurusTech/lurus-hub/internal/pkg/setting/config"

type FetchSetting struct {
	EnableSSRFProtection   bool     `json:"enable_ssrf_protection"` // 是否启用SSRF防护
	AllowPrivateIp         bool     `json:"allow_private_ip"`
	DomainFilterMode       bool     `json:"domain_filter_mode"`         // 域名过滤模式，true: 白名单模式，false: 黑名单模式
	IpFilterMode           bool     `json:"ip_filter_mode"`             // IP过滤模式，true: 白名单模式，false: 黑名单模式
	DomainList             []string `json:"domain_list"`                // domain format, e.g. example.com, *.example.com
	IpList                 []string `json:"ip_list"`                    // CIDR format
	AllowedPorts           []string `json:"allowed_ports"`              // port range format, e.g. 80, 443, 8000-9000
	ApplyIPFilterForDomain bool     `json:"apply_ip_filter_for_domain"` // 对域名启用IP过滤（实验性）
}

var defaultFetchSetting = FetchSetting{
	EnableSSRFProtection:   true, // 默认开启SSRF防护
	AllowPrivateIp:         false,
	DomainFilterMode:       false,
	IpFilterMode:           false,
	DomainList:             []string{},
	IpList:                 []string{},
	AllowedPorts:           []string{"80", "443", "8080", "8443"},
	ApplyIPFilterForDomain: false,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("fetch_setting", &defaultFetchSetting)
}

// GetFetchSetting returns the live registered object. Since the config writer
// became copy-on-write the lists it hands out (DomainList, IpList,
// AllowedPorts) are not written again after publication, so a caller that
// reads one keeps a list somebody actually published; the field reads
// themselves are still unsynchronised, which is what GetFetchSettingSnapshot
// exists to fix for callers that take an SSRF decision.
func GetFetchSetting() *FetchSetting {
	return &defaultFetchSetting
}

// GetFetchSettingSnapshot returns a by-value copy of the fetch settings taken
// under the configuration read lock. The copy shares the published slices,
// which copy-on-write guarantees are immutable, so the whole decision a caller
// takes from it is against one coherent published configuration rather than a
// field-by-field mixture of two.
func GetFetchSettingSnapshot() FetchSetting {
	config.RLock()
	defer config.RUnlock()

	return defaultFetchSetting
}
