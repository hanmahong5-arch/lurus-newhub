package middleware

// wire_message_language_gate_test.go - the structural lock for the "API
// error.message is English-only" contract. Born in cycle13 L3 over the error
// CONSTRUCTORS; widened in cycle14 L4 to responses written DIRECTLY, after
// this gate spent a whole cycle green while sitting in the same package as
// middleware/auth.go's eight Chinese refusals - auth.go writes them with
// c.JSON(gin.H{...}), which was not in the sink list. The lesson is in the
// blind-spot section below: a gate whose header oversells it is worse than
// no gate.
//
// WHAT IT SCANS
//
// Every non-test .go file under these roots, recursively (so subpackages
// like app/relay/helper and adapter/handler/router are covered):
//
//	internal/adapter/middleware
//	internal/adapter/repo
//	internal/app/relay
//	internal/adapter/handler     (added cycle14 L4)
//
// for CallExpr nodes whose callee is one of:
//
//   - abortWithOpenAiMessage (bare identifier - this package's own helper)
//   - MidjourneyErrorWrapper (bare or qualified, e.g. app.MidjourneyErrorWrapper)
//   - errors.New / fmt.Errorf (qualified by those exact package names)
//   - a qualified call whose selector name has the prefix "NewError" (covers
//     types.NewError / types.NewErrorWithStatusCode from any import alias)
//   - a DIRECT response write: any call whose method name is in
//     wireMessageGateResponseWriters (JSON, AbortWithStatusJSON, String,
//     Data, ...) AND that has at least two arguments. The arity test is the
//     false-positive control for Stringer: `sb.String()` is not a response.
//   - this repo's own response wrappers, wireMessageGateResponseHelpers
//     (common.ApiErrorMsg / common.ApiError, which are one-line c.JSON calls
//     in internal/pkg/common/gin.go).
//
// For every matched call, each argument is walked through "+" chains,
// composite literals (gin.H{...}, &T{...}) and parentheses, and each string
// literal reached is decoded and checked for a rune >= 0x80.
//
// BLIND SPOTS - what a green run here does NOT prove
//
//  1. It reads LITERALS. A message assembled with %s from a helper in another
//     package is ASCII here and not necessarily ASCII on the wire: the
//     pre-consume 402 built in internal/app/quota.go interpolated
//     logger.FormatQuota, which renders a fullwidth currency sign, and a live
//     UAT probe caught it after this gate had been green all cycle
//     (2026-09-21). internal/app is not scanned at all. The boundary oracle
//     for that path is
//     TestPreConsumeTokenQuota_WireMessageIsASCII_UnderEveryDisplayType.
//
//  2. It reads call ARGUMENTS. A message that reaches the wire by ASSIGNMENT
//     is invisible. Measured 2026-09-21: 327 code lines in these four roots
//     carry a CJK character inside quotes, while the whitelist below (181
//     entries) plus zero violations is everything this gate can see. Most of
//     the difference is server logging, correctly out of scope - but not all
//     of it: handler/midjourney.go:127's
//     `responseItem.FailReason = "..."` is persisted onto the task row and
//     served back by the MJ task-fetch endpoint, and
//     app/relay/compatible_handler.go:204's
//     `extraContent = append(extraContent, "...")` becomes the consume-log
//     content column that /api/log/self serves back. Neither is an argument
//     of a matched call.
//
//  3. It matches sink NAMES. A future response wrapper under a new name is
//     invisible until someone adds it to one of the two maps. The same goes
//     for a response written through c.Render or a hand-rolled writer.
//
//  4. Inside a composite literal it walks VALUES, never KEYS: a map key is a
//     field name, not a sentence. A non-ASCII map key is invisible.
//
//  5. It cannot tell a MESSAGE from DATA, because a gin.H value is just a
//     value: `"name_cn": "..."` looks exactly like `"message": "..."`. That
//     is why nine handler/internal_currency.go entries and five
//     openrouter_sync.go labels sit in the whitelist rather than being fixed.
//
//  6. It does not do interprocedural dataflow: a literal buried inside a
//     nested, non-target call passed as an argument is not followed. It does
//     not need to recurse into nested TARGET calls (errors.New("...") inside
//     types.NewError) - ast.Inspect visits every CallExpr once regardless of
//     nesting, so the inner call is matched on its own.
//
// WHITELIST
//
// wireMessageGateWhitelist is keyed by "<repo-relative file> | <decoded
// literal>" and NOT by line number - other lanes edit these files in parallel
// and a line-keyed entry would turn an unrelated insertion above it into a
// red gate. The trade-off, stated plainly: identical text at a NEW site in
// the same file is whitelisted without a new entry (repo/user.go already has
// nine "id ..." sites behind one entry). Each entry carries a reason; the
// reason classes are listed inside the map. A whitelist hit still counts
// toward sitesSeen (scanner honesty) and is checked for staleness - every
// entry must match a scanned site this run, or the whitelist has drifted
// from the source.
//
// Violation output prints the key QUOTED so it can be pasted into the map
// verbatim; several literals in this tree end in a space.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// wireMessageGateWhitelist maps "<repo-relative, forward-slash file path> |
// <decoded literal>" to a short reason. The reason classes are listed at the
// head of the cycle14 block below; see the file-level comment for why the key
// is the literal's text rather than its line.
var wireMessageGateWhitelist = map[string]string{
	"internal/adapter/repo/redemption.go | 无效的兑换码":       "ErrRedemptionInvalid — switch-contract text, cycle13 plan §2 (unchanged this cycle)",
	"internal/adapter/repo/redemption.go | 该兑换码已使用":      "ErrRedemptionUsed — switch-contract text, cycle13 plan §2 (the finding #7 fix: 已被使用 → 已使用)",
	"internal/adapter/repo/redemption.go | 该兑换码已过期":      "ErrRedemptionExpired — switch-contract text, cycle13 plan §2 (unchanged this cycle)",
	"internal/adapter/repo/redemption.go | 用户不存在":        "ErrRedemptionUserNotFound — switch-contract text (the 不存在 marker), cycle13 plan §2 (unchanged this cycle)",
	"internal/adapter/repo/redemption.go | 该兑换码不属于当前租户":  "ErrRedemptionWrongTenant — switch-contract text, cycle13 plan §2 (unchanged this cycle; matches no classifier marker, a gap switch_redeem.go's G5a comment already documents)",
	"internal/adapter/repo/redemption.go | 服务暂不可用，请稍后重试": "ErrRedemptionFailed — the pre-existing generic fallback switch_redeem_test.go's TestSwitchRedeemAnonymous_RawDBErrorSanitized pins",
	"internal/adapter/repo/redemption.go | 未提供兑换码":       "Redeem() empty-key guard — pinned byte-for-byte by cov_repo-deep_redemption_redeem_test.go:66 (that test file is not owned by L3)",
	"internal/adapter/repo/redemption.go | 无效的 user id":  "Redeem() zero-userId guard — pinned byte-for-byte by cov_repo-deep_redemption_redeem_test.go:69 (that test file is not owned by L3)",
	"internal/adapter/repo/user.go | id 为空！":             "pre-existing argument guard (nine sites); user.go is outside cycle13 L3's Owned list and the text is referenced by a1_provisioned_token_auth_test.go / cover_r2_billing_test.go",
	"internal/adapter/repo/user.go | email 为空！":          "pre-existing argument guard; user.go is outside cycle13 L3's Owned list",
	"internal/app/relay/helper/valid_request.go | size an unexpected error occurred in the parameter, please use 'x' instead of the multiplication sign '×'": "English prose that quotes the literal × (U+00D7 multiplication sign, not a CJK character) while telling the caller not to use it; valid_request.go is outside cycle13 L3's Owned list",
	// --- cycle14 L4: the sites the widened sinks and the new
	// internal/adapter/handler root made visible. Reason classes:
	//   console-v1     — a browser-only admin/self-service screen renders
	//                    this message verbatim; the product speaks Chinese
	//                    there by design.
	//   switch-contract — the Switch client classifies on the text, same
	//                    contract repo/redemption.go's entries document.
	//   product-client — a non-browser client that still shows the string
	//                    to a Chinese-language end user.
	//   bilingual      — "<Chinese> / <English>" in one literal, so the
	//                    machine-readable half is already present.
	//   DATA not a message — a name_cn / label FIELD, not an error text.
	// Every one of these sits in a file cycle14 L4 does not own; the three
	// it does own (auth.go's 401s) were fixed instead of whitelisted.
	"internal/adapter/handler/channel-billing.go | 多密钥渠道不支持余额查询":                                                         "console-v1: channel balance refresh button; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel-billing.go | 尚未实现":                                                                 "console-v1: channel balance refresh button; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel-test.go | 测试已在运行中":                                                                 "console-v1: channel test button; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | Vertex AI key JSON 编码失败: %w":                                                  "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | tag不能为空":                                                                      "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 不支持的操作":                                                                       "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 不支持的添加模式":                                                                     "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 不能删除最后一个密钥":                                                                   "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 参数覆盖格式错误：":                                                                    "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 参数错误":                                                                         "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 密钥已删除":                                                                        "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 密钥已启用":                                                                        "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 密钥已禁用":                                                                        "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 密钥索引超出范围":                                                                     "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 批量添加 Vertex AI 必须使用标准的JsonArray格式，例如[{key1}, {key2}...]，请检查输入: %w":            "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 批量添加 Vertex AI 的 keys 不能为空":                                                   "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 无权访问其它租户的资源":                                                                  "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 未指定要删除的密钥索引":                                                                  "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 未指定要启用的密钥索引":                                                                  "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 未指定要禁用的密钥索引":                                                                  "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 模型名称过长: %s":                                                                   "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 模型映射格式错误：":                                                                    "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 没有可禁用的密钥":                                                                     "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 没有需要删除的自动禁用密钥":                                                                "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 渠道不存在":                                                                        "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 该渠道不是多密钥模式":                                                                   "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 请求头覆盖格式错误：":                                                                   "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 追加密钥解析失败: ":                                                                   "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 部署地区不能为空":                                                                     "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 部署地区必须包含default字段":                                                            "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel.go | 部署地区必须是标准的Json格式，例如{\"default\": \"us-central1\", \"region2\": \"us-east1\"}": "console-v1: /api/channel admin screens render message verbatim; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel_key.go | 渠道ID格式错误: %w":                                                             "console-v1: channel key-reveal screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel_key.go | 渠道不存在":                                                                    "console-v1: channel key-reveal screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel_key.go | 获取成功":                                                                     "console-v1: channel key-reveal screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel_key.go | 获取渠道信息失败: %w":                                                             "console-v1: channel key-reveal screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel_probe_policy.go | 响应时间 %s 超过阈值 %s":                                                 "console-v1: probe failure reason shown in the channel list / auto-ban runbook; handler/ is outside L4 Owned",
	"internal/adapter/handler/channel_probe_policy.go | 探活超时（超过 %s）: %w":                                                 "console-v1: probe failure reason shown in the channel list / auto-ban runbook; handler/ is outside L4 Owned",
	"internal/adapter/handler/internal_currency.go | 标准":                                                                 "DATA not a message: an explicit name_cn / VIP label field with the ASCII name beside it (this gate reads map VALUES without the key)",
	"internal/adapter/handler/internal_currency.go | 白银":                                                                 "DATA not a message: an explicit name_cn / VIP label field with the ASCII name beside it (this gate reads map VALUES without the key)",
	"internal/adapter/handler/internal_currency.go | 路特":                                                                 "DATA not a message: an explicit name_cn / VIP label field with the ASCII name beside it (this gate reads map VALUES without the key)",
	"internal/adapter/handler/internal_currency.go | 钻石":                                                                 "DATA not a message: an explicit name_cn / VIP label field with the ASCII name beside it (this gate reads map VALUES without the key)",
	"internal/adapter/handler/internal_currency.go | 铂金":                                                                 "DATA not a message: an explicit name_cn / VIP label field with the ASCII name beside it (this gate reads map VALUES without the key)",
	"internal/adapter/handler/internal_currency.go | 陆币":                                                                 "DATA not a message: an explicit name_cn / VIP label field with the ASCII name beside it (this gate reads map VALUES without the key)",
	"internal/adapter/handler/internal_currency.go | 陆金":                                                                 "DATA not a message: an explicit name_cn / VIP label field with the ASCII name beside it (this gate reads map VALUES without the key)",
	"internal/adapter/handler/internal_currency.go | 黄金":                                                                 "DATA not a message: an explicit name_cn / VIP label field with the ASCII name beside it (this gate reads map VALUES without the key)",
	"internal/adapter/handler/model_meta.go | 模型名称不能为空":                                                                  "console-v1: model metadata admin screen, via the common.ApiErrorMsg wrapper; handler/ is outside L4 Owned",
	"internal/adapter/handler/model_meta.go | 模型名称已存在":                                                                   "console-v1: model metadata admin screen, via the common.ApiErrorMsg wrapper; handler/ is outside L4 Owned",
	"internal/adapter/handler/model_meta.go | 缺少模型 ID":                                                                   "console-v1: model metadata admin screen, via the common.ApiErrorMsg wrapper; handler/ is outside L4 Owned",
	"internal/adapter/handler/prefill_group.go | 组名称和类型不能为空":                                                             "console-v1: prefill-group admin screen, via the common.ApiErrorMsg wrapper; handler/ is outside L4 Owned",
	"internal/adapter/handler/prefill_group.go | 组名称已存在":                                                                 "console-v1: prefill-group admin screen, via the common.ApiErrorMsg wrapper; handler/ is outside L4 Owned",
	"internal/adapter/handler/prefill_group.go | 缺少组 ID":                                                                 "console-v1: prefill-group admin screen, via the common.ApiErrorMsg wrapper; handler/ is outside L4 Owned",
	"internal/adapter/handler/vendor_meta.go | 供应商名称不能为空":                                                                "console-v1: vendor metadata admin screen, via the common.ApiErrorMsg wrapper; handler/ is outside L4 Owned",
	"internal/adapter/handler/vendor_meta.go | 供应商名称已存在":                                                                 "console-v1: vendor metadata admin screen, via the common.ApiErrorMsg wrapper; handler/ is outside L4 Owned",
	"internal/adapter/handler/vendor_meta.go | 缺少供应商 ID":                                                                 "console-v1: vendor metadata admin screen, via the common.ApiErrorMsg wrapper; handler/ is outside L4 Owned",
	"internal/adapter/handler/misc.go | 数据库连接失败":                                                                         "console-v1: status/notice endpoint; handler/ is outside L4 Owned",
	"internal/adapter/handler/model_sync.go | 获取上游模型失败: ":                                                                "console-v1: model-sync admin screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/oauth.go | 无效的租户标识格式 / Invalid tenant slug format":                                         "bilingual: Chinese / English in one browser-OAuth string, the English half is already on the wire; handler/ is outside L4 Owned",
	"internal/adapter/handler/oauth.go | 无效的重定向URL / Invalid redirect URL":                                               "bilingual: Chinese / English in one browser-OAuth string, the English half is already on the wire; handler/ is outside L4 Owned",
	"internal/adapter/handler/oauth.go | 此账号所属组织与该租户不匹配 / This account's organization does not match this tenant":        "bilingual: Chinese / English in one browser-OAuth string, the English half is already on the wire; handler/ is outside L4 Owned",
	"internal/adapter/handler/oauth.go | 租户不存在 / Tenant not found":                                                       "bilingual: Chinese / English in one browser-OAuth string, the English half is already on the wire; handler/ is outside L4 Owned",
	"internal/adapter/handler/oauth.go | 租户已被禁用或暂停 / Tenant is disabled or suspended":                                    "bilingual: Chinese / English in one browser-OAuth string, the English half is already on the wire; handler/ is outside L4 Owned",
	"internal/adapter/handler/oauth.go | 租户标识不能为空 / Tenant slug is required":                                             "bilingual: Chinese / English in one browser-OAuth string, the English half is already on the wire; handler/ is outside L4 Owned",
	"internal/adapter/handler/openrouter_sync.go | 多模态视觉":                                                                "DATA not a message: category dropdown label for the console; the ASCII key field sits beside it",
	"internal/adapter/handler/openrouter_sync.go | 推理语言大模型":                                                              "DATA not a message: category dropdown label for the console; the ASCII key field sits beside it",
	"internal/adapter/handler/openrouter_sync.go | 文字转语音":                                                                "DATA not a message: category dropdown label for the console; the ASCII key field sits beside it",
	"internal/adapter/handler/openrouter_sync.go | 文生图":                                                                  "DATA not a message: category dropdown label for the console; the ASCII key field sits beside it",
	"internal/adapter/handler/openrouter_sync.go | 语音转文字":                                                                "DATA not a message: category dropdown label for the console; the ASCII key field sits beside it",
	"internal/adapter/handler/option.go | 图片倍率设置失败: ":                                                                    "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 无效的参数":                                                                         "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 无效的参数：value 不能为 null，清空请传空字符串":                                                 "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 无法启用 Discord OAuth，请先填入 Discord Client Id 以及 Discord Client Secret！":           "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 无法启用 GitHub OAuth，请先填入 GitHub Client Id 以及 GitHub Client Secret！":              "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 无法启用 LinuxDO OAuth，请先填入 LinuxDO Client Id 以及 LinuxDO Client Secret！":           "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 无法启用 OIDC 登录，请先填入 OIDC Client Id 以及 OIDC Client Secret！":                       "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 无法启用 Telegram OAuth，请先填入 Telegram Bot Token！":                                  "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 无法启用 Turnstile 校验，请先填入 Turnstile 校验相关配置信息！":                                    "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 无法启用微信登录，请先填入微信登录相关配置信息！":                                                      "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 无法启用邮箱域名限制，请先填入限制的邮箱域名！":                                                       "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 音频倍率设置失败: ":                                                                    "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/option.go | 音频补全倍率设置失败: ":                                                                  "console-v1: system-settings screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/playground.go | 暂不支持使用 access token":                                                       "console-v1: playground, session-authenticated console surface; handler/ is outside L4 Owned",
	"internal/adapter/handler/pricing.go | 重置模型倍率成功":                                                                     "console-v1: pricing admin screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/ratio_config.go | 倍率配置接口未启用":                                                               "console-v1: ratio-config admin screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/redemption.go | 一次兑换码批量生成的个数不能大于 100":                                                      "console-v1: redemption admin screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/redemption.go | 兑换码个数必须大于0":                                                                "console-v1: redemption admin screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/redemption.go | 兑换码名称长度必须在1-20之间":                                                          "console-v1: redemption admin screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/redemption.go | 已使用的兑换码状态不可修改":                                                             "console-v1: redemption admin screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/redemption.go | 无效的兑换码状态":                                                                  "console-v1: redemption admin screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/redemption.go | 过期时间不能早于当前时间":                                                              "console-v1: redemption admin screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/secure_verification.go | 不支持的验证方式":                                                         "console-v1: step-up verification flow, pinned by web/src/services/z1_secureVerification.test.js; handler/ is outside L4 Owned",
	"internal/adapter/handler/secure_verification.go | 已启用两步验证，请输入验证码":                                                   "console-v1: step-up verification flow, pinned by web/src/services/z1_secureVerification.test.js; handler/ is outside L4 Owned",
	"internal/adapter/handler/secure_verification.go | 未登录":                                                              "console-v1: step-up verification flow, pinned by web/src/services/z1_secureVerification.test.js; handler/ is outside L4 Owned",
	"internal/adapter/handler/secure_verification.go | 该用户已被禁用":                                                          "console-v1: step-up verification flow, pinned by web/src/services/z1_secureVerification.test.js; handler/ is outside L4 Owned",
	"internal/adapter/handler/secure_verification.go | 该验证码已被使用，请等待新的验证码":                                                "console-v1: step-up verification flow, pinned by web/src/services/z1_secureVerification.test.js; handler/ is outside L4 Owned",
	"internal/adapter/handler/secure_verification.go | 验证失败次数过多，请稍后再试":                                                   "console-v1: step-up verification flow, pinned by web/src/services/z1_secureVerification.test.js; handler/ is outside L4 Owned",
	"internal/adapter/handler/secure_verification.go | 验证成功":                                                             "console-v1: step-up verification flow, pinned by web/src/services/z1_secureVerification.test.js; handler/ is outside L4 Owned",
	"internal/adapter/handler/secure_verification.go | 验证码错误，请重试":                                                        "console-v1: step-up verification flow, pinned by web/src/services/z1_secureVerification.test.js; handler/ is outside L4 Owned",
	"internal/adapter/handler/setup.go | 保存演示站点模式设置失败: ":                                                                 "console-v1: first-run setup wizard, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/setup.go | 保存自用模式设置失败: ":                                                                   "console-v1: first-run setup wizard, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/setup.go | 创建管理员账号失败: ":                                                                    "console-v1: first-run setup wizard, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/setup.go | 用户名长度必须在1-12个字符之间":                                                              "console-v1: first-run setup wizard, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/setup.go | 系统初始化失败: ":                                                                      "console-v1: first-run setup wizard, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/setup.go | 系统初始化成功":                                                                        "console-v1: first-run setup wizard, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/setup.go | 系统已经初始化完成":                                                                      "console-v1: first-run setup wizard, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/setup.go | 请求参数有误":                                                                         "console-v1: first-run setup wizard, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/switch_redeem.go | 兑换成功":                                                                   "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 无法创建匿名账户，请稍后重试":                                                         "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 无法签发用户 token，请联系管理员":                                                    "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 服务暂不可用，请稍后重试":                                                           "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 激活码不存在":                                                                 "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 激活码已使用":                                                                 "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 激活码已被禁用":                                                                "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 激活码已过期":                                                                 "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 激活码必填":                                                                  "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 经销商账户已停用，请联系经销商":                                                        "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 设备指纹必填":                                                                 "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 该兑换码不属于当前租户":                                                            "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/switch_redeem.go | 请求体格式错误":                                                                "switch-contract: POST /api/v2/switch/redeem, the same Switch-client classifier text repo/redemption.go entries document (cycle13 plan 2)",
	"internal/adapter/handler/tenant_model_limits.go | model 名称无效（须为 1–255 字符的精确模型名）":                                    "console-v1: tenant model-limit screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/token.go | 参数错误":                                                                           "console-v1: token admin screen; handler/ is outside L4 Owned",
	"internal/adapter/handler/totp.go | 两步验证启用成功":                                                                        "console-v1: TOTP enrolment flow, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/totp.go | 两步验证已启用":                                                                         "console-v1: TOTP enrolment flow, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/totp.go | 两步验证已启用，如需重新配置请先禁用":                                                              "console-v1: TOTP enrolment flow, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/totp.go | 两步验证已禁用":                                                                         "console-v1: TOTP enrolment flow, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/totp.go | 两步验证未启用":                                                                         "console-v1: TOTP enrolment flow, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/totp.go | 未登录":                                                                             "console-v1: TOTP enrolment flow, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/totp.go | 请先获取两步验证密钥":                                                                      "console-v1: TOTP enrolment flow, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/totp.go | 请输入验证码":                                                                          "console-v1: TOTP enrolment flow, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/totp.go | 验证失败次数过多，请稍后再试":                                                                  "console-v1: TOTP enrolment flow, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/totp.go | 验证码错误，请重试":                                                                       "console-v1: TOTP enrolment flow, browser-only; handler/ is outside L4 Owned",
	"internal/adapter/handler/usedata.go | 时间跨度不能超过 1 个月":                                                                "console-v1: usage dashboard; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | Bark推送URL不能为空":                                                                   "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | Bark推送URL必须以http://或https://开头":                                                  "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | Gotify令牌不能为空":                                                                    "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | Gotify服务器地址不能为空":                                                                 "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | Gotify服务器地址必须以http://或https://开头":                                                "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | Webhook地址不能为空":                                                                   "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 不能降低自己的权限等级":                                                                     "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 无效的Bark推送URL":                                                                    "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 无效的Gotify服务器地址":                                                                  "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 无效的Webhook地址":                                                                    "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 无效的参数":                                                                           "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 无效的邮箱地址":                                                                         "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 无效的预警类型":                                                                         "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 无权将其他用户权限等级提升到大于等于自己的权限等级":                                                       "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 无权更新同权限等级或更高权限等级的用户信息":                                                           "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 无权获取同级或更高等级用户的信息":                                                                "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 更新设置失败: ":                                                                        "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 生成失败":                                                                            "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 设置已更新":                                                                           "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 设置更新成功":                                                                          "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 请重试，系统生成的 UUID 竟然重复了！":                                                           "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 输入不合法 ":                                                                          "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/user.go | 预警阈值必须大于0":                                                                       "console-v1: user admin + self-settings screens; handler/ is outside L4 Owned",
	"internal/adapter/handler/web_search.go | 联网搜索失败":                                                                    "product-client: POST /api/v2/lutu/search, shown to a Chinese-language end user in the Lutu app; handler/ is outside L4 Owned",
	"internal/adapter/handler/web_search.go | 联网搜索暂未配置":                                                                  "product-client: POST /api/v2/lutu/search, shown to a Chinese-language end user in the Lutu app; handler/ is outside L4 Owned",
	"internal/adapter/handler/zita_bootstrap.go | zita identity missing — SDK middleware did not validate session":       "ASCII prose whose only non-ASCII rune is the em dash U+2014 (a punctuation hit, not a language one); zita_bootstrap.go is outside L4 Owned - hand-off: swap the em dash for a plain hyphen",
	"internal/adapter/handler/zita_bootstrap.go | 该租户席位已满，请联系管理员 / This tenant has reached its user limit":               "bilingual: Chinese / English in one string, the English half is already on the wire; zita_bootstrap.go is outside L4 Owned",
	"internal/adapter/middleware/admin_jwt_auth.go | 无权进行此操作，权限不足":                                                       "this exact text is a KEY of rootSessionDenialsByMessage in the same file (the v2 rewrite fallback); changing it would silently reclassify the denial",
	"internal/adapter/middleware/auth.go | 所属租户已被禁用或暂停":                                                                  "kept by cycle14 L4 on purpose: this envelope carries error_code TENANT_DISABLED, so a machine client branches on the code and the sentence is display text",
	"internal/adapter/middleware/auth_playground.go | 所属租户已被禁用或暂停":                                                       "kept by cycle14 L4 on purpose: this envelope carries error_code TENANT_DISABLED, so a machine client branches on the code and the sentence is display text (PlaygroundAuth's copy, moved verbatim out of auth.go in cycle 14 to pay for its size ratchet)",
	"internal/adapter/middleware/auth.go | 无权进行此操作，access token 无效":                                                      "kept by cycle14 L4 on purpose: key of rootSessionDenialsByMessage (401 UNAUTHENTICATED); the v1 200-shaped console envelope",
	"internal/adapter/middleware/auth.go | 无权进行此操作，权限不足":                                                                 "kept by cycle14 L4 on purpose: key of rootSessionDenialsByMessage (403 PERMISSION_DENIED) AND of web/src/helpers/loadState.js PERMISSION_REFUSAL_MESSAGES",
	"internal/adapter/middleware/auth.go | 无权进行此操作，用户信息无效":                                                               "kept by cycle14 L4 on purpose: key of rootSessionDenialsByMessage (401 UNAUTHENTICATED); the v1 200-shaped console envelope",
	"internal/adapter/middleware/auth.go | 用户已被封禁":                                                                       "kept by cycle14 L4 on purpose: key of rootSessionDenialsByMessage (403 USER_DISABLED) and pinned byte-for-byte by root_or_granted_test.go:330",
	"internal/adapter/middleware/oidc_auth.go | Token 无效或已过期 / Invalid or expired token":                                 "bilingual: Chinese / English in one browser-SSO string; oidc_auth.go is outside L4 Owned",
	"internal/adapter/middleware/oidc_auth.go | 用户身份映射失败 / User identity mapping failed":                                 "bilingual: Chinese / English in one browser-SSO string; oidc_auth.go is outside L4 Owned",
	"internal/adapter/middleware/secure_verification.go | 未登录":                                                           "console step-up flow; pinned by web/src/services/z1_secureVerification.test.js; secure_verification.go is outside L4 Owned",
	"internal/adapter/middleware/secure_verification.go | 需要安全验证":                                                        "console step-up flow (the challenge prompt itself); secure_verification.go is outside L4 Owned",
	"internal/adapter/middleware/secure_verification.go | 验证已过期，请重新验证":                                                   "console step-up flow; secure_verification.go is outside L4 Owned",
	"internal/adapter/middleware/secure_verification.go | 验证状态异常，请重新验证":                                                  "console step-up flow; secure_verification.go is outside L4 Owned"}

