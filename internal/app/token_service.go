package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

const (
	TokenNameMaxLength = 50
	MaxQuotaMultiplier = 1000000000
	// MaxTokenExpiredTime is 3000-01-01T00:00:00Z in epoch seconds. Anything
	// later is almost certainly a unit mistake (e.g. milliseconds sent as
	// seconds) and would silently behave like "never expires", so reject it.
	MaxTokenExpiredTime = 32503680000
	// MaxRateLimitPerMinute bounds rpm_limit / tpm_limit (token AND tenant
	// dimensions). 10^9 per minute is far beyond any real workload — anything
	// larger is a unit mistake, and the cap keeps window arithmetic (int64
	// sums of int values) trivially overflow-free.
	MaxRateLimitPerMinute = 1_000_000_000
)

// ValidateTokenName checks that the token name does not exceed the maximum length.
func ValidateTokenName(name string) error {
	if len(name) > TokenNameMaxLength {
		return errors.New("令牌名称过长")
	}
	return nil
}

// ValidateTokenQuota validates the token quota values.
// If unlimitedQuota is true, no quota validation is performed.
func ValidateTokenQuota(remainQuota int, unlimitedQuota bool) error {
	if unlimitedQuota {
		return nil
	}
	if remainQuota < 0 {
		return errors.New("额度值不能为负数")
	}
	maxQuotaValue := int(MaxQuotaMultiplier * common.QuotaPerUnit)
	if remainQuota > maxQuotaValue {
		return fmt.Errorf("额度值超出有效范围，最大值为 %d", maxQuotaValue)
	}
	return nil
}

// ValidateTokenExpiredTime checks that expired_time is within a sane range.
// -1 (never expires) and 0 (unset) are accepted; explicit "never expires"
// must use -1 rather than a far-future timestamp.
func ValidateTokenExpiredTime(expiredTime int64) error {
	if expiredTime > MaxTokenExpiredTime {
		return errors.New("过期时间无效，不能晚于公元 3000 年，永不过期请使用 -1")
	}
	return nil
}

// ValidateRateLimits checks per-minute rate-limit values (RPM/TPM, token or
// tenant dimension): non-negative and at most MaxRateLimitPerMinute. 0 means
// unlimited and is always valid.
func ValidateRateLimits(rpm, tpm int) error {
	if rpm < 0 || tpm < 0 {
		return errors.New("速率限制不能为负数（0 表示不限制）")
	}
	if rpm > MaxRateLimitPerMinute || tpm > MaxRateLimitPerMinute {
		return fmt.Errorf("速率限制超出有效范围，最大值为 %d", MaxRateLimitPerMinute)
	}
	return nil
}

// NormalizeTokenScopes validates and canonicalizes a scope list. Each entry
// must be one of types.ValidTokenScopes. Whitespace is trimmed; duplicates
// are dropped; the result is sorted by the canonical order of
// types.ValidTokenScopes so equal inputs produce equal stored strings (which
// keeps audit diffs deterministic). nil/empty input returns "" — i.e. "no
// restriction", consistent with HasScope semantics.
func NormalizeTokenScopes(scopes []string) (string, error) {
	if len(scopes) == 0 {
		return "", nil
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, s := range scopes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !types.IsValidTokenScope(s) {
			return "", fmt.Errorf("令牌权限范围无效: %q", s)
		}
		seen[s] = struct{}{}
	}
	ordered := make([]string, 0, len(seen))
	for _, v := range types.ValidTokenScopes {
		if _, ok := seen[v]; ok {
			ordered = append(ordered, v)
		}
	}
	return strings.Join(ordered, ","), nil
}

// CanEnableToken checks whether a token can be enabled based on its current state.
// Returns nil if the token can be enabled, or an error describing why it cannot.
func CanEnableToken(token *repo.Token) error {
	if token.Status == common.TokenStatusExpired &&
		token.ExpiredTime <= common.GetTimestamp() &&
		token.ExpiredTime != -1 {
		return errors.New("令牌已过期，无法启用，请先修改令牌过期时间，或者设置为永不过期")
	}
	if token.Status == common.TokenStatusExhausted &&
		token.RemainQuota <= 0 &&
		!token.UnlimitedQuota {
		return errors.New("令牌可用额度已用尽，无法启用，请先修改令牌剩余额度，或者设置为无限额度")
	}
	return nil
}

// GenerateTokenKey generates a new unique token key.
func GenerateTokenKey() (string, error) {
	key, err := common.GenerateKey()
	if err != nil {
		common.SysLog("failed to generate token key: " + err.Error())
		return "", errors.New("生成令牌失败")
	}
	return key, nil
}

// BuildCleanToken creates a sanitized Token struct for insertion.
func BuildCleanToken(userId int, tenantId string, token *repo.Token, key string) repo.Token {
	return repo.Token{
		UserId:             userId,
		TenantId:           tenantId,
		Name:               token.Name,
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        token.ExpiredTime,
		RemainQuota:        token.RemainQuota,
		UnlimitedQuota:     token.UnlimitedQuota,
		ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits:        token.ModelLimits,
		AllowIps:           token.AllowIps,
		Group:              token.Group,
		CrossGroupRetry:    token.CrossGroupRetry,
		Scopes:             token.Scopes,
		RateLimitRPM:       token.RateLimitRPM,
		RateLimitTPM:       token.RateLimitTPM,
	}
}

// ApplyTokenUpdate copies update fields from the source token to the target.
// Returns the updated target token.
func ApplyTokenUpdate(target *repo.Token, source *repo.Token) {
	target.Name = source.Name
	target.ExpiredTime = source.ExpiredTime
	target.RemainQuota = source.RemainQuota
	target.UnlimitedQuota = source.UnlimitedQuota
	target.ModelLimitsEnabled = source.ModelLimitsEnabled
	target.ModelLimits = source.ModelLimits
	target.AllowIps = source.AllowIps
	target.Group = source.Group
	target.CrossGroupRetry = source.CrossGroupRetry
	target.Scopes = source.Scopes
	target.RateLimitRPM = source.RateLimitRPM
	target.RateLimitTPM = source.RateLimitTPM
}
