package handler

// channel_sensitive_write.go — channel:sensitive_write (cycle 9, L2,
// auth-security-17/18 follow-up named in authz/catalog.go's own comment).
//
// api-router.go:151 gates merely READING a channel key behind RootAuth +
// CriticalRateLimit + DisableCache + SecureVerificationRequired, while the
// v1 POST/PUT writers and the v2 create/update writers sat behind nothing
// more than AdminAuth()/requireTenantAdmin — any tenant admin (role 10)
// could silently swap the upstream credential or redirect traffic to a
// different base_url. This file is the ONE shared predicate + gate called
// in-handler from seven call sites (see enforceChannelSensitiveWrite's own
// comment for the list) — the router itself is untouched this cycle (plan
// §3 L2: "no router edit").
//
// Scope, pinned by the cycle-9 plan (O5) and widened by the repair-round
// ruling R2: the sensitive field set is key, base_url, param_override,
// header_override, the per-channel proxy setting, type, other, and
// openai_organization — every field that changes where traffic goes or
// what credential it carries. type changes the derived upstream host when
// base_url is empty (entity/channel.go's BaseURL-or-ChannelBaseURLs[Type]
// fallback); other feeds api_version/region/plugin/bot_id
// (middleware/distributor.go); openai_organization rides as a header on
// the credential. models/group/name/priority/weight/status are
// deliberately NOT in this set; adding them would make ordinary channel
// administration require a grant, which the plan explicitly leaves out.
//
// Two explicit non-goals, NOT silent gaps: OtherSettings (json:"settings" —
// azure api-version and similar vendor knobs) is never diffed by this
// predicate, and inside the Setting/json:"setting" blob only the proxy
// member is diffed — force_format/system_prompt/and any other member of
// that same blob never trip the gate. (PUT /api/channel/tag was an earlier
// non-goal; R4 withdrew it — see EditTagChannels below, which the gate now
// covers for the two fields that struct actually carries.)

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// channelSensitiveWriteResource/Action name the (resource, action) pair
// registered in authz/catalog.go and looked up via
// repo.HasActivePermissionGrant — the same catalogue and repo call
// middleware.RootOrGranted uses for the audit-feed grant, applied here
// in-handler instead of as router middleware.
const (
	channelSensitiveWriteResource = "channel"
	channelSensitiveWriteAction   = "sensitive_write"
)

// channelWriteTouchesSensitiveField reports whether persisting req onto
// existing (nil for a create) would write one of the sensitive fields
// named in this file's header comment. "Touches" mirrors the write
// semantics every caller already applies independently of this predicate:
// v1's repo.Channel.Update runs `DB.Model(channel).Updates(channel)`, and
// GORM's struct-Updates skips a zero string / zero int / nil pointer field
// — so a zero-valued field on req is already "not part of this write" by
// the rule the DB layer applies, not a fresh convention invented here. A
// create (existing == nil) has no prior state to diff against — a create
// request that populates a sensitive field carries the same power as an
// update that does, per the plan's "creating a channel carries the same
// power and the same gate": every branch below degenerates to a plain
// presence check when existing is nil, because the "previous" value it
// diffs against is the type's zero value.
//
// R1 (repair-round ruling): base_url/param_override/header_override are
// VALUE-diffed, not presence-checked — the shipped channel editors (both
// v1's EditChannelModal.jsx and v2's Channel/index.jsx) always resend
// these fields on every save, including a plain rename, so a presence
// check would 403 every edit for a non-root admin. nil and "" compare
// equal (repo.Channel already treats them identically — see
// entity/channel.go). Key stays presence-based: neither editor resends the
// stored key on update (it is never echoed back to the client), and
// multi-key append mode makes a value diff wrong (the "new" value is a
// delta to append, not the full replacement).
//
// R2 (repair-round ruling): type/other/openai_organization join the set,
// also value-diffed — both shipped editors resend these on every save the
// same way they resend base_url, so these are presence-check-unsafe for
// the identical reason.
func channelWriteTouchesSensitiveField(existing *repo.Channel, req *repo.Channel) bool {
	if req == nil {
		return false
	}
	if req.Key != "" {
		return true
	}
	if strPtrChanged(existing, req.BaseURL, func(ch *repo.Channel) *string { return ch.BaseURL }) {
		return true
	}
	if strPtrChanged(existing, req.ParamOverride, func(ch *repo.Channel) *string { return ch.ParamOverride }) {
		return true
	}
	if strPtrChanged(existing, req.HeaderOverride, func(ch *repo.Channel) *string { return ch.HeaderOverride }) {
		return true
	}
	if strPtrChanged(existing, req.OpenAIOrganization, func(ch *repo.Channel) *string { return ch.OpenAIOrganization }) {
		return true
	}
	if req.Type != 0 {
		prevType := 0
		if existing != nil {
			prevType = existing.Type
		}
		if req.Type != prevType {
			return true
		}
	}
	if req.Other != "" {
		prevOther := ""
		if existing != nil {
			prevOther = existing.Other
		}
		if req.Other != prevOther {
			return true
		}
	}
	if req.Setting != nil {
		var existingProxy string
		if existing != nil {
			existingProxy = channelSettingProxy(existing.Setting)
		}
		if channelSettingProxy(req.Setting) != existingProxy {
			return true
		}
	}
	return false
}

