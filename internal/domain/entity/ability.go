package entity

type Ability struct {
	Group     string  `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	Model     string  `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int     `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool    `json:"enabled"`
	Priority  *int64  `json:"priority" gorm:"bigint;default:0;index"`
	Weight    uint    `json:"weight" gorm:"default:0;index"`
	Tag       *string `json:"tag" gorm:"index"`
	// Modality is the inferred kind of work this (group, model, channel) route
	// serves: chat | embedding | rerank | decision | image | audio. "" means
	// unknown and is treated as compatible with every request (migration 053).
	// Rewritten whenever the channel's abilities are rebuilt.
	Modality string `json:"modality" gorm:"type:varchar(16);not null;default:''"`
}

type AbilityWithChannel struct {
	Ability
	ChannelType int `json:"channel_type"`
	// ChannelTenantId is the joined channel's tenant_id — the tenant
	// boundary the abilities table has no column of its own for (see
	// repo.abilityTenantScope). Catalogue builders project on it so a
	// tenant's private model names stay out of the public price list.
	// "" here always means a real channel row whose tenant_id column is
	// empty or NULL, which IS platform-shared: the builder's join is inner
	// (repo.GetAllEnableAbilityWithChannels), so an ability whose channel
	// row is gone produces no row at all rather than a shared-looking one.
	ChannelTenantId string `json:"channel_tenant_id"`
}
