package handler

// channel_sensitive_write.go — channel:sensitive_write (cycle 9, L2,
// auth-security-17/18 follow-up named in authz/catalog.go's own comment).
//
// api-router.go:151 gates merely READING a channel key behind RootAuth +
// CriticalRateLimit + DisableCache + SecureVerificationRequired, while the
// v1 POST/PUT writers and the v2 create/update writers sat behind nothing
// more than AdminAuth()/requireTenantAdmin — any tenant admin (role 10)
// could silently swap the upstream credential or redirect traffic to a
// different base_url. This file is the ONE shared predicate + gate the four
// entry points (v1 AddChannel/UpdateChannel, v2
// CreateChannelV2/UpdateChannelV2) call in-handler — the router itself is
// untouched this cycle (plan §3 L2: "no router edit").
//
// Scope, pinned by the cycle-9 plan (O5): the sensitive field set is
// exactly key, base_url, param_override, header_override, and the
// per-channel proxy setting — every field that changes where traffic goes
// or what credential it carries. models/group/name/priority/weight/status
// are deliberately NOT in this set; adding them would make ordinary channel
// administration require a grant, which the plan explicitly leaves out.
// Tag-scoped edits (PUT /api/channel/tag) are out of scope this cycle — a
// known non-goal, not a silent gap.

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
// existing (nil for a create) would write one of the five sensitive
// fields. "Touches" mirrors the write semantics both callers already use
// independently of this predicate: v1's repo.Channel.Update runs
// `DB.Model(channel).Updates(channel)`, and GORM's struct-Updates skips a
// zero string / nil pointer field — so req.Key == "" or a nil pointer field
// is already "not part of this write" by the rule the DB layer applies, not
// a fresh convention invented here. v2's UpdateChannelV2 merge loop (this
// package) makes the identical nil/empty check by hand before copying a
// field onto existingChannel. A create (existing == nil) has no prior
// state to diff against — a create request that populates a sensitive
// field carries the same power as an update that does, per the plan's
// "creating a channel carries the same power and the same gate".
//
// The per-channel proxy setting is the one field this predicate cannot
// treat as "provided means touched": Setting is a single JSON blob shared
// with several non-sensitive knobs (force_format, system_prompt, …), and a
// plain admin must keep being able to edit those without the grant. So for
// Setting specifically, the predicate decodes both sides and compares only
// the proxy value — the other knobs inside the same blob never trigger the
// gate.
func channelWriteTouchesSensitiveField(existing *repo.Channel, req *repo.Channel) bool {
	if req == nil {
		return false
	}
	if req.Key != "" {
		return true
	}
	if req.BaseURL != nil {
		return true
	}
	if req.ParamOverride != nil {
		return true
	}
	if req.HeaderOverride != nil {
		return true
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

// enforceChannelSensitiveWrite is the shared in-handler gate the four write
// entry points call after binding the request and before any mutation
// reaches the database. Root (role >= RoleRootUser) always passes. A
// non-root caller whose request does not touch a sensitive field also
// passes untouched — ordinary channel administration (name, models, group,
// priority, weight, status) needs no grant. Only a non-root write that DOES
// touch a sensitive field is checked against repo.HasActivePermissionGrant;
// on denial (no grant, or the lookup itself failing — fail closed, mirrors
// middleware.rootOrGrantedDecision) it writes 403 PERMISSION_DENIED, an
// audit row naming the refusal, and returns true so the caller returns
// immediately without writing anything — a silent field strip that still
// reports 200 would be a key rotation that "succeeds" and does nothing.
//
// existingID is the channel's id for the audit row's ResourceID — 0 on the
// create path, where there is no row yet.
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
