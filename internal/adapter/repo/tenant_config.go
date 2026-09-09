package repo

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"gorm.io/gorm"
)

// Type alias pointing to entity package
type TenantConfig = entity.TenantConfig

// Re-export config type constants from entity
const (
	ConfigTypeString = entity.ConfigTypeString
	ConfigTypeInt    = entity.ConfigTypeInt
	ConfigTypeBool   = entity.ConfigTypeBool
	ConfigTypeJSON   = entity.ConfigTypeJSON
	ConfigTypeFloat  = entity.ConfigTypeFloat
)

// ErrTenantConfigNotFound is returned by GetTenantConfig when no row matches
// (tenant_id, config_key). Callers that need to tell "unconfigured" apart
// from a genuine DB fault use errors.Is(err, ErrTenantConfigNotFound) — a
// bare errors.New here would make that distinction impossible (2026-09-09
// L1 operator amendment: this used to re-wrap gorm.ErrRecordNotFound into a
// fresh error every call, so no caller's errors.Is check could ever match).
var ErrTenantConfigNotFound = errors.New("config not found")

// GetTenantConfig retrieves a configuration value by tenant ID and key
func GetTenantConfig(tenantID string, key string) (*TenantConfig, error) {
	var config TenantConfig
	err := DB.Where("tenant_id = ? AND config_key = ?", tenantID, key).First(&config).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTenantConfigNotFound
		}
		return nil, err
	}
	return &config, nil
}

// GetTenantConfigValue retrieves configuration value as string
func GetTenantConfigValue(tenantID string, key string, defaultValue string) string {
	config, err := GetTenantConfig(tenantID, key)
	if err != nil {
		return defaultValue
	}
	return config.ConfigValue
}

// GetTenantConfigInt retrieves configuration value as integer
func GetTenantConfigInt(tenantID string, key string, defaultValue int) int {
	config, err := GetTenantConfig(tenantID, key)
	if err != nil {
		return defaultValue
	}

	if config.ConfigType != ConfigTypeInt {
		// Try to convert
		val, err := strconv.Atoi(config.ConfigValue)
		if err != nil {
			return defaultValue
		}
		return val
	}

	val, err := strconv.Atoi(config.ConfigValue)
	if err != nil {
		return defaultValue
	}
	return val
}

// GetTenantConfigBool retrieves configuration value as boolean
func GetTenantConfigBool(tenantID string, key string, defaultValue bool) bool {
	config, err := GetTenantConfig(tenantID, key)
	if err != nil {
		return defaultValue
	}

	if config.ConfigType != ConfigTypeBool {
		// Try to convert
		val, err := strconv.ParseBool(config.ConfigValue)
		if err != nil {
			return defaultValue
		}
		return val
	}

	val, err := strconv.ParseBool(config.ConfigValue)
	if err != nil {
		return defaultValue
	}
	return val
}

// GetTenantConfigFloat retrieves configuration value as float64
func GetTenantConfigFloat(tenantID string, key string, defaultValue float64) float64 {
	config, err := GetTenantConfig(tenantID, key)
	if err != nil {
		return defaultValue
	}

	if config.ConfigType != ConfigTypeFloat {
		// Try to convert
		val, err := strconv.ParseFloat(config.ConfigValue, 64)
		if err != nil {
			return defaultValue
		}
		return val
	}

	val, err := strconv.ParseFloat(config.ConfigValue, 64)
	if err != nil {
		return defaultValue
	}
	return val
}

// GetTenantConfigJSON retrieves configuration value as JSON (unmarshals into provided interface)
func GetTenantConfigJSON(tenantID string, key string, target interface{}) error {
	config, err := GetTenantConfig(tenantID, key)
	if err != nil {
		return err
	}

	if config.ConfigType != ConfigTypeJSON {
		return errors.New("config type is not JSON")
	}

	return json.Unmarshal([]byte(config.ConfigValue), target)
}

