package repo

// GetMissingModels returns model names that are referenced in the system
func GetMissingModels() ([]string, error) {
	return missingModels(GetEnabledModels())
}

// GetMissingModelsForTenant is GetMissingModels over the channels
// abilityTenantScope makes visible to this tenant, so a tenant admin's
// "models with no metadata row" hint does not enumerate other tenants'
// model names. tenantID == "" reproduces GetMissingModels.
func GetMissingModelsForTenant(tenantID string) ([]string, error) {
	return missingModels(GetEnabledModelsForTenant(tenantID))
}

// missingModels subtracts the models metadata table from an already-deduped
// list of enabled model names; the two exported wrappers above differ only in
// how that list was scoped.
func missingModels(models []string) ([]string, error) {
	if len(models) == 0 {
		return []string{}, nil
	}

	// 2. 查询已有的元数据模型名
	var existing []string
	if err := DB.Model(&Model{}).Where("model_name IN ?", models).Pluck("model_name", &existing).Error; err != nil {
		return nil, err
	}

	existingSet := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		existingSet[e] = struct{}{}
	}

	// 3. 收集缺失模型
	var missing []string
	for _, name := range models {
		if _, ok := existingSet[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing, nil
}
