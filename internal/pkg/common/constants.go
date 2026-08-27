package common

import (
	//"os"
	//"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

var StartTime = time.Now().Unix() // unit: second
var Version = "v0.0.0"            // this hard coding will be replaced automatically when building, no need to manually change
var SystemName = "Lurus Hub"
var Footer = ""
var Logo = ""
var TopUpLink = ""

// var ChatLink = ""
// var ChatLink2 = ""
var QuotaPerUnit = 500 * 1000.0 // $0.002 / 1K tokens
// 保留旧变量以兼容历史逻辑，实际展示由 general_setting.quota_display_type 控制
var DisplayInCurrencyEnabled = true
var DisplayTokenStatEnabled = true
var DrawingEnabled = true
var TaskEnabled = true
var DataExportEnabled = true
var DataExportInterval = 5         // unit: minute
var DataExportDefaultTime = "hour" // unit: minute
var DefaultCollapseSidebar = false // default value of collapse sidebar

// Any options with "Secret", "Token" in its key won't be return by GetOptions

var SessionSecret = uuid.New().String()
var CryptoSecret = uuid.New().String()

var OptionMap map[string]string
var OptionMapRWMutex sync.RWMutex

var ItemsPerPage = 10
var MaxRecentItems = 100

var PasswordLoginEnabled = true
var PasswordRegisterEnabled = true
var EmailVerificationEnabled = false
var GitHubOAuthEnabled = false
var LinuxDOOAuthEnabled = false
var WeChatAuthEnabled = false
var TelegramOAuthEnabled = false
var TurnstileCheckEnabled = false
var RegisterEnabled = true

var EmailDomainRestrictionEnabled = false // 是否启用邮箱域名限制
var EmailAliasRestrictionEnabled = false  // 是否启用邮箱别名限制
var EmailDomainWhitelist = []string{
	"gmail.com",
	"163.com",
	"126.com",
	"qq.com",
	"outlook.com",
	"hotmail.com",
	"icloud.com",
	"yahoo.com",
	"foxmail.com",
}
var EmailLoginAuthServerList = []string{
	"smtp.sendcloud.net",
	"smtp.azurecomm.net",
}

var DebugEnabled bool
var MemoryCacheEnabled bool

var LogConsumeEnabled = true

var SMTPServer = ""
var SMTPPort = 587
var SMTPSSLEnabled = false
var SMTPAccount = ""
var SMTPFrom = ""
var SMTPToken = ""

var GitHubClientId = ""
var GitHubClientSecret = ""
var LinuxDOClientId = ""
var LinuxDOClientSecret = ""
var LinuxDOMinimumTrustLevel = 0

var WeChatServerAddress = ""
var WeChatServerToken = ""
var WeChatAccountQRCodeImageURL = ""

var TurnstileSiteKey = ""
var TurnstileSecretKey = ""

var TelegramBotToken = ""
var TelegramBotName = ""

var QuotaForNewUser = 0
var QuotaForInviter = 0
var QuotaForInvitee = 0
var ChannelDisableThreshold = 5.0
var AutomaticDisableChannelEnabled = false
var AutomaticEnableChannelEnabled = false
var QuotaRemindThreshold = 1000
var PreConsumedQuota = 500

var RetryTimes = 0

//var RootUserEmail = ""

var IsMasterNode bool

var requestInterval int
var RequestInterval time.Duration

var SyncFrequency int // unit is second

var BatchUpdateEnabled = false
var BatchUpdateInterval int

var RelayTimeout int // unit is second

var RelayMaxIdleConns int
var RelayMaxIdleConnsPerHost int

// Streaming-safe hung-upstream guards (B1/R3). RelayTimeout (http.Client.Timeout)
// is deliberately left at its default 0 — a TOTAL timeout would cut legitimate
// long SSE streams. Instead we bound only the two hang vectors that don't affect
// a healthy in-flight stream:
//   - RelayResponseHeaderTimeout caps time-to-first-response-header. It fires
//     ONLY before the first header byte; once 200+headers arrive the SSE body is
//     governed by the existing per-chunk streamingTimeout (300s), not this. A
//     hung provider that never responds now surfaces as a transport error
//     (ErrorCodeDoRequestFailed → 500) that trips the breaker and fails over.
//   - RelayDialTimeout caps TCP connection establishment.
// Tune via RELAY_RESPONSE_HEADER_TIMEOUT / RELAY_DIAL_TIMEOUT (seconds).
var (
	RelayResponseHeaderTimeout = 90 * time.Second
	RelayDialTimeout           = 10 * time.Second
)

// HealthDBPingTimeout bounds the /api/health DB ping (A1). On every readiness
// probe the handler pings the DB; without its own deadline a ping to a DOWN
// host can outlive the readinessProbe timeoutSeconds:2 window (~5s observed) and
// the pod keeps draining traffic as a "zombie". This caps the ping below that
// window. NOTE: the DSN statement_timeout governs queries on a LIVE connection,
// not connect/ping to a dead host — this is a distinct deadline. Tune via
// HEALTH_DB_PING_TIMEOUT_MS.
var HealthDBPingTimeout = 1500 * time.Millisecond

// DB/Redis boot connect-retry budget (A2). A transient dependency blip at
// startup (the DB/Redis pod not yet Ready when this pod boots) otherwise
// crashes the process on the first gorm.Open/ping → exit(1) → a burned K8s
// restart. RetryConnect (retry.go) wraps connect+ping with bounded exponential
// backoff so a cold cluster self-heals instead of crash-looping. The budget is
// intentionally small — worst case ≈ 1+2+4+8s backoff + 5×5s ping ≈ 40s, well
// under the startupProbe (~120s) — so a genuinely-misconfigured DSN only delays
// the terminal FatalLog at the call site by that much. Tune via DB_CONNECT_*.
var (
	DBConnectRetries        = 5
	DBConnectRetryBaseDelay = time.Second
	DBConnectRetryMaxDelay  = 10 * time.Second
	DBConnectPingTimeout    = 5 * time.Second
)

var GeminiSafetySetting string

// https://docs.cohere.com/docs/safety-modes Type; NONE/CONTEXTUAL/STRICT
var CohereSafetySetting string

const (
	RequestIdKey = "X-Oneapi-Request-Id"
)

const (
	RoleGuestUser      = 0
	RoleCommonUser     = 1
	RoleSubscriberUser = 5 // Paid subscriber user
	RoleAdminUser      = 10
	RoleRootUser       = 100
)

func IsValidateRole(role int) bool {
	return role == RoleGuestUser || role == RoleCommonUser || role == RoleSubscriberUser || role == RoleAdminUser || role == RoleRootUser
}

// IsSubscriber checks if role is subscriber or higher
func IsSubscriber(role int) bool {
	return role >= RoleSubscriberUser
}

var (
	FileUploadPermission    = RoleGuestUser
	FileDownloadPermission  = RoleGuestUser
	ImageUploadPermission   = RoleGuestUser
	ImageDownloadPermission = RoleGuestUser
)

// All duration's unit is seconds
// Shouldn't larger then RateLimitKeyExpirationDuration
var (
	GlobalApiRateLimitEnable   bool
	GlobalApiRateLimitNum      int
	GlobalApiRateLimitDuration int64

	GlobalWebRateLimitEnable   bool
	GlobalWebRateLimitNum      int
	GlobalWebRateLimitDuration int64

	CriticalRateLimitEnable   bool
	CriticalRateLimitNum            = 20
	CriticalRateLimitDuration int64 = 20 * 60

	UploadRateLimitNum            = 10
	UploadRateLimitDuration int64 = 60

	DownloadRateLimitNum            = 10
	DownloadRateLimitDuration int64 = 60

	// Internal API per-key rate limits. The bucket is keyed by the AUTHENTICATED
	// internal API-key id (not client IP), so a stolen key cannot be used to DoS
	// the internal plane or spam provisioning by spreading replays across many
	// source IPs. Three tiers: a general/default bucket on every /internal route,
	// a tighter bucket on write endpoints, and the tightest on provisioning +
	// credit-pool funding (the highest-impact operations). Conservative defaults —
	// tune via env with the owner once real traffic shape is known.
	InternalApiRateLimitEnable = true

	InternalApiReadRateLimitNum            = 600
	InternalApiReadRateLimitDuration int64 = 60

	InternalApiWriteRateLimitNum            = 120
	InternalApiWriteRateLimitDuration int64 = 60

	InternalApiProvisionRateLimitNum            = 60
	InternalApiProvisionRateLimitDuration int64 = 60
)

// Billing degradation (P1-2). When the platform billing breaker is OPEN, a relay
// can be admitted on fresh cached wallet balance instead of hard-failing 402 —
// bounded so an unreachable platform can't be exploited for unlimited unsecured
// spend. BillingDegradedSpendCapLB is the per-tenant ceiling of unsecured spend
// (in LB) admitted within a rolling BillingDegradedWindowSec window; once hit,
// further requests fail closed until the window rolls or the breaker recovers.
// The cap VALUE is an owner-signed business-risk decision — the conservative
// default ships the mechanism; tune via env with the owner.
var (
	BillingDegradedSpendCapLB float64 = 50.0
	BillingDegradedWindowSec  int     = 3600
)

var RateLimitKeyExpirationDuration = 20 * time.Minute

// CostSpikeProtection — per-user 5-minute sliding window safety net. Catches
// agent loops or runaway scripts that burn through quota faster than the
// monthly cap can intervene. On breach, the user account is auto-disabled —
// but only when CostSpikeEnforce is true (see below).
// Knobs (env): COST_SPIKE_PROTECTION_ENABLED, COST_SPIKE_HARD_LIMIT_PER_5MIN,
// COST_SPIKE_ENFORCE.
var (
	CostSpikeProtectionEnabled = true
	CostSpikeHardLimitPer5Min  = 50000 // quota units (~0.1 USD/5min at typical ratios)

	// CostSpikeEnforce gates whether a breach actually disables the account
	// and returns 429, versus just being logged/counted (observe mode).
	// Defaults to false (observe-only). Reasons:
	//   (i)   CostSpikeHardLimitPer5Min=50000 (~$0.10/5min) has never been
	//         crossed by real traffic, and until the write side was fixed it
	//         could not have been: PostConsumeQuota only recorded the window
	//         inside its platform-wallet gate, so on the live gateway 1 of 6
	//         tokens accumulated data and the other 5 were invisible to the
	//         fuse. Recording is unconditional now (quota.go, pinned by
	//         TestR6PostConsumeQuota_RecordsCostSpikeWindowWithoutIdentityAccount),
	//         which means flipping this flag to enforce would for the first
	//         time apply an uncalibrated threshold to traffic that has never
	//         been measured against it. That is an argument for observing
	//         first, not for leaving the write side broken.
	//   (ii)  The breach action is repo.DisableUserById — an auto-suspend
	//         whose failure mode is a self-inflicted outage for a paying
	//         customer, which is worse than a fuse that has never tripped.
	//   (iii) Observe first, collect the real distribution, then decide on
	//         enforcement. One env var (COST_SPIKE_ENFORCE=true) flips this
	//         back on; fully reversible.
	CostSpikeEnforce = false
)

const (
	UserStatusEnabled  = 1 // don't use 0, 0 is the default value!
	UserStatusDisabled = 2 // also don't use 0
)

const (
	TokenStatusEnabled   = 1 // don't use 0, 0 is the default value!
	TokenStatusDisabled  = 2 // also don't use 0
	TokenStatusExpired   = 3
	TokenStatusExhausted = 4
)

const (
	RedemptionCodeStatusEnabled  = 1 // don't use 0, 0 is the default value!
	RedemptionCodeStatusDisabled = 2 // also don't use 0
	RedemptionCodeStatusUsed     = 3 // also don't use 0
)

const (
	ChannelStatusUnknown          = 0
	ChannelStatusEnabled          = 1 // don't use 0, 0 is the default value!
	ChannelStatusManuallyDisabled = 2 // also don't use 0
	ChannelStatusAutoDisabled     = 3
)

// Phone Verification Modes - controls when phone verification is required
const (
	PhoneVerificationDisabled          = "disabled"           // Phone verification completely disabled
	PhoneVerificationOptional          = "optional"           // Phone verification is optional (default)
	PhoneVerificationRequiredLogin     = "required_login"     // Phone verification required for login
	PhoneVerificationRequiredSensitive = "required_sensitive" // Phone verification required for sensitive actions
)

// Registration Modes - controls how users can register
const (
	RegistrationModeOpen          = "open"           // Open registration (default)
	RegistrationModeInviteOnly    = "invite_only"    // Invitation code required
	RegistrationModeOAuthOnly     = "oauth_only"     // Only OAuth registration allowed
	RegistrationModePhoneVerified = "phone_verified" // Phone verification required for registration
	RegistrationModeClosed        = "closed"         // Registration closed
)

// Sensitive Action Types - actions that may require additional verification
const (
	SensitiveActionPayment        = "payment"
	SensitiveActionWithdrawal     = "withdrawal"
	SensitiveActionPasswordChange = "password_change"
	SensitiveAction2FAChange      = "2fa_change"
	SensitiveActionPhoneBind      = "phone_bind"
	SensitiveActionOAuthBind      = "oauth_bind"
	SensitiveActionAccountDelete  = "account_delete"
	SensitiveActionTokenGenerate  = "token_generate"
)

// Login and Registration Configuration Variables
var (
	// Phone Verification Configuration
	PhoneVerificationMode         = PhoneVerificationOptional // Current phone verification mode
	PhoneRequiredForLogin         = false                     // Require phone verification for login
	PhoneRequiredForPasswordReset = false                     // Require phone for password reset
	PhoneRequiredFor2FAChange     = false                     // Require phone for 2FA changes
	PhoneRequiredForPayment       = false                     // Require phone for payment operations
	PhoneRequiredForWithdrawal    = false                     // Require phone for withdrawal
	PhoneRequiredForPhoneBind     = false                     // Require old phone verification to bind new phone
	PhoneRequiredForAccountDelete = false                     // Require phone for account deletion
	PhoneRequiredForTokenGenerate = false                     // Require phone for API token generation
	PhoneRequiredForOAuthBind     = false                     // Require phone for OAuth binding

	// Registration Configuration
	RegistrationMode   = RegistrationModeOpen // Current registration mode
	SMSAutoRegister    = true                 // Auto-register new users via SMS login
	InviteCodeRequired = false                // Require invitation code for registration

	// Security Configuration
	SensitiveActionRequirePassword = false // Require password re-entry for sensitive actions
	SensitiveActionRequire2FA      = false // Require 2FA for sensitive actions
	SessionTimeoutMinutes          = 10080 // Session timeout in minutes (7 days default)
)