// SetTenantConfig sets or updates a tenant configuration
func SetTenantConfig(tenantID string, key string, value string, configType string, description string, isSystem bool) error {
	// Check if config exists
	existingConfig, err := GetTenantConfig(tenantID, key)
	if err == nil {
		// Update existing config
		return UpdateTenantConfig(existingConfig.Id, map[string]interface{}{
			"config_value": value,
			"config_type":  configType,
			"description":  description,
		})
	}

	// Create new config
	config := &TenantConfig{
		TenantID:    tenantID,
		ConfigKey:   key,
		ConfigValue: value,
		ConfigType:  configType,
		Description: description,
		IsSystem:    isSystem,
		IsEncrypted: false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	return DB.Create(config).Error
}

// SetTenantConfigInt sets an integer configuration value
func SetTenantConfigInt(tenantID string, key string, value int, description string) error {
	return SetTenantConfig(tenantID, key, strconv.Itoa(value), ConfigTypeInt, description, false)
}

// SetTenantConfigBool sets a boolean configuration value
func SetTenantConfigBool(tenantID string, key string, value bool, description string) error {
	return SetTenantConfig(tenantID, key, strconv.FormatBool(value), ConfigTypeBool, description, false)
}

// SetTenantConfigFloat sets a float configuration value
func SetTenantConfigFloat(tenantID string, key string, value float64, description string) error {
	return SetTenantConfig(tenantID, key, strconv.FormatFloat(value, 'f', -1, 64), ConfigTypeFloat, description, false)
}

// SetTenantConfigJSON sets a JSON configuration value (marshals the provided interface)
func SetTenantConfigJSON(tenantID string, key string, value interface{}, description string) error {
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return SetTenantConfig(tenantID, key, string(jsonBytes), ConfigTypeJSON, description, false)
}

// UpdateTenantConfig updates tenant configuration
func UpdateTenantConfig(id int, updates map[string]interface{}) error {
	updates["updated_at"] = time.Now()
	return DB.Model(&TenantConfig{}).Where("id = ?", id).Updates(updates).Error
}

// DeleteTenantConfig deletes a tenant configuration
func DeleteTenantConfig(tenantID string, key string) error {
	return DB.Where("tenant_id = ? AND config_key = ?", tenantID, key).Delete(&TenantConfig{}).Error
}

// ListTenantConfigs retrieves all configurations for a tenant
func ListTenantConfigs(tenantID string, includeSystem bool) ([]*TenantConfig, error) {
	var configs []*TenantConfig
	query := DB.Where("tenant_id = ?", tenantID)

	if !includeSystem {
		query = query.Where("is_system = ?", false)
	}

	err := query.Order("config_key ASC").Find(&configs).Error
	return configs, err
}

// GetTenantConfigsByPrefix retrieves all configurations with a key prefix
// Useful for getting all configs in a namespace (e.g., "quota.*")
func GetTenantConfigsByPrefix(tenantID string, keyPrefix string) ([]*TenantConfig, error) {
	var configs []*TenantConfig
	err := DB.Where("tenant_id = ? AND config_key LIKE ?", tenantID, keyPrefix+"%").
		Order("config_key ASC").
		Find(&configs).Error
	return configs, err
}

// InitializeDefaultTenantConfigs creates default configurations for a new tenant
func InitializeDefaultTenantConfigs(tenantID string) error {
	// Default configurations
	configs := []TenantConfig{
		// User quota settings
		{TenantID: tenantID, ConfigKey: "quota.new_user_quota", ConfigValue: "10000", ConfigType: ConfigTypeInt, Description: "Default quota for new users (in tokens)", IsSystem: false},
		{TenantID: tenantID, ConfigKey: "quota.max_user_quota", ConfigValue: "1000000", ConfigType: ConfigTypeInt, Description: "Maximum quota per user (in tokens)", IsSystem: false},
		{TenantID: tenantID, ConfigKey: "quota.quota_reset_enabled", ConfigValue: "false", ConfigType: ConfigTypeBool, Description: "Enable monthly quota reset", IsSystem: false},

		// Billing settings
		{TenantID: tenantID, ConfigKey: "billing.currency", ConfigValue: "CNY", ConfigType: ConfigTypeString, Description: "Default currency for billing", IsSystem: false},
		{TenantID: tenantID, ConfigKey: "billing.tax_rate", ConfigValue: "0.13", ConfigType: ConfigTypeFloat, Description: "Tax rate (0.13 = 13%)", IsSystem: false},
		{TenantID: tenantID, ConfigKey: "billing.min_topup_amount", ConfigValue: "1", ConfigType: ConfigTypeInt, Description: "Minimum top-up amount", IsSystem: false},

		// Feature toggles
		{TenantID: tenantID, ConfigKey: "features.enable_meilisearch", ConfigValue: "true", ConfigType: ConfigTypeBool, Description: "Enable Meilisearch integration", IsSystem: true},
		{TenantID: tenantID, ConfigKey: "features.enable_subscriptions", ConfigValue: "true", ConfigType: ConfigTypeBool, Description: "Enable subscription system", IsSystem: true},
		{TenantID: tenantID, ConfigKey: "features.enable_redemptions", ConfigValue: "true", ConfigType: ConfigTypeBool, Description: "Enable redemption codes", IsSystem: true},
		{TenantID: tenantID, ConfigKey: "features.enable_oauth", ConfigValue: "true", ConfigType: ConfigTypeBool, Description: "Enable OAuth login", IsSystem: true},

		// Security settings
		{TenantID: tenantID, ConfigKey: "security.max_login_attempts", ConfigValue: "5", ConfigType: ConfigTypeInt, Description: "Maximum login attempts before lockout", IsSystem: true},
		{TenantID: tenantID, ConfigKey: "security.session_timeout", ConfigValue: "86400", ConfigType: ConfigTypeInt, Description: "Session timeout in seconds (24 hours)", IsSystem: true},
		{TenantID: tenantID, ConfigKey: "security.token_expiry", ConfigValue: "2592000", ConfigType: ConfigTypeInt, Description: "Access token expiry in seconds (30 days)", IsSystem: true},

		// Rate limiting
		{TenantID: tenantID, ConfigKey: "rate_limit.requests_per_minute", ConfigValue: "60", ConfigType: ConfigTypeInt, Description: "Maximum API requests per minute", IsSystem: false},
		{TenantID: tenantID, ConfigKey: "rate_limit.requests_per_day", ConfigValue: "10000", ConfigType: ConfigTypeInt, Description: "Maximum API requests per day", IsSystem: false},

		// Notification settings
		{TenantID: tenantID, ConfigKey: "notification.email_enabled", ConfigValue: "true", ConfigType: ConfigTypeBool, Description: "Enable email notifications", IsSystem: false},
		{TenantID: tenantID, ConfigKey: "notification.low_quota_threshold", ConfigValue: "1000", ConfigType: ConfigTypeInt, Description: "Quota threshold for low quota warning", IsSystem: false},
	}

	// Insert all configs
	for _, config := range configs {
		config.CreatedAt = time.Now()
		config.UpdatedAt = time.Now()

		// Use ON CONFLICT to avoid duplicates
		err := DB.Create(&config).Error
		if err != nil {
			// If duplicate, skip
			continue
		}
	}

	return nil
}

// CopyConfigsToNewTenant copies configurations from one tenant to another
// Useful when creating a new tenant based on a template
func CopyConfigsToNewTenant(sourceTenantID string, targetTenantID string, copySystemConfigs bool) error {
	var sourceConfigs []*TenantConfig
	query := DB.Where("tenant_id = ?", sourceTenantID)

	if !copySystemConfigs {
		query = query.Where("is_system = ?", false)
	}

	err := query.Find(&sourceConfigs).Error
	if err != nil {
		return err
	}

	// Copy each config to new tenant
	for _, sourceConfig := range sourceConfigs {
		newConfig := &TenantConfig{
			TenantID:    targetTenantID,
			ConfigKey:   sourceConfig.ConfigKey,
			ConfigValue: sourceConfig.ConfigValue,
			ConfigType:  sourceConfig.ConfigType,
			Description: sourceConfig.Description,
			IsSystem:    sourceConfig.IsSystem,
			IsEncrypted: sourceConfig.IsEncrypted,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		err = DB.Create(newConfig).Error
		if err != nil {
			// Skip if already exists
			continue
		}
	}

	return nil
}
