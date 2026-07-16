package repo

import (
	"errors"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Admin CRUD for per-model rate limits (entity.ModelRateLimit, migration 026).
// The relay-path READ lives in middleware/business_model_rate_limit.go
// (fetchModelBizLimitsFromDB), mirroring how the token/tenant dimensions keep
// their hot-path lookup beside the enforcement code.

// ListModelRateLimits returns all per-model limits configured for a tenant,
// ordered by model name for a stable admin listing.
func ListModelRateLimits(tenantID string) ([]entity.ModelRateLimit, error) {
	var rows []entity.ModelRateLimit
	err := DB.Where("tenant_id = ?", tenantID).Order("model").Find(&rows).Error
	return rows, err
}

// UpsertModelRateLimit creates or updates the (tenant, model) limit row and
// returns the persisted state. Uses the uk_model_rate_limits_tenant_model
// composite unique key so concurrent upserts cannot create duplicates.
func UpsertModelRateLimit(tenantID, model string, rpm, tpm int) (*entity.ModelRateLimit, error) {
	row := entity.ModelRateLimit{
		TenantId:     tenantID,
		Model:        model,
		RateLimitRPM: rpm,
		RateLimitTPM: tpm,
	}
	err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "model"}},
		DoUpdates: clause.AssignmentColumns([]string{"rpm_limit", "tpm_limit", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return nil, err
	}
	// On the conflict path some dialects leave row.Id at 0 — reload so the
	// response reflects the stored row either way.
	var stored entity.ModelRateLimit
	if err := DB.Where("tenant_id = ? AND model = ?", tenantID, model).Take(&stored).Error; err != nil {
		return nil, err
	}
	return &stored, nil
}

// DeleteModelRateLimit removes the (tenant, model) limit row. Returns false
// when no such row existed (callers surface that as 404, not an error).
func DeleteModelRateLimit(tenantID, model string) (bool, error) {
	res := DB.Where("tenant_id = ? AND model = ?", tenantID, model).Delete(&entity.ModelRateLimit{})
	if res.Error != nil && !errors.Is(res.Error, gorm.ErrRecordNotFound) {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
