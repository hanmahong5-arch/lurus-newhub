package entity

type Ability struct {
	Group     string  `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	Model     string  `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int     `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool    `json:"enabled"`
	Priority  *int64  `json:"priority" gorm:"bigint;default:0;index"`
	Weight    uint    `json:"weight" gorm:"default:0;index"`
	Tag       *string `json:"tag" gorm:"index"`
}

type AbilityWithChannel struct {
	Ability
	ChannelType int `json:"channel_type"`
	// ChannelTenantId is the joined channel's tenant_id — the tenant
	// boundary the abilities table has no column of its own for (see
	// repo.abilityTenantScope). Catalogue builders project on it so a
	// tenant's private model names stay out of the public price list.
	// A row whose channel no longer exists (the builder's left join finds
	// no match) reads as the empty string, which repo/pricing.go counts as
	// platform-shared, the same way entity Channel.TenantId's "" does.
	ChannelTenantId string `json:"channel_tenant_id"`
}
