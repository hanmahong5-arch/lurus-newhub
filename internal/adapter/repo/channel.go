package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// ChannelInfo is aliased from the canonical definition in domain/entity/channel.go.
type ChannelInfo = entity.ChannelInfo

// Channel mirrors the canonical persistence model in domain/entity/channel.go.
type Channel struct {
	Id                  int         `json:"id"`
	TenantId            string      `json:"tenant_id" gorm:"type:varchar(36);index;index:idx_tenant_status,priority:1;default:'default'"`
	Type                int         `json:"type" gorm:"default:0"`
	Key                 string      `json:"key" gorm:"not null"`
	OpenAIOrganization  *string     `json:"openai_organization"`
	TestModel           *string     `json:"test_model"`
	Status              int         `json:"status" gorm:"default:1;index:idx_tenant_status,priority:2"`
	Name                string      `json:"name" gorm:"index"`
	Weight              *uint       `json:"weight" gorm:"default:0"`
	CreatedTime         int64       `json:"created_time" gorm:"bigint"`
	TestTime            int64       `json:"test_time" gorm:"bigint"`
	ResponseTime        int         `json:"response_time"`
	BaseURL             *string     `json:"base_url" gorm:"column:base_url;default:''"`
	Other               string      `json:"other"`
	Balance             float64     `json:"balance"`
	BalanceUpdatedTime  int64       `json:"balance_updated_time" gorm:"bigint"`
	Models              string      `json:"models"`
	Group               string      `json:"group" gorm:"type:varchar(64);default:'default'"`
	UsedQuota           int64       `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping        *string     `json:"model_mapping" gorm:"type:text"`
	StatusCodeMapping   *string     `json:"status_code_mapping" gorm:"type:varchar(1024);default:''"`
	Priority            *int64      `json:"priority" gorm:"bigint;default:0"`
	AutoBan             *int        `json:"auto_ban" gorm:"default:1"`
	OtherInfo           string      `json:"other_info"`
	Tag                 *string     `json:"tag" gorm:"index"`
	Setting             *string     `json:"setting" gorm:"type:text"`
	ParamOverride       *string     `json:"param_override" gorm:"type:text"`
	HeaderOverride      *string     `json:"header_override" gorm:"type:text"`
	Remark              *string     `json:"remark" gorm:"type:varchar(255)" validate:"max=255"`
	ChannelInfo         ChannelInfo `json:"channel_info" gorm:"type:json"`
	OtherSettings       string      `json:"settings" gorm:"column:settings"`
	ManagedModelsBySync string      `json:"managed_models_by_sync" gorm:"type:text;default:''"`
	LastSyncFetchCount  int         `json:"last_sync_fetch_count" gorm:"default:0"`
	Keys                []string    `json:"-" gorm:"-"`
}

func (channel *Channel) SaveChannelInfo() error {
	return DB.Model(channel).Update("channel_info", channel.ChannelInfo).Error
}

func (channel *Channel) GetModels() []string {
	if channel.Models == "" {
		return []string{}
	}
	return strings.Split(strings.Trim(channel.Models, ","), ",")
}

// GetManagedModelsBySync parses the JSON-encoded managed model set.
func (channel *Channel) GetManagedModelsBySync() []string {
	if strings.TrimSpace(channel.ManagedModelsBySync) == "" {
		return []string{}
	}
	var arr []string
	if err := common.Unmarshal([]byte(channel.ManagedModelsBySync), &arr); err != nil {
		common.SysLog("Channel: failed to unmarshal ManagedModelsBySync: " + err.Error())
		return []string{}
	}
	return arr
}

// SetManagedModelsBySync serializes the given set into ManagedModelsBySync.
func (channel *Channel) SetManagedModelsBySync(models []string) error {
	if models == nil {
		models = []string{}
	}
	b, err := json.Marshal(models)
	if err != nil {
		return err
	}
	channel.ManagedModelsBySync = string(b)
	return nil
}

func (channel *Channel) GetGroups() []string {
	if channel.Group == "" {
		return []string{}
	}
	groups := strings.Split(strings.Trim(channel.Group, ","), ",")
	for i, group := range groups {
		groups[i] = strings.TrimSpace(group)
	}
	return groups
}

func (channel *Channel) GetOtherInfo() map[string]interface{} {
	otherInfo := make(map[string]interface{})
	if channel.OtherInfo != "" {
		if err := common.Unmarshal([]byte(channel.OtherInfo), &otherInfo); err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		}
	}
	return otherInfo
}

func (channel *Channel) SetOtherInfo(otherInfo map[string]interface{}) {
	otherInfoBytes, err := json.Marshal(otherInfo)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		return
	}
	channel.OtherInfo = string(otherInfoBytes)
}

func (channel *Channel) GetTag() string {
	if channel.Tag == nil {
		return ""
	}
	return *channel.Tag
}

func (channel *Channel) SetTag(tag string) {
	channel.Tag = &tag
}

func (channel *Channel) GetAutoBan() bool {
	if channel.AutoBan == nil {
		return false
	}
	return *channel.AutoBan == 1
}

func (channel *Channel) Save() error {
	return DB.Save(channel).Error
}

func (channel *Channel) SaveWithoutKey() error {
	if channel.Id == 0 {
		return errors.New("channel ID is 0")
	}
	return DB.Omit("key").Save(channel).Error
}

func (channel *Channel) GetPriority() int64 {
	if channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func (channel *Channel) GetWeight() int {
	if channel.Weight == nil {
		return 0
	}
	return int(*channel.Weight)
}

func (channel *Channel) GetBaseURL() string {
	if channel.BaseURL == nil {
		return ""
	}
	url := *channel.BaseURL
	if url == "" {
		url = constant.ChannelBaseURLs[channel.Type]
	}
	return url
}

func (channel *Channel) GetModelMapping() string {
	if channel.ModelMapping == nil {
		return ""
	}
	return *channel.ModelMapping
}

func (channel *Channel) GetStatusCodeMapping() string {
	if channel.StatusCodeMapping == nil {
		return ""
	}
	return *channel.StatusCodeMapping
}

func (channel *Channel) ValidateSettings() error {
	channelParams := &dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		if err := common.Unmarshal([]byte(*channel.Setting), channelParams); err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) GetSetting() dto.ChannelSettings {
	setting := dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		if err := common.Unmarshal([]byte(*channel.Setting), &setting); err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.Setting = nil
			if saveErr := channel.Save(); saveErr != nil {
				common.SysError(fmt.Sprintf("failed to self-heal corrupted setting: channel_id=%d, error=%v", channel.Id, saveErr))
			}
		}
	}
	return setting
}

func (channel *Channel) SetSetting(setting dto.ChannelSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.Setting = common.GetPointer[string](string(settingBytes))
}

func (channel *Channel) GetOtherSettings() dto.ChannelOtherSettings {
	setting := dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		if err := common.UnmarshalJsonStr(channel.OtherSettings, &setting); err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.OtherSettings = "{}"
			if saveErr := channel.Save(); saveErr != nil {
				common.SysError(fmt.Sprintf("failed to self-heal corrupted other_settings: channel_id=%d, error=%v", channel.Id, saveErr))
			}
		}
	}
	return setting
}

func (channel *Channel) SetOtherSettings(setting dto.ChannelOtherSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.OtherSettings = string(settingBytes)
}

func (channel *Channel) GetParamOverride() map[string]interface{} {
	paramOverride := make(map[string]interface{})
	if channel.ParamOverride != nil && *channel.ParamOverride != "" {
		if err := common.Unmarshal([]byte(*channel.ParamOverride), &paramOverride); err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal param override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return paramOverride
}

func (channel *Channel) GetHeaderOverride() map[string]interface{} {
	headerOverride := make(map[string]interface{})
	if channel.HeaderOverride != nil && *channel.HeaderOverride != "" {
		if err := common.Unmarshal([]byte(*channel.HeaderOverride), &headerOverride); err != nil {
			common.SysLog(fmt.Sprintf("failed to unmarshal header override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return headerOverride
}