// wireMessageGateSitesFloor is the scanner-honesty floor: today's scan of
// the three roots finds well over 100 sites (abortWithOpenAiMessage alone
// has 40+ call sites in this package — see abort_code_structural_test.go's
// own floor). Set far below that so ordinary edits do not need to bump it;
// it exists to catch a REGRESSION (the scan silently walking zero/few files)
// rather than to pin today's count.
const wireMessageGateSitesFloor = 20

// wireMessageGateRepoRoot walks up from this package directory to the
// module root (mirrors option_owned_globals_gate_test.go's
// optionGateRepoRoot).
func wireMessageGateRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the module root above this package")
	return ""
}

// wireMessageGateResponseWriters are the gin methods that write a response
// body straight to the client, added in cycle14 L4 — the sink family that let
// middleware/auth.go's eight Chinese refusals sit in a scanned file while
// this gate reported green (see the file-level comment).
//
// A call only counts as one of these when it has at least two arguments,
// which is what separates gin's `c.String(code, format, ...)` from the
// Stringer method of the same name that half this tree has.
var wireMessageGateResponseWriters = map[string]bool{
	"JSON":                 true,
	"AbortWithStatusJSON":  true,
	"IndentedJSON":         true,
	"PureJSON":             true,
	"SecureJSON":           true,
	"AsciiJSON":            true,
	"JSONP":                true,
	"XML":                  true,
	"YAML":                 true,
	"ProtoBuf":             true,
	"String":               true,
	"Data":                 true,
	"AbortWithStatusError": true,
}

