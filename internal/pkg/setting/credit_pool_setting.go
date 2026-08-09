package setting

import (
	"os"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// CREDIT_POOL_REQUIRED three-state gradual-enforcement flag (Phase 0 of the
// pool-required rollout). Today a tenant with no tenant_credit_pools row is
// treated as "unlimited" and the relay gate silently bypasses it — real
// consumption goes unbilled. This flag lets ops move from that legacy
// behaviour to a hard block without a code change:
//
//	off     (default) — legacy bypass, no signal. Byte-identical to
//	                     pre-flag behaviour.
//	log     — still bypasses the relay request, but counts + structured-logs
//	          the miss so ops can size the blast radius before enforcing.
//	enforce — the relay gate returns HTTP 402 (pool_not_configured) instead
//	          of bypassing.
const (
	CreditPoolRequiredOff     = "off"
	CreditPoolRequiredLog     = "log"
	CreditPoolRequiredEnforce = "enforce"
)

// GetCreditPoolRequired reads CREDIT_POOL_REQUIRED fresh on every call
// (mirrors the GetMonitorSetting os.Getenv-per-call style in
// operation_setting/monitor_setting.go — cheap, no init()-time caching, so
// tests can flip it with t.Setenv without a process restart).
//
// Any value other than "off"/"log"/"enforce" (including empty) degrades to
// "off" — this flag must NEVER fail-open to "enforce" on a typo, so an
// unrecognized value is logged via SysError and treated exactly like "off".
func GetCreditPoolRequired() string {
	raw := os.Getenv("CREDIT_POOL_REQUIRED")
	switch raw {
	case "", CreditPoolRequiredOff:
		return CreditPoolRequiredOff
	case CreditPoolRequiredLog:
		return CreditPoolRequiredLog
	case CreditPoolRequiredEnforce:
		return CreditPoolRequiredEnforce
	default:
		common.SysError("invalid CREDIT_POOL_REQUIRED=" + raw + ", falling back to off (never fail-open to enforce)")
		return CreditPoolRequiredOff
	}
}
