package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Channel override templates (migration 050), platform staff only
// (/api/v2/admin/channel-templates, RootJWTAuth). A template carries the same
// param_override / header_override documents a channel stores, validated by
// the same validators; applying it copies the text into the channels, so the
// existing relay executor runs it unchanged.

// maxTemplateApplyChannels bounds one bulk apply.
const maxTemplateApplyChannels = 500

type channelTemplateRequest struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	ParamOverride  string `json:"param_override"`
	HeaderOverride string `json:"header_override"`
}

func (r channelTemplateRequest) validate() (string, bool) {
	if r.Name == "" || len(r.Name) > 64 {
		return "name is required (<= 64 bytes)", false
	}
	if len(r.Description) > 255 {
		return "description longer than 255 bytes", false
	}
	if r.ParamOverride == "" && r.HeaderOverride == "" {
		return "a template needs a param_override or a header_override", false
	}
	if err := relaycommon.ValidateParamOverride(r.ParamOverride); err != nil {
		return err.Error(), false
	}
	if err := relaycommon.ValidateHeaderOverride(r.HeaderOverride); err != nil {
		return err.Error(), false
	}
	return "", true
}

func templateStatus(c *gin.Context, err error, what string) {
	switch {
	case errors.Is(err, repo.ErrChannelTemplateNotFound):
		dpFail(c, http.StatusNotFound, "TEMPLATE_NOT_FOUND", "Template not found")
	case errors.Is(err, repo.ErrChannelTemplateExists):
		dpFail(c, http.StatusConflict, "TEMPLATE_NAME_EXISTS", err.Error())
	default:
		common.SysError(what + ": " + err.Error())
		dpFail(c, http.StatusInternalServerError, "INTERNAL", "Failed to "+what)
	}
}

// ListChannelTemplatesV2 handles GET /admin/channel-templates.
func ListChannelTemplatesV2(c *gin.Context) {
	rows, err := repo.ListChannelOverrideTemplates()
	if err != nil {
		templateStatus(c, err, "list channel templates")
		return
	}
	if rows == nil {
		rows = []repo.ChannelOverrideTemplate{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rows})
}

// CreateChannelTemplateV2 handles POST /admin/channel-templates.
func CreateChannelTemplateV2(c *gin.Context) {
	var req channelTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid template body")
		return
	}
	if msg, ok := req.validate(); !ok {
		dpFail(c, http.StatusBadRequest, "INVALID_TEMPLATE", msg)
		return
	}
	t := &repo.ChannelOverrideTemplate{Name: req.Name, Description: req.Description,
		ParamOverride: req.ParamOverride, HeaderOverride: req.HeaderOverride, CreatedBy: int64(dpActor(c))}
	if err := repo.CreateChannelOverrideTemplate(t); err != nil {
		templateStatus(c, err, "create channel template")
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, dpActor(c),
		governance.ActionChannelTemplateCreated, governance.ResourceChannelTemplate, int(t.Id),
		fmt.Sprintf(`{"name":%q,"version":%d}`, t.Name, t.Version)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": t})
}

// UpdateChannelTemplateV2 handles PUT /admin/channel-templates/:id. It bumps
// the version; channels already carrying an older application are not
// rewritten until the template is applied again.
func UpdateChannelTemplateV2(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid template id")
		return
	}
	var req channelTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid template body")
		return
	}
	if msg, ok := req.validate(); !ok {
		dpFail(c, http.StatusBadRequest, "INVALID_TEMPLATE", msg)
		return
	}
	t, err := repo.UpdateChannelOverrideTemplate(id, req.Name, req.Description, req.ParamOverride, req.HeaderOverride)
	if err != nil {
		templateStatus(c, err, "update channel template")
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, dpActor(c),
		governance.ActionChannelTemplateUpdated, governance.ResourceChannelTemplate, int(t.Id),
		fmt.Sprintf(`{"name":%q,"version":%d}`, t.Name, t.Version)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": t})
}

// DeleteChannelTemplateV2 handles DELETE /admin/channel-templates/:id.
func DeleteChannelTemplateV2(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid template id")
		return
	}
	if err := repo.DeleteChannelOverrideTemplate(id); err != nil {
		templateStatus(c, err, "delete channel template")
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, dpActor(c),
		governance.ActionChannelTemplateDeleted, governance.ResourceChannelTemplate, int(id), `{}`))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ApplyChannelTemplateV2 handles POST /admin/channel-templates/:id/apply with
// {"channel_ids":[...]}. Per-channel results are returned; one missing channel
// does not abort the rest.
func ApplyChannelTemplateV2(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid template id")
		return
	}
	var req struct {
		ChannelIDs []int `json:"channel_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.ChannelIDs) == 0 {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "channel_ids is required")
		return
	}
	if len(req.ChannelIDs) > maxTemplateApplyChannels {
		dpFail(c, http.StatusBadRequest, "TOO_MANY_CHANNELS",
			fmt.Sprintf("at most %d channels per apply", maxTemplateApplyChannels))
		return
	}
	t, err := repo.GetChannelOverrideTemplate(id)
	if err != nil {
		templateStatus(c, err, "load channel template")
		return
	}
	results := repo.ApplyChannelOverrideTemplate(t, req.ChannelIDs, int64(dpActor(c)))
	applied := make([]int, 0, len(results))
	for _, r := range results {
		if r.Applied {
			applied = append(applied, r.ChannelId)
		}
	}
	repo.RefreshChannelCache(applied)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, dpActor(c),
		governance.ActionChannelTemplateApplied, governance.ResourceChannelTemplate, int(t.Id),
		fmt.Sprintf(`{"template_version":%d,"requested":%d,"applied":%d,"channel_ids":%v}`,
			t.Version, len(results), len(applied), applied)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"template_id": t.Id, "template_version": t.Version, "results": results,
	}})
}