// wireMessageGateResponseHelpers are this repo's OWN one-line wrappers around
// a direct response write (internal/pkg/common/gin.go). They are sinks for the
// same reason c.JSON is: common.ApiErrorMsg(c, "模型名称不能为空") in
// handler/model_meta.go puts that sentence in the response body, and nothing
// about the call site looks like an error constructor.
//
// common.ApiSuccess is deliberately NOT here: its second argument is a data
// payload, not a refusal sentence, and this gate is about the message a
// failure sends.
var wireMessageGateResponseHelpers = map[string]bool{
	"ApiErrorMsg": true,
	"ApiError":    true,
}

// wireMessageGateTargetName reports the matched target function's display
// name and whether call is one of the categories described in the file-level
// comment.
func wireMessageGateTargetName(call *ast.CallExpr) (string, bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		if fn.Name == "abortWithOpenAiMessage" || fn.Name == "MidjourneyErrorWrapper" {
			return fn.Name, true
		}
		if wireMessageGateResponseHelpers[fn.Name] {
			return fn.Name, true
		}
	case *ast.SelectorExpr:
		if wireMessageGateResponseHelpers[fn.Sel.Name] {
			return "common." + fn.Sel.Name, true
		}
		if strings.HasPrefix(fn.Sel.Name, "NewError") {
			return "types." + fn.Sel.Name, true
		}
		if fn.Sel.Name == "MidjourneyErrorWrapper" {
			return "MidjourneyErrorWrapper", true
		}
		if pkgIdent, ok := fn.X.(*ast.Ident); ok {
			if pkgIdent.Name == "errors" && fn.Sel.Name == "New" {
				return "errors.New", true
			}
			if pkgIdent.Name == "fmt" && fn.Sel.Name == "Errorf" {
				return "fmt.Errorf", true
			}
		}
		if wireMessageGateResponseWriters[fn.Sel.Name] && len(call.Args) >= 2 {
			recv := "response"
			if ident, ok := fn.X.(*ast.Ident); ok {
				recv = ident.Name
			}
			return recv + "." + fn.Sel.Name, true
		}
	}
	return "", false
}

