package repo

import (
	"errors"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"gorm.io/gorm"
)

// ChannelOverrideTemplate is a reusable param/header override document
// (migration 050). It deliberately stores the SAME JSON the channels'
// param_override / header_override columns hold, validated by the same
// validators and executed by the same relay executor: applying a template is
// a column copy, not a second override engine.
type ChannelOverrideTemplate struct {
	Id             int64  `json:"id" gorm:"primaryKey"`
	Name           string `json:"name" gorm:"type:varchar(64);not null;default:'';uniqueIndex:ux_channel_override_templates_name"`
	Description    string `json:"description" gorm:"type:varchar(255);not null;default:''"`
	ParamOverride  string `json:"param_override" gorm:"type:text;not null;default:''"`
	HeaderOverride string `json:"header_override" gorm:"type:text;not null;default:''"`
	Version        int64  `json:"version" gorm:"not null;default:1"`
	CreatedBy      int64  `json:"created_by" gorm:"not null;default:0"`
	CreatedAt      int64  `json:"created_at" gorm:"not null;default:0"`
	UpdatedAt      int64  `json:"updated_at" gorm:"not null;default:0"`
}

func (ChannelOverrideTemplate) TableName() string { return "channel_override_templates" }

// ChannelTemplateApplication records which template version was last written
// into a channel (the "source template id and version" of the channel's
// override fields). Append-only; the latest row per channel is current.
type ChannelTemplateApplication struct {
	Id              int64 `json:"id" gorm:"primaryKey"`
	ChannelId       int64 `json:"channel_id" gorm:"not null;default:0;index:idx_channel_template_applications_channel"`
	TemplateId      int64 `json:"template_id" gorm:"not null;default:0"`
	TemplateVersion int64 `json:"template_version" gorm:"not null;default:0"`
	AppliedBy       int64 `json:"applied_by" gorm:"not null;default:0"`
	AppliedAt       int64 `json:"applied_at" gorm:"not null;default:0"`
}

func (ChannelTemplateApplication) TableName() string { return "channel_template_applications" }

// Sentinel errors of the template store.
var (
	ErrChannelTemplateNotFound = errors.New("channel override template not found")
	ErrChannelTemplateExists   = errors.New("a channel override template with this name already exists")
)

// ListChannelOverrideTemplates lists all templates, newest first.
func ListChannelOverrideTemplates() ([]ChannelOverrideTemplate, error) {
	var out []ChannelOverrideTemplate
	err := DB.Order("id desc").Find(&out).Error
	return out, err
}

// GetChannelOverrideTemplate loads one template.
func GetChannelOverrideTemplate(id int64) (*ChannelOverrideTemplate, error) {
	var t ChannelOverrideTemplate
	err := DB.First(&t, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrChannelTemplateNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateChannelOverrideTemplate stores a new template at version 1.
func CreateChannelOverrideTemplate(t *ChannelOverrideTemplate) error {
	var n int64
	if err := DB.Model(&ChannelOverrideTemplate{}).Where("name = ?", t.Name).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return ErrChannelTemplateExists
	}
	now := common.GetTimestamp()
	t.Id, t.Version, t.CreatedAt, t.UpdatedAt = 0, 1, now, now
	return DB.Create(t).Error
}

// UpdateChannelOverrideTemplate rewrites a template and bumps its version, so
// a channel's recorded application can be compared with the current version
// to tell it is stale.
func UpdateChannelOverrideTemplate(id int64, name, description, paramOverride, headerOverride string) (*ChannelOverrideTemplate, error) {
	t, err := GetChannelOverrideTemplate(id)
	if err != nil {
		return nil, err
	}
	if name != t.Name {
		var n int64
		if err := DB.Model(&ChannelOverrideTemplate{}).Where("name = ? AND id <> ?", name, id).Count(&n).Error; err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, ErrChannelTemplateExists
		}
	}
	t.Name, t.Description, t.ParamOverride, t.HeaderOverride = name, description, paramOverride, headerOverride
	t.Version++
	t.UpdatedAt = common.GetTimestamp()
	if err := DB.Save(t).Error; err != nil {
		return nil, err
	}
	return t, nil
}

// DeleteChannelOverrideTemplate removes a template. Channels keep the override
// text already copied into them.
func DeleteChannelOverrideTemplate(id int64) error {
	res := DB.Delete(&ChannelOverrideTemplate{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrChannelTemplateNotFound
	}
	return nil
}

// TemplateApplyResult is the per-channel outcome of a bulk apply.
type TemplateApplyResult struct {
	ChannelId int    `json:"channel_id"`
	Applied   bool   `json:"applied"`
	Error     string `json:"error,omitempty"`
}

// ApplyChannelOverrideTemplate writes the template's override documents into
// each channel and records the source template id and version. A template
// field that is empty is left alone on the channel (a template may carry only
// params or only headers). Results are per channel: one missing channel does
// not abort the rest.
func ApplyChannelOverrideTemplate(t *ChannelOverrideTemplate, channelIDs []int, actor int64) []TemplateApplyResult {
	results := make([]TemplateApplyResult, 0, len(channelIDs))
	seen := make(map[int]bool, len(channelIDs))
	for _, id := range channelIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		res := TemplateApplyResult{ChannelId: id}
		if err := applyTemplateToChannel(t, id, actor); err != nil {
			res.Error = err.Error()
		} else {
			res.Applied = true
		}
		results = append(results, res)
	}
	return results
}

func applyTemplateToChannel(t *ChannelOverrideTemplate, channelID int, actor int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var ch Channel
		if err := WithoutTenantIsolation(tx).Where("id = ?", channelID).First(&ch).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("channel not found")
			}
			return err
		}
		updates := map[string]interface{}{}
		if t.ParamOverride != "" {
			updates["param_override"] = t.ParamOverride
		}
		if t.HeaderOverride != "" {
			updates["header_override"] = t.HeaderOverride
		}
		if len(updates) > 0 {
			if err := WithoutTenantIsolation(tx).Model(&Channel{}).Where("id = ?", channelID).Updates(updates).Error; err != nil {
				return err
			}
		}
		return tx.Create(&ChannelTemplateApplication{
			ChannelId: int64(channelID), TemplateId: t.Id, TemplateVersion: t.Version,
			AppliedBy: actor, AppliedAt: common.GetTimestamp(),
		}).Error
	})
}

// RefreshChannelCache reloads the given channels into the in-memory channel
// cache after a bulk write that bypassed Channel.Update.
func RefreshChannelCache(ids []int) {
	for _, id := range ids {
		var ch Channel
		if err := WithoutTenantIsolation(DB).Where("id = ?", id).First(&ch).Error; err == nil {
			CacheUpdateChannel(&ch)
		}
	}
}

// LatestTemplateApplication returns the most recent application for a channel
// or nil.
func LatestTemplateApplication(channelID int) (*ChannelTemplateApplication, error) {
	var a ChannelTemplateApplication
	err := DB.Where("channel_id = ?", channelID).Order("id desc").First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}