// strPtrChanged reports whether reqVal (a sensitive *string field on req)
// differs from the corresponding field on existing, treating nil and ""
// as equal on both sides — the shared rule R1 applies to base_url,
// param_override and header_override, and R2 extends to
// openai_organization. A nil reqVal never touches anything (mirrors GORM
// skipping a nil pointer field on struct-Updates); existing == nil (create)
// diffs against the empty string, so any non-empty reqVal is a touch.
func strPtrChanged(existing *repo.Channel, reqVal *string, field func(*repo.Channel) *string) bool {
	if reqVal == nil {
		return false
	}
	prev := ""
	if existing != nil {
		if p := field(existing); p != nil {
			prev = *p
		}
	}
	return *reqVal != prev
}

// channelSettingProxy decodes only the proxy value out of a channel's raw
// Setting JSON blob, side-effect-free (unlike repo.Channel.GetSetting,
// which self-heals a corrupted document by writing nil back to the row —
// wrong for a predicate that must not touch the database before the gate
// it is deciding for has even run). A nil/empty/unparseable document reads
// as no proxy configured, same as an unset field.
func channelSettingProxy(setting *string) string {
	if setting == nil || *setting == "" {
		return ""
	}
	var s dto.ChannelSettings
	if err := common.Unmarshal([]byte(*setting), &s); err != nil {
		return ""
	}
	return s.Proxy
}

// enforceChannelSensitiveWrite is the shared in-handler gate called after
// binding the request and before any mutation reaches the database, from
// seven call sites: v1 AddChannel and UpdateChannel (channel.go), v2
// CreateChannelV2 and UpdateChannelV2 (v2_channel.go), and — added by the
// repair-round rulings R3/R4 — v1 CopyChannel, the delete_key and
// delete_disabled_keys branches of v1 ManageMultiKeys, and v1
// EditTagChannels (all channel.go). Root (role >= RoleRootUser) always
// passes. A non-root caller whose request does not touch a sensitive field
// also passes untouched — ordinary channel administration (name, models,
// group, priority, weight, status) needs no grant. Only a non-root write
// that DOES touch a sensitive field is checked against
// repo.HasActivePermissionGrant; on denial (no grant, or the lookup itself
// failing — fail closed, mirrors middleware.rootOrGrantedDecision) it
// writes 403 PERMISSION_DENIED, an audit row naming the refusal, and
// returns true so the caller returns immediately without writing anything
// — a silent field strip that still reports 200 would be a key rotation
// that "succeeds" and does nothing.
//
// existingID is the channel's id for the audit row's ResourceID — 0 on the
// create path (and on EditTagChannels, which targets many rows by tag, not
// one id).
//
// Root recognition (R11, repair-round ruling): only the integer "role"
// session/access-token key is checked — deliberately NOT widened to accept
// a JWT "root" string role the way requirePlatformRoot (v2_rbac.go) does,
// because every one of the seven call sites above is mounted under
// middleware.AdminAuth() (v1 channelRoute, router/api-router.go) or
// AdminAuth()+TenantSlugGuard() (v2 tenantChannels,
// router/api-v2-router.go) — both session/access-token-only middlewares
// that never populate a JWT role — so a JWT-authenticated caller can never
// reach this gate today. If any of these routes is ever remounted under a
// JWT-accepting middleware (as /credit-pool/me is under a different
// group), this check must be revisited.
func enforceChannelSensitiveWrite(c *gin.Context, existing *repo.Channel, req *repo.Channel, existingID int) bool {
	if !channelWriteTouchesSensitiveField(existing, req) {
		return false
	}
	if c.GetInt("role") >= common.RoleRootUser {
		return false
	}
	userID := c.GetInt("id")
	granted, err := repo.HasActivePermissionGrant(userID, channelSensitiveWriteResource, channelSensitiveWriteAction)
	if err == nil && granted {
		return false
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, userID,
		governance.ActionChannelSensitiveWriteRefused, governance.ResourceChannel, existingID, ""))
	c.JSON(http.StatusForbidden, gin.H{
		"success":    false,
		"message":    "insufficient permission",
		"error_code": "PERMISSION_DENIED",
	})
	return true
}