// wireMessageGateStringLiterals returns every *ast.BasicLit STRING node
// directly reachable from expr through a chain of "+" BinaryExprs — i.e. the
// literal pieces of `"a" + x + "b"`-shaped arguments — and, since cycle14 L4,
// through composite literals, so the message inside `gin.H{"message": "…"}`
// is reached. It still does NOT descend into unrelated nested CallExprs (see
// the file-level comment for why that is deliberate and sufficient).
//
// For a keyed element only the VALUE is walked, never the key: a map key is a
// field name, not a sentence shown to anyone. A non-ASCII map KEY is
// therefore invisible to this gate, deliberately.
func wireMessageGateStringLiterals(expr ast.Expr) []*ast.BasicLit {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			return []*ast.BasicLit{e}
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			out := wireMessageGateStringLiterals(e.X)
			out = append(out, wireMessageGateStringLiterals(e.Y)...)
			return out
		}
	case *ast.KeyValueExpr:
		return wireMessageGateStringLiterals(e.Value)
	case *ast.CompositeLit:
		var out []*ast.BasicLit
		for _, elt := range e.Elts {
			out = append(out, wireMessageGateStringLiterals(elt)...)
		}
		return out
	case *ast.UnaryExpr:
		// &SomeStruct{Message: "…"}
		return wireMessageGateStringLiterals(e.X)
	case *ast.ParenExpr:
		return wireMessageGateStringLiterals(e.X)
	}
	return nil
}

