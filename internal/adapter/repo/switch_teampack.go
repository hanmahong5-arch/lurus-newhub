package repo

// switch_teampack.go - persistence for switch_teampacks (migration 055), the
// one-row-per-tenant store behind handler.PutSwitchTeamPack and the public
// teampack GET routes. See entity.SwitchTeamPack for what the row holds.
//
// Every call keys on the tenant_id primary key alone and uses the bare DB
// handle (no tenant-scoped context): the handler resolves the tenant first
// and passes its id explicitly.

import (
	"errors"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrSwitchTeamPackNotFound is returned by GetSwitchTeamPack when the tenant
// has no published pack.
var ErrSwitchTeamPackNotFound = errors.New("switch teampack not found")

// GetSwitchTeamPack loads the tenant's published pack, blob included.
// Returns ErrSwitchTeamPackNotFound when none exists.
func GetSwitchTeamPack(tenantID string) (*entity.SwitchTeamPack, error) {
	var row entity.SwitchTeamPack
	err := DB.Where("tenant_id = ?", tenantID).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSwitchTeamPackNotFound
		}
		return nil, err
	}
	return &row, nil
}

// GetSwitchTeamPackMeta is GetSwitchTeamPack without the blob column, for the
// meta and .sig routes that never need the (up to 8 MiB) bundle bytes.
func GetSwitchTeamPackMeta(tenantID string) (*entity.SwitchTeamPack, error) {
	var row entity.SwitchTeamPack
	err := DB.Omit("blob").Where("tenant_id = ?", tenantID).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSwitchTeamPackNotFound
		}
		return nil, err
	}
	return &row, nil
}

// UpsertSwitchTeamPack inserts the pack or replaces the tenant's existing
// one. ON CONFLICT (tenant_id) DO UPDATE is atomic on both Postgres and
// SQLite, so two concurrent publishes cannot both insert.
func UpsertSwitchTeamPack(p *entity.SwitchTeamPack) error {
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"version", "sha256", "size", "blob", "sig", "updated_at",
		}),
	}).Create(p).Error
}

// DeleteSwitchTeamPack removes the tenant's pack. Deleting a tenant that has
// none is not an error (idempotent).
func DeleteSwitchTeamPack(tenantID string) error {
	return DB.Where("tenant_id = ?", tenantID).Delete(&entity.SwitchTeamPack{}).Error
}
