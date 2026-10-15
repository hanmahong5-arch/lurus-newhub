package entity

// SwitchTeamPack is the one signed team-asset bundle a reseller tenant has
// published for its white-label Switch end users, written by the root admin
// endpoint PUT /api/v2/admin/tenants/:id/switch-teampack and served by the
// public GET /api/v2/:tenant_slug/switch/teampack{,.sig,/meta} routes.
//
// The Hub is a size-bounded blob store only: it records sha256/size of the
// uploaded tar.gz and keeps the reseller's detached ed25519 signature (hex)
// verbatim, but never verifies it - the verification key lives in the
// white-label terminal, not here.
//
// One row per tenant (TenantID is the primary key); a re-publish replaces
// the row. Schema is created BOTH by migration 055 (switch_teampacks) and by
// repo.migrateDB's AutoMigrate call - same dual-creation pattern as 036/038
// /040; the migration's column types match what GORM derives here.
type SwitchTeamPack struct {
	TenantID  string `json:"tenant_id" gorm:"primaryKey;type:varchar(36);size:36"`
	Version   string `json:"version" gorm:"type:varchar(64);size:64;not null"`
	SHA256    string `json:"sha256" gorm:"column:sha256;type:varchar(64);size:64;not null"`
	Size      int64  `json:"size" gorm:"not null"`
	Blob      []byte `json:"-" gorm:"type:bytea"`
	Sig       string `json:"-" gorm:"type:text;not null"`
	UpdatedAt int64  `json:"updated_at" gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (SwitchTeamPack) TableName() string {
	return "switch_teampacks"
}