// wireMessageGateLiteralValue decodes the BasicLit's source token into the
// string it denotes and reports the first non-ASCII rune in it (0 and false
// when the literal is pure ASCII). The decoded value doubles as the
// whitelist key, so it is returned either way.
func wireMessageGateLiteralValue(lit *ast.BasicLit) (string, rune, bool) {
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		// Not a plain quoted string (e.g. an unusual raw-string edge case) —
		// fall back to the raw token text minus its quote/backtick delimiters.
		value = strings.Trim(lit.Value, "`\"")
	}
	for _, r := range value {
		if r >= 0x80 {
			return value, r, true
		}
	}
	return value, 0, false
}

// wireMessageGateScanSource runs the gate's own two helpers over a source
// snippet and returns "<target> | <literal>" for every non-ASCII literal it
// would report. It is the seam the self-test below uses, so the self-test
// exercises the SAME matcher the real scan does rather than a copy of it.
func wireMessageGateScanSource(t *testing.T, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		targetName, matched := wireMessageGateTargetName(call)
		if !matched {
			return true
		}
		for _, arg := range call.Args {
			for _, lit := range wireMessageGateStringLiterals(arg) {
				value, _, isBad := wireMessageGateLiteralValue(lit)
				if isBad {
					found = append(found, targetName+" | "+value)
				}
			}
		}
		return true
	})
	sort.Strings(found)
	return found
}

// TestWireMessageLanguageGate_SeesADirectJSONResponse is this gate's
// self-test, and it exists because of what the gate missed: it sat in the
// same package as middleware/auth.go's eight Chinese refusals and reported
// green all cycle, because auth.go writes them with c.JSON(gin.H{...})
// instead of one of the error constructors the sink list matched.
//
// Two things must hold for the widening to be real, and the fixture below
// contains a case for each: the direct-response call has to be MATCHED as a
// sink at all, and the literal walker has to descend into the gin.H
// composite literal to reach the message. Before the widening the first
// failed; a half-widening that only added the sink would fail the second.
//
// The last fixture line is the false-positive control for adding String to
// the sink list: a no-argument `.String()` is a stringer, not a response.
func TestWireMessageLanguageGate_SeesADirectJSONResponse(t *testing.T) {
	const src = `package fixture

func refuse(c *gin.Context) {
	c.JSON(401, gin.H{"success": false, "message": "无权进行此操作，未登录且未提供 access token"})
	c.JSON(200, gin.H{"success": true, "message": "ok"})
	c.AbortWithStatusJSON(403, gin.H{"message": "所属租户已被禁用或暂停"})
	_ = c.Request.URL.String()
}
`
	got := wireMessageGateScanSource(t, src)
	want := []string{
		"c.AbortWithStatusJSON | 所属租户已被禁用或暂停",
		"c.JSON | 无权进行此操作，未登录且未提供 access token",
	}
	if len(got) != len(want) {
		t.Fatalf("gate found %d non-ASCII response literal(s) %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("finding[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWireMessageLanguageGate_ASCIIOnly(t *testing.T) {
	root := wireMessageGateRepoRoot(t)
	scanRoots := []string{
		filepath.Join(root, "internal", "adapter", "middleware"),
		filepath.Join(root, "internal", "adapter", "repo"),
		filepath.Join(root, "internal", "app", "relay"),
		filepath.Join(root, "internal", "adapter", "handler"),
	}

	fset := token.NewFileSet()
	sitesSeen := 0
	filesTouched := map[string]bool{}
	seenWhitelist := map[string]bool{}
	var violations []string

	for _, scanRoot := range scanRoots {
		walkErr := filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)

			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			file, parseErr := parser.ParseFile(fset, path, src, 0)
			if parseErr != nil {
				// A file another lane is mid-edit must not be reported as a
				// finding of this gate.
				t.Logf("skipping unparseable %s: %v", rel, parseErr)
				return nil
			}

			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				targetName, matched := wireMessageGateTargetName(call)
				if !matched {
					return true
				}
				sitesSeen++
				filesTouched[rel] = true

				for _, arg := range call.Args {
					for _, lit := range wireMessageGateStringLiterals(arg) {
						value, bad, isBad := wireMessageGateLiteralValue(lit)
						if !isBad {
							continue
						}
						key := rel + " | " + value
						if _, ok := wireMessageGateWhitelist[key]; ok {
							seenWhitelist[key] = true
							continue
						}
						line := fset.Position(lit.Pos()).Line
						// The key is printed QUOTED so it can be pasted into
						// wireMessageGateWhitelist verbatim: several of the
						// literals in this tree end in a space, and a key
						// transcribed by eye from unquoted output silently
						// fails to match and then shows up as "stale".
						violations = append(violations, strconv.Quote(key)+" (line "+strconv.Itoa(line)+"): "+targetName+"() argument contains non-ASCII rune "+strconv.QuoteRune(bad))
					}
				}
				return true
			})
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", scanRoot, walkErr)
		}
	}

	if sitesSeen < wireMessageGateSitesFloor {
		t.Fatalf("sitesSeen = %d, want >= %d (scanner-honesty floor — the scan found suspiciously few call sites; it may be broken, not the codebase clean)", sitesSeen, wireMessageGateSitesFloor)
	}
	if len(filesTouched) == 0 {
		t.Fatal("filesTouched = 0 — the scan touched no files, it is broken")
	}
	t.Logf("wire message language gate: %d call sites scanned across %d files", sitesSeen, len(filesTouched))

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("%d non-ASCII wire-message literal(s) found (API error.message must be English-only — cycle13 plan §2):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}

	// Whitelist-honesty: every entry must have actually matched a scanned
	// site this run, otherwise it is stale (the line moved, the file
	// changed, or the entry was never real) and silently hides nothing.
	var stale []string
	for key := range wireMessageGateWhitelist {
		if !seenWhitelist[key] {
			stale = append(stale, key)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("%d stale wire-message-gate whitelist entr(y/ies) never matched a scan site (the file moved/changed — update the whitelist):\n%s",
			len(stale), strings.Join(stale, "\n"))
	}
}
